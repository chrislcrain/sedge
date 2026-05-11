package agentlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Watch tails the given sub-agent JSONL file forever, pretty-printing each
// new message to stdout with ANSI styling. Used by `sedge watch-agent`.
func Watch(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Printf("\x1b[1;35m─── sub-agent log ───\x1b[0m  \x1b[2m%s\x1b[0m\n\n", path)

	var pending []byte
	chunk := make([]byte, 8192)
	for {
		n, rerr := f.Read(chunk)
		if n > 0 {
			pending = append(pending, chunk[:n]...)
			for {
				idx := bytes.IndexByte(pending, '\n')
				if idx < 0 {
					break
				}
				line := pending[:idx]
				pending = pending[idx+1:]
				if len(line) > 0 {
					printRecord(line)
				}
			}
		}
		if rerr == io.EOF || rerr == nil && n == 0 {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		if rerr != nil {
			return rerr
		}
	}
}

type watchRecord struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

type watchMessage struct {
	Role    string         `json:"role"`
	Content []watchContent `json:"content"`
}

type watchContent struct {
	Type    string                 `json:"type"`
	Name    string                 `json:"name,omitempty"`
	Text    string                 `json:"text,omitempty"`
	Input   map[string]interface{} `json:"input,omitempty"`
	Content json.RawMessage        `json:"content,omitempty"` // for tool_result
}

func printRecord(line []byte) {
	var r watchRecord
	if err := json.Unmarshal(line, &r); err != nil {
		return
	}
	// message can be either an object (assistant/user) or a plain string
	// (rare). Try object first.
	var m watchMessage
	if err := json.Unmarshal(r.Message, &m); err != nil {
		return
	}
	for _, c := range m.Content {
		switch c.Type {
		case "text":
			tag := m.Role
			if tag == "" {
				tag = r.Type
			}
			color := "1;36" // assistant cyan
			if tag == "user" {
				color = "1;32" // user green
			}
			fmt.Printf("\x1b[%sm%s\x1b[0m\n%s\n\n", color, tag, c.Text)
		case "tool_use":
			fmt.Printf("\x1b[1;33m→ %s\x1b[0m %s\n", c.Name, summarize(c.Input))
		case "tool_result":
			text := flattenToolResult(c.Content)
			if text == "" {
				text = "(complete)"
			}
			fmt.Printf("\x1b[2m← result:\x1b[0m %s\n\n", truncate(text, 400))
		}
	}
}

func summarize(input map[string]interface{}) string {
	if input == nil {
		return ""
	}
	for _, k := range []string{"description", "command", "file_path", "pattern", "subagent_type", "url"} {
		if v, ok := input[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return truncate(s, 120)
			}
		}
	}
	b, _ := json.Marshal(input)
	return truncate(string(b), 120)
}

func flattenToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Else array of {type,text}.
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &items); err == nil {
		var b strings.Builder
		for _, it := range items {
			if it.Text != "" {
				b.WriteString(it.Text)
			}
		}
		return b.String()
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
