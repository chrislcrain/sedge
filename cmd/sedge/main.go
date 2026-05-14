package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/chrislcrain/sedge/internal/agentlog"
	"github.com/chrislcrain/sedge/internal/instructions"
	"github.com/chrislcrain/sedge/internal/project"
	"github.com/chrislcrain/sedge/internal/tmux"
	"github.com/chrislcrain/sedge/internal/tui"
	"github.com/chrislcrain/sedge/internal/xdg"
	"github.com/spf13/cobra"
)

var (
	version    = "dev"
	insideTmux bool
)

func main() {
	root := &cobra.Command{
		Use:           "sedge",
		Short:         "TUI harness for Claude Code",
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runRoot,
	}
	root.Flags().BoolVar(&insideTmux, "inside-tmux", false, "internal: skip tmux detection and run TUI directly")
	_ = root.Flags().MarkHidden("inside-tmux")

	root.AddCommand(
		cmdInit(),
		cmdAdd(),
		cmdLs(),
		cmdRm(),
		cmdEdit(),
		cmdClean(),
		cmdPrune(),
		cmdWatchAgent(),
		cmdHook(),
		cmdInstallHooks(),
		cmdVersion(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runRoot(cmd *cobra.Command, args []string) error {
	if err := ensureInit(); err != nil {
		return err
	}

	// The --inside-tmux flag is set by sedge re-execing itself in a fresh
	// tmux window/session. At that point we know we are in our own window
	// with a clean slot, so we run the TUI directly.
	if insideTmux {
		m, err := tui.New()
		if err != nil {
			return err
		}
		_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
		return err
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}

	// Warm start: already inside a tmux session but not in our dedicated
	// window. Create a new window for sedge so the slot pane is unambiguous.
	if tmux.InsideTmux() {
		return tmux.SpawnWindowAndSwitch(self)
	}

	// Cold start: no tmux at all. Spawn a new session.
	return tmux.SpawnAndAttach(tmux.DefaultSessionName, self)
}

func ensureInit() error {
	if err := xdg.EnsureDirs(); err != nil {
		return err
	}
	if err := instructions.WriteDefaultGlobalIfMissing(); err != nil {
		return err
	}
	if err := instructions.WriteDefaultAgentsIfMissing(); err != nil {
		return err
	}
	if err := tui.WriteDefaultNameplateIfMissing(); err != nil {
		return err
	}
	cfgPath, err := xdg.ConfigFile()
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return project.Save(project.Config{
			DefaultPermissionMode: "auto",
			WorktreesRoot:         xdg.DefaultWorktreesRoot(),
		})
	}
	return nil
}

func cmdInit() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize the sedge home directory with defaults (idempotent)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := ensureInit(); err != nil {
				return err
			}
			root, _ := xdg.Root()
			fmt.Printf("initialized %s\n", root)
			return nil
		},
	}
}

func cmdAdd() *cobra.Command {
	var name, branch string
	cmd := &cobra.Command{
		Use:   "add [path]",
		Short: "Register a project at path (default: cwd)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := ensureInit(); err != nil {
				return err
			}
			path, err := os.Getwd()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				path = args[0]
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
				return fmt.Errorf("%s does not look like a git repo (no .git)", abs)
			}
			if name == "" {
				name = filepath.Base(abs)
			}
			if branch == "" {
				branch = project.DetectDefaultBranch(abs)
			}
			cfg, err := project.Load()
			if err != nil {
				return err
			}
			if err := cfg.Add(project.Project{Name: name, Path: abs, DefaultBranch: branch}); err != nil {
				return err
			}
			if err := project.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("registered %q at %s (branch: %s)\n", name, abs, branch)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name (default: basename of path)")
	cmd.Flags().StringVar(&branch, "branch", "", "default branch (default: auto-detect)")
	return cmd
}

func cmdLs() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List registered projects",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := project.Load()
			if err != nil {
				return err
			}
			if len(cfg.Projects) == 0 {
				fmt.Println("(none)")
				return nil
			}
			for _, p := range cfg.Projects {
				fmt.Printf("%-20s  %s  [%s]\n", p.Name, p.Path, p.ResolvedDefaultBranch())
			}
			return nil
		},
	}
}

func cmdRm() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Unregister a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := project.Load()
			if err != nil {
				return err
			}
			if err := cfg.Remove(args[0]); err != nil {
				return err
			}
			return project.Save(cfg)
		},
	}
}

func cmdEdit() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open config.toml in $EDITOR",
		RunE: func(_ *cobra.Command, _ []string) error {
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vim"
			}
			path, err := xdg.ConfigFile()
			if err != nil {
				return err
			}
			c := exec.Command(editor, path)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
	}
}

func cmdClean() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "clean [name]",
		Short: "Prune sedge worktrees that have no live tmux pane",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			root, err := xdg.WorktreesRoot()
			if err != nil {
				return err
			}
			cfg, err := project.Load()
			if err != nil {
				return err
			}
			projects := cfg.Projects
			if len(args) == 1 {
				p, _ := cfg.FindByName(args[0])
				if p == nil {
					return fmt.Errorf("project %q not found", args[0])
				}
				projects = []project.Project{*p}
			}
			live := liveSessionNames()
			removed := 0
			for _, p := range projects {
				dir := filepath.Join(root, p.Name)
				entries, err := os.ReadDir(dir)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return err
				}
				for _, e := range entries {
					if !e.IsDir() {
						continue
					}
					if !all && live[e.Name()] {
						continue
					}
					wtPath := filepath.Join(dir, e.Name())
					if err := exec.Command("git", "-C", p.Path, "worktree", "remove", "--force", wtPath).Run(); err != nil {
						fmt.Fprintf(os.Stderr, "git worktree remove %s: %v\n", wtPath, err)
						continue
					}
					removed++
				}
			}
			fmt.Printf("removed %d worktree(s)\n", removed)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "remove ALL sedge worktrees, even live ones")
	return cmd
}

func cmdPrune() *cobra.Command {
	return &cobra.Command{
		Use:   "prune [name]",
		Short: "Run `git worktree prune` across registered projects to drop orphaned refs",
		Long: "Removes stale `.git/worktrees/<name>` metadata for worktree paths that " +
			"no longer exist on disk (e.g. after a volume wipe). Operates on every " +
			"registered project unless a name is given.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := project.Load()
			if err != nil {
				return err
			}
			projects := cfg.Projects
			if len(args) == 1 {
				p, _ := cfg.FindByName(args[0])
				if p == nil {
					return fmt.Errorf("project %q not found", args[0])
				}
				projects = []project.Project{*p}
			}
			for _, p := range projects {
				out, err := exec.Command("git", "-C", p.Path, "worktree", "prune", "-v").CombinedOutput()
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s: git worktree prune: %v\n%s", p.Name, err, out)
					continue
				}
				trimmed := strings.TrimSpace(string(out))
				if trimmed == "" {
					fmt.Printf("%s: nothing to prune\n", p.Name)
				} else {
					fmt.Printf("%s:\n%s\n", p.Name, indent(trimmed, "  "))
				}
			}
			return nil
		},
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// liveSessionNames returns the set of session names ("s1234...") that match
// the `-n` display name of any live claude pane across tmux sessions.
func liveSessionNames() map[string]bool {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_title}").Output()
	if err != nil {
		return nil
	}
	names := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names[line] = true
		}
	}
	return names
}

func cmdWatchAgent() *cobra.Command {
	return &cobra.Command{
		Use:   "watch-agent <jsonl-path>",
		Short: "Tail and pretty-print a sub-agent session log",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return agentlog.Watch(args[0])
		},
	}
}

func cmdVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print sedge version",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println("sedge", version)
		},
	}
}
