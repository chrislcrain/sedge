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

type Model struct {
	cfg      project.Config
	cursor   int
	err      error
	status   string
	w, h     int
	tmuxName string
}

func New() (Model, error) {
	cfg, err := project.Load()
	if err != nil {
		return Model{}, err
	}
	name, _ := tmux.CurrentSession()
	return Model{cfg: cfg, tmuxName: name}, nil
}

type (
	reloadedMsg  project.Config
	spawnedMsg   struct{ pane string }
	errMsg       struct{ err error }
	statusMsg    string
	clearStatus  struct{}
)

func (m Model) Init() tea.Cmd { return nil }

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
			if m.cursor < len(m.cfg.Projects)-1 {
				m.cursor++
			}
		case actUp:
			if m.cursor > 0 {
				m.cursor--
			}
		case actEnter:
			if len(m.cfg.Projects) == 0 {
				m.status = "no projects registered — press 'a' to add one"
				return m, clearStatusAfter(2 * time.Second)
			}
			if !tmux.InsideTmux() {
				m.err = fmt.Errorf("not inside tmux; restart sedge from a non-tmux shell")
				return m, nil
			}
			p := m.cfg.Projects[m.cursor]
			return m, spawnCmd(p, m.cfg)
		case actAdd:
			return m, editConfigCmd()
		case actEdit:
			return m, editConfigCmd()
		case actReload:
			return m, reloadCmd()
		}

	case reloadedMsg:
		m.cfg = project.Config(msg)
		if m.cursor >= len(m.cfg.Projects) {
			m.cursor = len(m.cfg.Projects) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		m.status = "reloaded"
		return m, clearStatusAfter(2 * time.Second)

	case spawnedMsg:
		m.status = "spawned " + msg.pane
		return m, clearStatusAfter(3 * time.Second)

	case errMsg:
		m.err = msg.err
		return m, nil

	case statusMsg:
		m.status = string(msg)
		return m, clearStatusAfter(2 * time.Second)

	case clearStatus:
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
		for i, p := range m.cfg.Projects {
			line := fmt.Sprintf("%s  %s", p.Name, dim(p.Path))
			if i == m.cursor {
				b.WriteString(selectedItemStyle.Render("▸ " + line))
			} else {
				b.WriteString(itemStyle.Render("  " + line))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("j/k  ↑↓ · enter spawn · a/e edit config · r reload · q quit"))

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errStyle.Render("error: " + m.err.Error()))
	} else if m.status != "" {
		b.WriteString("\n")
		b.WriteString(helpStyle.Render(m.status))
	}
	return b.String()
}

func dim(s string) string {
	return helpStyle.Render(s)
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

		pane, err := tmux.SpawnClaudePane(tmux.SpawnClaudeOpts{
			WorktreeDir:    wt,
			ProjectPath:    p.Path,
			PromptFile:     promptFile,
			SessionName:    sessionName,
			PermissionMode: cfg.DefaultPermissionMode,
			Model:          cfg.DefaultModel,
		})
		if err != nil {
			return errMsg{err}
		}
		return spawnedMsg{pane: pane}
	}
}

func reloadCmd() tea.Cmd {
	return func() tea.Msg {
		cfg, err := project.Load()
		if err != nil {
			return errMsg{err}
		}
		return reloadedMsg(cfg)
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
		return reloadedMsg(cfg)
	})
}

func clearStatusAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return clearStatus{} })
}
