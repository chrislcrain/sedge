package instructions

import (
	_ "embed"
	"os"

	"github.com/chrislcrain/sedge/internal/xdg"
)

//go:embed default_agents.json
var defaultAgentsJSON string

// LoadAgentsJSON returns the contents of ~/.sedge/agents.json if present,
// otherwise the embedded default. The string is the JSON literal passed to
// `claude --agents`. Whitespace is preserved as-is.
func LoadAgentsJSON() (string, error) {
	root, err := xdg.Root()
	if err != nil {
		return defaultAgentsJSON, err
	}
	data, err := os.ReadFile(root + "/agents.json")
	if os.IsNotExist(err) {
		return defaultAgentsJSON, nil
	}
	if err != nil {
		return defaultAgentsJSON, err
	}
	return string(data), nil
}

// WriteDefaultAgentsIfMissing seeds ~/.sedge/agents.json with the embedded
// default if no file exists yet. Idempotent.
func WriteDefaultAgentsIfMissing() error {
	root, err := xdg.Root()
	if err != nil {
		return err
	}
	path := root + "/agents.json"
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := xdg.EnsureDirs(); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultAgentsJSON), 0o644)
}
