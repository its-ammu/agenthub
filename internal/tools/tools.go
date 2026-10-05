// Package tools is the registry of coding agents AgentHub knows about: how to
// install the blackboard instructions for each, how to detect its session, and
// which usage reader applies. The built-in list is tools.json; users can add or
// override tools in ~/.agenthub/tools.json without touching code.
package tools

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

//go:embed tools.json
var builtinJSON []byte

//go:embed blackboard.md.tmpl
var instructionsTmpl string

// IDRe is the shape of a valid tool id. The hub accepts any id of this form.
var IDRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

type Install struct {
	Type string `json:"type"` // "skill" (own SKILL.md file) or "snippet" (block inside a shared file)
	Path string `json:"path"` // ~ expands to home; relative paths are relative to the project dir
	// Shared marks a skill file used by several tools (for example ~/.agents/skills,
	// which Codex and Gemini CLI both read). It cannot name one tool, so the agent
	// is told to set AH_TOOL to its own id.
	Shared bool `json:"shared,omitempty"`
}

type Session struct {
	Type string   `json:"type"`          // "env", "cursor-transcript" or "workspace"
	Env  []string `json:"env,omitempty"` // env vars holding the session id, for type "env"
}

type Tool struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Scope     string  `json:"scope"`                // "global" (user-level file) or "project" (file in the repo)
	DetectDir string  `json:"detect_dir,omitempty"` // exists => tool is installed on this machine
	Install   Install `json:"install"`
	Session   Session `json:"session"`
	Usage     string  `json:"usage,omitempty"` // usage reader: "claude", "cursor" or empty for none
	Note      string  `json:"note,omitempty"`  // shown after install
	// Legacy lists places earlier versions installed to. Installing or
	// uninstalling removes the AgentHub block from them (never other content).
	Legacy []Install `json:"legacy,omitempty"`
}

type file struct {
	Tools []Tool `json:"tools"`
}

// Load returns the built-in tools merged with the user's overrides. A user tool
// with an existing id replaces the built-in one.
func Load() ([]Tool, error) {
	var out file
	if err := json.Unmarshal(builtinJSON, &out); err != nil {
		return nil, fmt.Errorf("built-in tools.json: %w", err)
	}
	list := out.Tools
	home, _ := os.UserHomeDir()
	if data, err := os.ReadFile(filepath.Join(home, ".agenthub", "tools.json")); err == nil {
		var user file
		if err := json.Unmarshal(data, &user); err != nil {
			return nil, fmt.Errorf("~/.agenthub/tools.json: %w", err)
		}
		for _, u := range user.Tools {
			replaced := false
			for i := range list {
				if list[i].ID == u.ID {
					list[i], replaced = u, true
				}
			}
			if !replaced {
				list = append(list, u)
			}
		}
	}
	for i := range list {
		if err := list[i].validate(); err != nil {
			return nil, err
		}
	}
	return list, nil
}

func (t *Tool) validate() error {
	if !IDRe.MatchString(t.ID) {
		return fmt.Errorf("tool id %q must match %s", t.ID, IDRe)
	}
	if t.Name == "" {
		t.Name = t.ID
	}
	if t.Scope == "" {
		t.Scope = "global"
	}
	if t.Scope != "global" && t.Scope != "project" {
		return fmt.Errorf("tool %s: scope must be global or project", t.ID)
	}
	if t.Install.Type != "skill" && t.Install.Type != "snippet" {
		return fmt.Errorf("tool %s: install.type must be skill or snippet", t.ID)
	}
	if t.Install.Path == "" {
		return fmt.Errorf("tool %s: install.path is required", t.ID)
	}
	switch t.Session.Type {
	case "env":
		if len(t.Session.Env) == 0 {
			return fmt.Errorf("tool %s: session.env is required for type env", t.ID)
		}
	case "cursor-transcript", "workspace":
	case "":
		t.Session.Type = "workspace"
	default:
		return fmt.Errorf("tool %s: unknown session type %q", t.ID, t.Session.Type)
	}
	return nil
}

// Find returns the tool with the given id.
func Find(list []Tool, id string) (Tool, bool) {
	for _, t := range list {
		if t.ID == id {
			return t, true
		}
	}
	return Tool{}, false
}

// Detected reports whether the tool looks installed on this machine.
func (t Tool) Detected() bool {
	if t.DetectDir == "" {
		return false
	}
	st, err := os.Stat(ExpandHome(t.DetectDir))
	return err == nil && st.IsDir()
}

// LegacyTarget resolves one of the Legacy install paths.
func (i Install) LegacyTarget(projectDir string) string {
	p := ExpandHome(i.Path)
	if !filepath.IsAbs(p) {
		p = filepath.Join(projectDir, p)
	}
	return p
}

// Target is where the instructions for this tool are written. projectDir is
// used for project-scoped tools and relative paths.
func (t Tool) Target(projectDir string) string {
	p := ExpandHome(t.Install.Path)
	if !filepath.IsAbs(p) {
		p = filepath.Join(projectDir, p)
	}
	return p
}

func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// ahCmd is how the agent should invoke the CLI. Tools that cannot expose a
// session id get AH_TOOL baked in so `ah` knows which tool it is serving.
func (t Tool) ahCmd(ahBin string) string {
	if t.Install.Shared {
		return "AH_TOOL=<tool-id> " + ahBin
	}
	if t.Session.Type == "workspace" {
		return "AH_TOOL=" + t.ID + " " + ahBin
	}
	return ahBin
}

const (
	SnippetBegin = "<!-- >>> agenthub blackboard >>> -->"
	SnippetEnd   = "<!-- <<< agenthub blackboard <<< -->"
)

// Render produces the instructions for the tool: a full SKILL.md (with
// frontmatter) for skill tools, or a marker-wrapped block for snippet tools.
func (t Tool) Render(ahBin, server string) (string, error) {
	tmpl, err := template.New("blackboard").Parse(instructionsTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	toolName := t.Name
	if t.Install.Shared {
		toolName = "an AI coding agent that supports skills"
	}
	err = tmpl.Execute(&buf, map[string]any{
		"ToolName": toolName, "AHCmd": t.ahCmd(ahBin), "Server": server, "Shared": t.Install.Shared,
	})
	if err != nil {
		return "", err
	}
	text := buf.String()
	if t.Install.Type == "skill" {
		return text, nil
	}
	return SnippetBegin + "\n" + stripFrontmatter(text) + "\n" + SnippetEnd + "\n", nil
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	if i := strings.Index(s[4:], "\n---\n"); i >= 0 {
		return strings.TrimLeft(s[4+i+5:], "\n")
	}
	return s
}

// ReplaceSnippet inserts block into existing file contents, replacing a
// previous AgentHub block if there is one and leaving everything else alone.
func ReplaceSnippet(existing, block string) string {
	stripped := RemoveSnippet(existing)
	if strings.TrimSpace(stripped) == "" {
		return block
	}
	return strings.TrimRight(stripped, "\n") + "\n\n" + block
}

// RemoveSnippet deletes the AgentHub block from file contents.
func RemoveSnippet(s string) string {
	a := strings.Index(s, SnippetBegin)
	b := strings.Index(s, SnippetEnd)
	if a < 0 || b < a {
		return s
	}
	head := strings.TrimRight(s[:a], "\n")
	tail := strings.TrimLeft(s[b+len(SnippetEnd):], "\n")
	switch {
	case head == "":
		return tail
	case tail == "":
		return head + "\n"
	}
	return head + "\n\n" + tail
}

// HasSnippet reports whether the AgentHub block is present.
func HasSnippet(s string) bool {
	a := strings.Index(s, SnippetBegin)
	return a >= 0 && strings.Index(s, SnippetEnd) > a
}
