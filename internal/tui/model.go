package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/chrislcrain/sedge/internal/instructions"
	"github.com/chrislcrain/sedge/internal/project"
	"github.com/chrislcrain/sedge/internal/tmux"
)

// rowKind tags what a rendered row represents.
type rowKind int

const (
	rowProject rowKind = iota
	rowWorktree
	rowNewSession
)

type row struct {
	kind    rowKind
	project *project.Project
	wt      *project.Worktree
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
}

func New() (Model, error) {
	cfg, err := project.Load()
	if err != nil {
		return Model{}, err
	}
	m := Model{
		cfg:       cfg,
		worktrees: map[string][]project.Worktree{},
		expanded:  map[string]bool{},
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
	errMsg     struct{ err error }
	clearStat  struct{}
)

func (m Model) Init() tea.Cmd {
	return loadAllWorktreesCmd(m.cfg)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
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
		case actAdd, actEdit:
			return m, editConfigCmd()
		case actReload:
			return m, tea.Batch(reloadCfgCmd(), loadAllWorktreesCmd(m.cfg))
		}

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
		m.status = "focused " + msg.pane
		return m, clearStatusAfter(2 * time.Second)

	case errMsg:
		m.err = msg.err
		return m, nil

	case clearStat:
		m.status = ""
		m.err = nil
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("sedge"))
	b.WriteString("\n")

	if len(m.cfg.Projects) == 0 {
		b.WriteString(emptyStyle.Render("no projects registered"))
		b.WriteString("\n")
		b.WriteString(emptyStyle.Render("press 'a' to add"))
		b.WriteString("\n")
	} else {
		for i, r := range m.rows {
			// Emit a divider before this row when the row introduces a new
			// logical group. Project rows always start a new group; worktree
			// rows under an expanded project get a thinner divider.
			if i > 0 {
				if r.kind == rowProject {
					b.WriteString(dividerStyle.Render(strings.Repeat("─", dividerWidth(m.w))))
					b.WriteString("\n")
				} else if r.kind == rowWorktree && m.rows[i-1].kind == rowWorktree {
					b.WriteString(dividerStyle.Render("  " + strings.Repeat("·", dividerWidth(m.w)-2)))
					b.WriteString("\n")
				}
			}
			line := m.renderRow(r)
			if i == m.cursor {
				b.WriteString(selectedItemStyle.Render("› " + line))
			} else {
				b.WriteString(itemStyle.Render("  " + line))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("j/k  ↑↓ · enter expand/spawn/focus · a/e edit config · r reload · q quit"))

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
		dot := idleStyle.Render("○")
		if r.wt.Busy {
			dot = busyStyle.Render("●")
		}
		return fmt.Sprintf("  %s %s", dot, r.wt.SessionName)
	case rowNewSession:
		return newSessionStyle.Render("  + new session")
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

func (m *Model) activateRow() tea.Cmd {
	if len(m.rows) == 0 {
		return nil
	}
	r := m.rows[m.cursor]
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
		return spawnCmd(*r.project, m.cfg)
	case rowWorktree:
		if !tmux.InsideTmux() {
			m.err = fmt.Errorf("not inside tmux; restart sedge from a non-tmux shell")
			return nil
		}
		return focusOrRespawnCmd(*r.project, *r.wt, m.cfg)
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
		live, _ := tmux.LivePaths()
		for i := range list {
			if live[list[i].Path] {
				list[i].Busy = true
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

func spawnCmd(p project.Project, cfg project.Config) tea.Cmd {
	return func() tea.Msg {
		sessionName := fmt.Sprintf("s%d", time.Now().Unix())

		defaultBranch := p.ResolvedDefaultBranch()
		if defaultBranch == "" {
			defaultBranch = project.DetectDefaultBranch(p.Path)
		}

		wt := project.WorktreePath(cfg.WorktreesRoot, p, sessionName)
		if err := project.EnsureWorktree(p.Path, defaultBranch, wt, sessionName); err != nil {
			return errMsg{err}
		}

		promptFile, err := instructions.Resolve(p.Path, p.Name, sessionName)
		if err != nil {
			return errMsg{err}
		}
		agentsJSON, _ := instructions.LoadAgentsJSON()

		pane, err := tmux.SpawnClaudePane(tmux.SpawnClaudeOpts{
			WorktreeDir:    wt,
			ProjectPath:    p.Path,
			PromptFile:     promptFile,
			SessionName:    sessionName,
			PermissionMode: cfg.DefaultPermissionMode,
			Model:          cfg.DefaultModel,
			AgentsJSON:     agentsJSON,
		})
		if err != nil {
			return errMsg{err}
		}
		return spawnedMsg{pane: pane}
	}
}

func focusOrRespawnCmd(p project.Project, wt project.Worktree, cfg project.Config) tea.Cmd {
	return func() tea.Msg {
		ref, err := tmux.FindPaneByCwd(wt.Path)
		if err != nil {
			return errMsg{err}
		}
		if ref != nil {
			if err := tmux.FocusPane(*ref); err != nil {
				return errMsg{err}
			}
			return focusedMsg{pane: ref.PaneID}
		}
		promptFile, err := instructions.Resolve(p.Path, p.Name, wt.SessionName)
		if err != nil {
			return errMsg{err}
		}
		agentsJSON, _ := instructions.LoadAgentsJSON()
		pane, err := tmux.SpawnClaudePane(tmux.SpawnClaudeOpts{
			WorktreeDir:    wt.Path,
			ProjectPath:    p.Path,
			PromptFile:     promptFile,
			SessionName:    wt.SessionName,
			PermissionMode: cfg.DefaultPermissionMode,
			Model:          cfg.DefaultModel,
			AgentsJSON:     agentsJSON,
		})
		if err != nil {
			return errMsg{err}
		}
		return spawnedMsg{pane: pane}
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
