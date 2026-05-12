package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/chrislcrain/sedge/internal/agentlog"
	"github.com/chrislcrain/sedge/internal/instructions"
	"github.com/chrislcrain/sedge/internal/project"
	"github.com/chrislcrain/sedge/internal/tmux"
)

const (
	highlightBgCode = "\x1b[48;5;237m" // dark gray
	highlightBold   = "\x1b[1m"
	ansiReset       = "\x1b[0m"
)

// highlightRow wraps a row in the selection background and bold weight,
// padded to fill the pane width. Inner styles in `line` emit their own
// `\x1b[0m` resets which would otherwise clear the bg/bold mid-line; we
// re-apply both after every internal reset.
func highlightRow(line string, width int) string {
	open := highlightBgCode + highlightBold
	prefixed := "  " + line
	// After every full-reset emitted by an inner style, re-open bg + bold so
	// the highlight survives across segment boundaries.
	withBg := strings.ReplaceAll(prefixed, ansiReset, ansiReset+open)
	visible := lipgloss.Width(withBg)
	pad := ""
	if width > visible {
		pad = strings.Repeat(" ", width-visible)
	}
	return open + withBg + pad + ansiReset
}

const refreshInterval = 3 * time.Second

type rowKind int

const (
	rowProject rowKind = iota
	rowWorktree
	rowSubAgent // ephemeral row under a worktree showing an in-flight Agent call
	rowNewSession
)

type row struct {
	kind     rowKind
	project  *project.Project
	wt       *project.Worktree
	subAgent *project.SubAgentInfo
}

type mode int

const (
	modeList mode = iota
	modePromptSession
	modeSelectBranch
	modePromptNewBranch
	modePromptProject
	modeConfirmDelete
	modeConfirmCleanExit
	modeConfirmMerge
)

// branchOption is one row in the modeSelectBranch picker.
type branchOption struct {
	display string // human-readable label rendered in the picker
	branch  string // git branch name; "" for the "new branch" sentinel
	newMode bool   // true if selecting this should open the new-branch name prompt
	useDef  bool   // true if this is the "use default branch as base for sedge/<session>" option
}

type Model struct {
	cfg       project.Config
	worktrees map[string][]project.Worktree
	expanded  map[string]bool
	rows      []row
	cursor    int
	err       error
	status    string
	w, h      int

	mode  mode
	input textinput.Model

	pendingProject     *project.Project  // for modePromptSession/modeSelectBranch (which project gets the new session)
	pendingSessionName string            // session name carried from modePromptSession into modeSelectBranch
	branchOptions      []branchOption    // populated when entering modeSelectBranch
	branchCursor       int               // selection index into branchOptions
	pendingWt          *project.Worktree // for modeConfirmDelete/modeConfirmMerge (which wt)
	pendingWtP         *project.Project  // for modeConfirmDelete/modeConfirmMerge (its project)
}

func New() (Model, error) {
	cfg, err := project.Load()
	if err != nil {
		return Model{}, err
	}
	ti := textinput.New()
	ti.CharLimit = 256
	ti.Width = 50
	m := Model{
		cfg:       cfg,
		worktrees: map[string][]project.Worktree{},
		expanded:  map[string]bool{},
		input:     ti,
	}
	m.rebuild()
	return m, nil
}

type (
	reloadedMsg  struct{ cfg project.Config }
	worktreesMsg struct {
		project string
		list    []project.Worktree
	}
	spawnedMsg struct{ pane string }
	focusedMsg struct{ pane string }
	deletedMsg struct{ session string }
	errMsg     struct{ err error }
	clearStat  struct{}
)

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadAllWorktreesCmd(m.cfg), tickCmd())
}

type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modePromptSession, modePromptNewBranch, modePromptProject:
			return m.updatePromptInput(msg)
		case modeSelectBranch:
			return m.updateSelectBranch(msg)
		case modeConfirmDelete:
			return m.updateConfirmDelete(msg)
		case modeConfirmCleanExit:
			return m.updateConfirmCleanExit(msg)
		case modeConfirmMerge:
			return m.updateConfirmMerge(msg)
		}
		// modeList
		return m.updateList(msg)

	case reloadedMsg:
		m.cfg = msg.cfg
		m.rebuild()
		m.clampCursor()
		return m, loadAllWorktreesCmd(m.cfg)

	case worktreesMsg:
		m.worktrees[msg.project] = msg.list
		m.rebuild()
		m.clampCursor()
		return m, nil

	case spawnedMsg:
		m.status = "spawned " + msg.pane
		return m, tea.Batch(loadAllWorktreesCmd(m.cfg), clearStatusAfter(3*time.Second))

	case focusedMsg:
		m.status = "active: " + msg.pane
		return m, tea.Batch(loadAllWorktreesCmd(m.cfg), clearStatusAfter(2*time.Second))

	case deletedMsg:
		m.status = "recycled " + msg.session
		return m, tea.Batch(loadAllWorktreesCmd(m.cfg), clearStatusAfter(3*time.Second))

	case errMsg:
		m.err = msg.err
		return m, nil

	case clearStat:
		m.status = ""
		m.err = nil
		return m, nil

	case statusMsg:
		m.status = string(msg)
		return m, tea.Batch(loadAllWorktreesCmd(m.cfg), clearStatusAfter(4*time.Second))

	case tickMsg:
		return m, tea.Batch(loadAllWorktreesCmd(m.cfg), tickCmd())
	}
	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch keyFor(msg) {
	case actQuit:
		return m, tea.Quit
	case actDown:
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case actUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case actEnter:
		return m, m.activateRow()
	case actEdit:
		return m, editConfigCmd()
	case actReload:
		return m, tea.Batch(reloadCfgCmd(), loadAllWorktreesCmd(m.cfg))
	case actDelete:
		if r, ok := m.currentRow(); ok && r.kind == rowWorktree {
			m.pendingWt = r.wt
			m.pendingWtP = r.project
			m.mode = modeConfirmDelete
		}
		return m, nil
	case actMerge:
		if r, ok := m.currentRow(); ok && r.kind == rowWorktree {
			m.pendingWt = r.wt
			m.pendingWtP = r.project
			m.mode = modeConfirmMerge
		}
		return m, nil
	case actAddProject:
		m.input.Reset()
		m.input.Placeholder = "absolute path to a git-init'd directory"
		m.input.Focus()
		m.mode = modePromptProject
		return m, textinput.Blink
	case actOpenCode:
		return m, openCodePaneCmd()
	case actCleanExit:
		m.mode = modeConfirmCleanExit
		return m, nil
	}
	return m, nil
}

func (m Model) updateConfirmCleanExit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		root := m.cfg.WorktreesRoot
		if root == "" {
			root = "~/.sedge/worktrees"
		}
		return m, tea.Sequence(cleanExitCmd(root), tea.Quit)
	default:
		m.mode = modeList
		return m, nil
	}
}

func (m Model) updatePromptInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeList
		m.input.Blur()
		m.pendingProject = nil
		m.pendingSessionName = ""
		return m, nil
	case tea.KeyEnter:
		value := strings.TrimSpace(m.input.Value())
		switch m.mode {
		case modePromptSession:
			// Move on to the branch picker; carry session name forward.
			m.pendingSessionName = value
			m.input.Blur()
			m.branchOptions = buildBranchOptions(*m.pendingProject, value)
			m.branchCursor = 0
			m.mode = modeSelectBranch
			return m, nil
		case modePromptNewBranch:
			p := m.pendingProject
			session := m.pendingSessionName
			m.mode = modeList
			m.input.Blur()
			m.pendingProject = nil
			m.pendingSessionName = ""
			return m, spawnSessionCmd(*p, session, value, m.cfg)
		case modePromptProject:
			m.mode = modeList
			m.input.Blur()
			return m, addProjectCmd(value)
		}
	case tea.KeyTab:
		if m.mode == modePromptProject {
			if completed, ok := completePath(m.input.Value()); ok {
				m.input.SetValue(completed)
				m.input.SetCursor(len(completed))
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) updateSelectBranch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.pendingProject = nil
		m.pendingSessionName = ""
		m.branchOptions = nil
		return m, nil
	case "j", "down":
		if m.branchCursor < len(m.branchOptions)-1 {
			m.branchCursor++
		}
		return m, nil
	case "k", "up":
		if m.branchCursor > 0 {
			m.branchCursor--
		}
		return m, nil
	case "enter":
		if len(m.branchOptions) == 0 {
			return m, nil
		}
		opt := m.branchOptions[m.branchCursor]
		switch {
		case opt.newMode:
			// User wants to name a new branch — open the name prompt.
			m.input.Reset()
			m.input.Placeholder = "new branch name (off " + m.pendingProject.ResolvedDefaultBranch() + ")"
			m.input.Focus()
			m.mode = modePromptNewBranch
			m.branchOptions = nil
			return m, textinput.Blink
		case opt.useDef:
			// Default mode: empty branchInput → spec creates new sedge/<session> off default.
			p := m.pendingProject
			session := m.pendingSessionName
			m.mode = modeList
			m.pendingProject = nil
			m.pendingSessionName = ""
			m.branchOptions = nil
			return m, spawnSessionCmd(*p, session, "", m.cfg)
		default:
			// Check out an existing local branch.
			p := m.pendingProject
			session := m.pendingSessionName
			branch := opt.branch
			m.mode = modeList
			m.pendingProject = nil
			m.pendingSessionName = ""
			m.branchOptions = nil
			return m, spawnSessionCmd(*p, session, branch, m.cfg)
		}
	}
	return m, nil
}

// buildBranchOptions composes the list shown in modeSelectBranch.
// Order: default-branch-as-base, each other local branch not currently
// checked out (check out), new branch sentinel.
func buildBranchOptions(p project.Project, sessionName string) []branchOption {
	def := p.ResolvedDefaultBranch()
	if def == "" {
		def = project.DetectDefaultBranch(p.Path)
	}
	displayName := slugifySession(sessionName)
	if displayName == "" {
		displayName = "<auto>"
	}
	opts := []branchOption{
		{
			display: fmt.Sprintf("%s  (base for new sedge/%s)", def, displayName),
			branch:  def,
			useDef:  true,
		},
	}
	branches, _ := project.ListLocalBranches(p.Path)
	checkedOut, _ := project.CheckedOutBranches(p.Path)
	for _, b := range branches {
		if b == def {
			continue
		}
		if checkedOut[b] {
			// Already checked out elsewhere — git won't allow another worktree on it.
			continue
		}
		opts = append(opts, branchOption{
			display: fmt.Sprintf("%s  (check out)", b),
			branch:  b,
		})
	}
	opts = append(opts, branchOption{
		display: fmt.Sprintf("+ new branch from %s …", def),
		newMode: true,
	})
	return opts
}

func (m Model) updateConfirmMerge(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		p := m.pendingWtP
		wt := m.pendingWt
		m.mode = modeList
		m.pendingWt = nil
		m.pendingWtP = nil
		return m, mergeWorktreeCmd(*p, *wt)
	default:
		m.mode = modeList
		m.pendingWt = nil
		m.pendingWtP = nil
		return m, nil
	}
}

func (m Model) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		p := m.pendingWtP
		wt := m.pendingWt
		m.mode = modeList
		m.pendingWt = nil
		m.pendingWtP = nil
		return m, deleteWorktreeCmd(*p, *wt)
	default:
		m.mode = modeList
		m.pendingWt = nil
		m.pendingWtP = nil
		return m, nil
	}
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(renderBanner())
	b.WriteString("\n")

	if len(m.cfg.Projects) == 0 {
		b.WriteString(emptyStyle.Render("no projects registered"))
		b.WriteString("\n")
		b.WriteString(emptyStyle.Render("press 'n' to add"))
		b.WriteString("\n")
	} else {
		for i, r := range m.rows {
			if i > 0 && r.kind == rowProject {
				// Subtle horizontal rule between projects — borders are
				// muted now that the cursor is shown by row highlight.
				b.WriteString(lightDividerStyle.Render(strings.Repeat("─", dividerWidth(m.w))))
				b.WriteString("\n")
			}
			line := m.renderRow(r)
			if i == m.cursor {
				w := m.w
				if w <= 0 {
					w = lipgloss.Width(line) + 4
				}
				b.WriteString(highlightRow(line, w))
			} else {
				b.WriteString(itemStyle.Render(line))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(renderHelp())

	switch m.mode {
	case modePromptSession:
		b.WriteString("\n")
		b.WriteString(promptStyle.Render("Name for new session (Enter for auto):"))
		b.WriteString("\n  " + m.input.View())
	case modeSelectBranch:
		b.WriteString("\n")
		b.WriteString(promptStyle.Render("Pick a branch:"))
		b.WriteString("\n")
		for i, opt := range m.branchOptions {
			line := "  " + opt.display
			if i == m.branchCursor {
				w := m.w
				if w <= 0 {
					w = lipgloss.Width(line) + 4
				}
				b.WriteString(highlightRow(line, w))
			} else {
				b.WriteString(itemStyle.Render(line))
			}
			b.WriteString("\n")
		}
		b.WriteString(helpStyle.Render("j/k navigate · ⏎ select · esc cancel"))
	case modePromptNewBranch:
		b.WriteString("\n")
		b.WriteString(promptStyle.Render("New branch name:"))
		b.WriteString("\n  " + m.input.View())
	case modePromptProject:
		b.WriteString("\n")
		b.WriteString(promptStyle.Render("Path to git repo:"))
		b.WriteString("\n  " + m.input.View())
	case modeConfirmDelete:
		if m.pendingWt != nil {
			b.WriteString("\n")
			b.WriteString(promptStyle.Render(fmt.Sprintf("Delete worktree %q and recycle its history? (y/N)", m.pendingWt.SessionName)))
		}
	case modeConfirmMerge:
		if m.pendingWt != nil {
			meta, _ := project.ReadWorktreeMeta(m.pendingWt.Path)
			src := meta.SourceBranch
			wtBranch := meta.WorktreeBranch
			if src == "" {
				src = m.pendingWtP.ResolvedDefaultBranch()
			}
			if wtBranch == "" {
				wtBranch = m.pendingWt.Branch
			}
			b.WriteString("\n")
			b.WriteString(promptStyle.Render(fmt.Sprintf("Merge %s into %s? (y/N)", wtBranch, src)))
		}
	case modeConfirmCleanExit:
		b.WriteString("\n")
		b.WriteString(promptStyle.Render("Kill all sedge claude sessions and quit? (y/N)"))
	}

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errStyle.Render("error: " + m.err.Error()))
	} else if m.status != "" {
		b.WriteString("\n")
		b.WriteString(helpStyle.Render(m.status))
	}
	return b.String()
}

func dividerWidth(w int) int {
	if w <= 4 {
		return 20
	}
	if w > 60 {
		return 40
	}
	return w - 4
}

func (m Model) renderRow(r row) string {
	switch r.kind {
	case rowProject:
		chev := "▸"
		if m.expanded[r.project.Name] {
			chev = "▾"
		}
		wts := m.worktrees[r.project.Name]
		return fmt.Sprintf("%s %s  %s", chev, r.project.Name, dim(fmt.Sprintf("(%d)", len(wts))))
	case rowWorktree:
		dot := dormantStyle.Render("○")
		switch r.wt.State {
		case project.WtActive:
			dot = activeStyle.Render("●")
		case project.WtWaiting:
			dot = waitingStyle.Render("⚠")
		case project.WtBackground:
			dot = backgroundStyle.Render("◐")
		}
		return treeBranchStyle.Render("  │ ") + dot + " " + r.wt.SessionName
	case rowSubAgent:
		desc := r.subAgent.Description
		if desc == "" {
			desc = r.subAgent.Type
		}
		if max := 40; len(desc) > max {
			desc = desc[:max-1] + "…"
		}
		return treeBranchStyle.Render("  │   ↳ ") + subAgentStyle.Render(r.subAgent.Type) + " " + dim(desc)
	case rowNewSession:
		return treeBranchStyle.Render("  │ ") + newSessionStyle.Render("+ new session")
	}
	return ""
}

func (m *Model) rebuild() {
	rows := make([]row, 0, len(m.cfg.Projects)*2)
	for i := range m.cfg.Projects {
		p := &m.cfg.Projects[i]
		rows = append(rows, row{kind: rowProject, project: p})
		if !m.expanded[p.Name] {
			continue
		}
		wts := m.worktrees[p.Name]
		for j := range wts {
			rows = append(rows, row{kind: rowWorktree, project: p, wt: &wts[j]})
			for k := range wts[j].SubAgents {
				rows = append(rows, row{kind: rowSubAgent, project: p, wt: &wts[j], subAgent: &wts[j].SubAgents[k]})
			}
		}
		rows = append(rows, row{kind: rowNewSession, project: p})
	}
	m.rows = rows
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m Model) currentRow() (row, bool) {
	if len(m.rows) == 0 || m.cursor < 0 || m.cursor >= len(m.rows) {
		return row{}, false
	}
	return m.rows[m.cursor], true
}

func (m *Model) activateRow() tea.Cmd {
	r, ok := m.currentRow()
	if !ok {
		return nil
	}
	switch r.kind {
	case rowProject:
		m.expanded[r.project.Name] = !m.expanded[r.project.Name]
		m.rebuild()
		m.clampCursor()
		if m.expanded[r.project.Name] {
			return loadWorktreesCmd(*r.project)
		}
		return nil
	case rowNewSession:
		if !tmux.InsideTmux() {
			m.err = fmt.Errorf("not inside tmux; restart sedge from a non-tmux shell")
			return nil
		}
		m.pendingProject = r.project
		m.input.Reset()
		m.input.Placeholder = "e.g. fix-login (Enter for auto)"
		m.input.Focus()
		m.mode = modePromptSession
		return textinput.Blink
	case rowWorktree:
		if !tmux.InsideTmux() {
			m.err = fmt.Errorf("not inside tmux; restart sedge from a non-tmux shell")
			return nil
		}
		return swapToWorktreeCmd(*r.project, *r.wt, m.cfg)
	case rowSubAgent:
		if !tmux.InsideTmux() {
			m.err = fmt.Errorf("not inside tmux; restart sedge from a non-tmux shell")
			return nil
		}
		return viewSubAgentCmd(r.wt.Path, r.subAgent.Description)
	}
	return nil
}

// ---- commands ----

func loadWorktreesCmd(p project.Project) tea.Cmd {
	return func() tea.Msg {
		list, err := project.ListWorktrees(p.Path)
		if err != nil {
			return errMsg{err}
		}
		activePath, _ := tmux.ActiveSlotPath(os.Getenv("TMUX_PANE"))
		for i := range list {
			winID, _, _ := tmux.FindWorktreeWindow(list[i].Path)
			list[i].WindowID = winID
			switch {
			case list[i].Path == activePath:
				list[i].State = project.WtActive
			case winID != "" && tmux.WindowActivity(winID):
				list[i].State = project.WtWaiting
			case winID != "":
				list[i].State = project.WtBackground
			default:
				list[i].State = project.WtDormant
			}
			// Sub-agents only matter for live worktrees — dormant ones can't
			// be running anything right now.
			if list[i].State != project.WtDormant {
				if subs, err := agentlog.ActiveSubAgents(list[i].Path); err == nil {
					for _, s := range subs {
						list[i].SubAgents = append(list[i].SubAgents, project.SubAgentInfo{
							ID:          s.ID,
							Type:        s.Type,
							Description: s.Description,
						})
					}
				}
			}
		}
		return worktreesMsg{project: p.Name, list: list}
	}
}

func loadAllWorktreesCmd(cfg project.Config) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(cfg.Projects))
	for _, p := range cfg.Projects {
		cmds = append(cmds, loadWorktreesCmd(p))
	}
	return tea.Batch(cmds...)
}

func reloadCfgCmd() tea.Cmd {
	return func() tea.Msg {
		cfg, err := project.Load()
		if err != nil {
			return errMsg{err}
		}
		return reloadedMsg{cfg: cfg}
	}
}

// spawnSessionCmd creates a worktree per the user's branch input, then swaps
// claude into the slot.
//
// branchInput interpretation:
//   - "" → base on default branch, create new "sedge/<session>" branch
//   - existing local branch → check out that branch directly (no new branch)
//   - any other name → create that name as a new branch off the default branch
func spawnSessionCmd(p project.Project, requestedName, branchInput string, cfg project.Config) tea.Cmd {
	return func() tea.Msg {
		sessionName := slugifySession(requestedName)
		if sessionName == "" {
			sessionName = fmt.Sprintf("s%d", time.Now().Unix())
		}
		defaultBranch := p.ResolvedDefaultBranch()
		if defaultBranch == "" {
			defaultBranch = project.DetectDefaultBranch(p.Path)
		}
		wtPath := project.WorktreePath(cfg.WorktreesRoot, p, sessionName)

		spec := project.WorktreeSpec{
			RepoPath:    p.Path,
			SessionName: sessionName,
			WtPath:      wtPath,
			BaseBranch:  defaultBranch,
		}
		branchInput = strings.TrimSpace(branchInput)
		switch {
		case branchInput == "":
			// default: new sedge/<session> off main
		case project.HasLocalBranch(p.Path, branchInput):
			spec.CheckoutExisting = true
			spec.BaseBranch = branchInput
		default:
			spec.NewBranchName = branchInput
		}

		if err := project.CreateWorktree(spec); err != nil {
			return errMsg{err}
		}
		wtBranch := spec.NewBranchName
		if spec.CheckoutExisting {
			wtBranch = spec.BaseBranch
		} else if wtBranch == "" {
			wtBranch = project.BranchName(sessionName)
		}
		return spawnIntoSlot(p, project.Worktree{SessionName: sessionName, Path: wtPath, Branch: wtBranch}, cfg)
	}
}

func mergeWorktreeCmd(p project.Project, wt project.Worktree) tea.Cmd {
	return func() tea.Msg {
		meta, err := project.ReadWorktreeMeta(wt.Path)
		if err != nil {
			// Fall back to inferring source = project's default branch.
			meta = project.WorktreeMeta{
				SourceBranch:   p.ResolvedDefaultBranch(),
				WorktreeBranch: wt.Branch,
			}
		}
		out, err := project.MergeWorktreeBack(p.Path, meta)
		if err != nil {
			return errMsg{fmt.Errorf("%w\n%s", err, strings.TrimSpace(out))}
		}
		return statusMsg(fmt.Sprintf("merged %s into %s", meta.WorktreeBranch, meta.SourceBranch))
	}
}

type statusMsg string

func swapToWorktreeCmd(p project.Project, wt project.Worktree, cfg project.Config) tea.Cmd {
	return func() tea.Msg {
		return spawnIntoSlot(p, wt, cfg)
	}
}

// spawnIntoSlot brings a claude session into the visible slot next to sedge.
//
// If a tmux window already exists for the worktree (the claude is alive in
// the background), the existing pane is swapped into the slot — preserving
// its conversation state and any in-flight work. Otherwise a new claude is
// spawned in a background window (with --continue if there's prior history),
// then swapped in.
//
// The previous slot pane, if any, is broken out into its own background
// window so the claude running there keeps going.
func spawnIntoSlot(p project.Project, wt project.Worktree, cfg project.Config) tea.Msg {
	sedgePane := os.Getenv("TMUX_PANE")

	// Try to find an existing window for this worktree.
	_, paneID, err := tmux.FindWorktreeWindow(wt.Path)
	if err != nil {
		return errMsg{err}
	}
	if paneID != "" {
		if err := tmux.SwapInPane(sedgePane, paneID, cfg.SedgeWidthCols, cfg.SlotWidthPercent); err != nil {
			return errMsg{err}
		}
		return focusedMsg{pane: wt.SessionName}
	}

	// No live window — spawn one in the background, then swap it in.
	promptFile, err := instructions.Resolve(p.Path, p.Name, wt.SessionName, instructions.DelegationPolicy{
		MaxParallel: cfg.MaxParallelSubAgents,
		MaxDepth:    cfg.MaxSubAgentDepth,
	})
	if err != nil {
		return errMsg{err}
	}
	agentsJSON, _ := instructions.LoadAgentsJSON()
	_, newPane, err := tmux.SpawnClaudeWindow(tmux.SpawnClaudeOpts{
		WorktreeDir:    wt.Path,
		ProjectPath:    p.Path,
		PromptFile:     promptFile,
		SessionName:    wt.SessionName,
		PermissionMode: cfg.DefaultPermissionMode,
		Model:          cfg.DefaultModel,
		AgentsJSON:     agentsJSON,
		Resume:         project.HasClaudeHistory(wt.Path),
	})
	if err != nil {
		return errMsg{err}
	}
	if err := tmux.SwapInPane(sedgePane, newPane, cfg.SedgeWidthCols, cfg.SlotWidthPercent); err != nil {
		return errMsg{err}
	}
	return spawnedMsg{pane: wt.SessionName}
}

func deleteWorktreeCmd(p project.Project, wt project.Worktree) tea.Cmd {
	return func() tea.Msg {
		// Kill whichever tmux container is holding this worktree's claude:
		// the slot pane (if it's active) or the background window (if it's
		// alive in the background).
		sedgePane := os.Getenv("TMUX_PANE")
		if activePath, _ := tmux.ActiveSlotPath(sedgePane); activePath == wt.Path {
			_ = tmux.KillSlotPane(sedgePane)
		} else if winID, _, err := tmux.FindWorktreeWindow(wt.Path); err == nil && winID != "" {
			_ = tmux.KillWindow(winID)
		}
		if err := project.Recycle(p, wt); err != nil {
			return errMsg{err}
		}
		return deletedMsg{session: wt.SessionName}
	}
}

func addProjectCmd(pathInput string) tea.Cmd {
	return func() tea.Msg {
		if pathInput == "" {
			return errMsg{fmt.Errorf("path required")}
		}
		abs, err := filepath.Abs(expandUser(pathInput))
		if err != nil {
			return errMsg{err}
		}
		if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
			return errMsg{fmt.Errorf("%s does not look like a git repo (no .git)", abs)}
		}
		cfg, err := project.Load()
		if err != nil {
			return errMsg{err}
		}
		name := filepath.Base(abs)
		branch := project.DetectDefaultBranch(abs)
		if err := cfg.Add(project.Project{Name: name, Path: abs, DefaultBranch: branch}); err != nil {
			return errMsg{err}
		}
		if err := project.Save(cfg); err != nil {
			return errMsg{err}
		}
		return reloadedMsg{cfg: cfg}
	}
}

func cleanExitCmd(worktreesRoot string) tea.Cmd {
	return func() tea.Msg {
		_, _ = tmux.KillAllWorktreeWindows(expandUser(worktreesRoot))
		// Also kill the slot pane if any (the visible one).
		_ = tmux.KillSlotPane(os.Getenv("TMUX_PANE"))
		return nil
	}
}

func viewSubAgentCmd(wtPath, description string) tea.Cmd {
	return func() tea.Msg {
		path, err := agentlog.FindAgentFile(wtPath, description)
		if err != nil {
			return errMsg{err}
		}
		if path == "" {
			return errMsg{fmt.Errorf("no agent file found for %q yet (claude may not have flushed it)", description)}
		}
		if err := tmux.OpenSubAgentViewer(os.Getenv("TMUX_PANE"), path); err != nil {
			return errMsg{err}
		}
		return clearStat{}
	}
}

func openCodePaneCmd() tea.Cmd {
	return func() tea.Msg {
		if err := tmux.OpenCodePane(os.Getenv("TMUX_PANE")); err != nil {
			return errMsg{err}
		}
		return clearStat{}
	}
}

func editConfigCmd() tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}
	path, err := configPath()
	if err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return errMsg{err}
		}
		cfg, lerr := project.Load()
		if lerr != nil {
			return errMsg{lerr}
		}
		return reloadedMsg{cfg: cfg}
	})
}

func clearStatusAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return clearStat{} })
}

func dim(s string) string { return dimStyle.Render(s) }

// slugifySession normalizes user-supplied session names: lowercase, spaces ->
// dashes, drop anything outside [a-z0-9-_]. Caller falls back to a timestamp
// if the result is empty.
func slugifySession(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ', r == '/':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-_")
}

func expandUser(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// completePath returns the longest common prefix of all filesystem entries
// matching the user-typed path. The returned string preserves the user's
// "~/" prefix if present. Returns ok=false when no candidates match.
func completePath(input string) (string, bool) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", false
	}
	usedTilde := strings.HasPrefix(raw, "~/")
	expanded := expandUser(raw)

	dir := filepath.Dir(expanded)
	prefix := filepath.Base(expanded)
	// Special case: when input ends with "/", list inside that dir.
	if strings.HasSuffix(expanded, "/") {
		dir = strings.TrimSuffix(expanded, "/")
		prefix = ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	lcp := longestCommonPrefix(matches)
	if lcp == "" {
		return "", false
	}
	full := filepath.Join(dir, lcp)
	if info, err := os.Stat(full); err == nil && info.IsDir() && len(matches) == 1 {
		full += "/"
	}
	if usedTilde {
		if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(full, home) {
			return "~" + strings.TrimPrefix(full, home), true
		}
	}
	return full, true
}

func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		for !strings.HasPrefix(s, prefix) {
			if len(prefix) == 0 {
				return ""
			}
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}
