package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agenthub/internal/tools"
)

// Session identity: each tool session (Claude Code, Cursor, Codex, ...) gets its own hub agent,
// named after the session id. Credentials live in ~/.agenthub/sessions/<id>.json.

// Server URL precedence: AH_SERVER env, ~/.agenthub/server, legacy config, then the default.
const defaultServer = "http://localhost:8080"

type sessionInfo struct {
	Tool    string
	ID      string
	Project string
}

// detectSession finds the current tool session, or returns false if it can't.
//
// Contract for any tool: set AH_SESSION_ID (and AH_TOOL, a short lowercase id
// like "mytool") and `ah` registers that session as its own agent. Without
// AH_SESSION_ID, each tool in the registry (internal/tools/tools.json) says how
// to find its session: an env var, Cursor's transcript directory, or, for tools
// that expose nothing, one session per workspace per day. If AH_TOOL is set,
// only that tool's detector is tried.
func detectSession() (sessionInfo, bool) {
	cwd, _ := os.Getwd()
	project := filepath.Base(cwd)
	want := os.Getenv("AH_TOOL")

	if sid := os.Getenv("AH_SESSION_ID"); sid != "" {
		if want == "" {
			want = "agent"
		}
		return sessionInfo{want, sid, project}, true
	}

	list, err := tools.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	if want != "" {
		if t, ok := tools.Find(list, want); ok {
			return detectTool(t, cwd, project)
		}
		// A tool the registry does not know (an agent told to name itself): one
		// session per workspace per day, like any tool with no session id.
		if tools.IDRe.MatchString(want) {
			return sessionInfo{want, workspaceSessionID(want, cwd, time.Now()), project}, true
		}
		return sessionInfo{}, false
	}
	for _, t := range list {
		if t.Session.Type == "workspace" {
			continue // only used when the tool is named via AH_TOOL
		}
		if s, ok := detectTool(t, cwd, project); ok {
			return s, true
		}
	}
	return sessionInfo{}, false
}

func detectTool(t tools.Tool, cwd, project string) (sessionInfo, bool) {
	switch t.Session.Type {
	case "env":
		for _, name := range t.Session.Env {
			if sid := os.Getenv(name); sid != "" {
				return sessionInfo{t.ID, sid, project}, true
			}
		}
	case "cursor-transcript":
		if sid, proj := cursorSession(cwd); sid != "" {
			return sessionInfo{t.ID, sid, proj}, true
		}
	case "workspace":
		return sessionInfo{t.ID, workspaceSessionID(t.ID, cwd, time.Now()), project}, true
	}
	return sessionInfo{}, false
}

// workspaceSessionID is a stable id for tools that expose no session id: one
// per tool, directory and day.
func workspaceSessionID(tool, dir string, now time.Time) string {
	sum := sha1.Sum([]byte(dir))
	return fmt.Sprintf("%s-%s-%s", tool, hex.EncodeToString(sum[:])[:8], now.Format("20060102"))
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// cursorSession finds the newest Cursor agent transcript for the workspace
// containing dir. Cursor writes the active chat's transcript continuously, so
// the most recently modified one is the current session.
func cursorSession(dir string) (sid, project string) {
	home, _ := os.UserHomeDir()
	for d := dir; ; d = filepath.Dir(d) {
		key := nonAlnum.ReplaceAllString(strings.TrimPrefix(d, "/"), "-")
		files, _ := filepath.Glob(filepath.Join(home, ".cursor", "projects", key, "agent-transcripts", "*", "*.jsonl"))
		var newest time.Time
		for _, f := range files {
			if st, err := os.Stat(f); err == nil && st.ModTime().After(newest) {
				newest = st.ModTime()
				sid = filepath.Base(filepath.Dir(f))
				project = filepath.Base(d)
			}
		}
		if sid != "" {
			return sid, project
		}
		if d == "/" || d == "." || filepath.Dir(d) == d {
			return "", ""
		}
	}
}

func serverURL() string {
	if s := os.Getenv("AH_SERVER"); s != "" {
		return strings.TrimRight(s, "/")
	}
	if data, err := os.ReadFile(filepath.Join(configDir(), "server")); err == nil {
		if u := strings.TrimSpace(string(data)); u != "" {
			return strings.TrimRight(u, "/")
		}
	}
	if data, err := os.ReadFile(configPath()); err == nil {
		var cfg CLIConfig
		if json.Unmarshal(data, &cfg) == nil && cfg.ServerURL != "" {
			return strings.TrimRight(cfg.ServerURL, "/")
		}
	}
	return defaultServer
}

func sessionConfigPath(id string) string {
	return filepath.Join(configDir(), "sessions", id+".json")
}

// sessionConfig loads the credentials for the current session, registering a
// new generated-name agent on first use. Returns nil if no session is detected.
func sessionConfig() *CLIConfig {
	s, ok := detectSession()
	if !ok {
		return nil
	}
	if data, err := os.ReadFile(sessionConfigPath(s.ID)); err == nil {
		var cfg CLIConfig
		if json.Unmarshal(data, &cfg) == nil && cfg.APIKey != "" {
			return &cfg
		}
	}

	client := &Client{BaseURL: serverURL(), HTTP: newClient(&CLIConfig{ServerURL: serverURL()}).HTTP}
	resp, err := client.postJSON("/api/sessions", map[string]string{
		"tool": s.Tool, "session_id": s.ID, "project": s.Project,
	})
	if err != nil {
		fatal("could not reach the hub at %s: %v", client.BaseURL, err)
	}
	var out map[string]string
	if err := readJSON(resp, &out); err != nil {
		fatal("could not register this %s session: %v", s.Tool, err)
	}
	cfg := &CLIConfig{ServerURL: client.BaseURL, APIKey: out["api_key"], AgentID: out["id"]}
	os.MkdirAll(filepath.Dir(sessionConfigPath(s.ID)), 0700)
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(sessionConfigPath(s.ID), data, 0600); err != nil {
		fatal("could not save session config: %v", err)
	}
	fmt.Fprintf(os.Stderr, "registered this %s session as %q\n", s.Tool, cfg.AgentID)
	return cfg
}

func cmdWhoami(args []string) {
	cfg := mustLoadConfig()
	fmt.Printf("agent:   %s\nserver:  %s\n", cfg.AgentID, cfg.ServerURL)
	if s, ok := detectSession(); ok {
		fmt.Printf("tool:    %s\nsession: %s\nproject: %s\n", s.Tool, s.ID, s.Project)
	} else {
		fmt.Println("session: (none detected, using legacy config)")
	}
}
