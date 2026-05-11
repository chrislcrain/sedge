package project

import (
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/chrislcrain/sedge/internal/xdg"
)

type Config struct {
	DefaultPermissionMode string    `toml:"default_permission_mode"`
	DefaultModel          string    `toml:"default_model,omitempty"`
	WorktreesRoot         string    `toml:"worktrees_root,omitempty"`
	Projects              []Project `toml:"projects,omitempty"`
}

func defaults() Config {
	return Config{
		DefaultPermissionMode: "auto",
		WorktreesRoot:         "~/.sedge/worktrees",
	}
}

func Load() (Config, error) {
	path, err := xdg.ConfigFile()
	if err != nil {
		return Config{}, err
	}
	cfg := defaults()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.DefaultPermissionMode == "" {
		cfg.DefaultPermissionMode = "auto"
	}
	if cfg.WorktreesRoot == "" {
		cfg.WorktreesRoot = "~/.sedge/worktrees"
	}
	return cfg, nil
}

func Save(cfg Config) error {
	if err := xdg.EnsureDirs(); err != nil {
		return err
	}
	path, err := xdg.ConfigFile()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(deriveDir(path), ".config.*.tmp")
	if err != nil {
		return err
	}
	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), path)
}

func deriveDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func (c *Config) FindByName(name string) (*Project, int) {
	for i := range c.Projects {
		if c.Projects[i].Name == name {
			return &c.Projects[i], i
		}
	}
	return nil, -1
}

func (c *Config) Add(p Project) error {
	if existing, _ := c.FindByName(p.Name); existing != nil {
		return fmt.Errorf("project %q already registered", p.Name)
	}
	c.Projects = append(c.Projects, p)
	return nil
}

func (c *Config) Remove(name string) error {
	_, idx := c.FindByName(name)
	if idx < 0 {
		return fmt.Errorf("project %q not found", name)
	}
	c.Projects = append(c.Projects[:idx], c.Projects[idx+1:]...)
	return nil
}
