package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Session identity: each Claude Code or Cursor session gets its own hub agent,
// named after the session id. Credentials live in ~/.agenthub/sessions/<id>.json.

// Server URL precedence: AH_SERVER env, ~/.agenthub/server, legacy config, then the default.
const defaultServer = "http://localhost:8080"

type sessionInfo struct {
	Tool    string
	ID      string
	Project string
}

// detectSession finds the current tool session, or returns false if it can't.
// Override with AH_TOOL and AH_SESSION_ID.
func detectSession() (sessionInfo, bool) {
	cwd, _ := os.Getwd()
	project := filepath.Base(cwd)

	if sid := os.Getenv("AH_SESSION_ID"); sid != "" {
		tool := os.Getenv("AH_TOOL")
		if tool == "" {
			tool = "cursor"
		}
		return sessionInfo{tool, sid, project}, true
	}
	if sid := os.Getenv("CLAUDE_CODE_SESSION_ID"); sid != "" && os.Getenv("AH_TOOL") != "cursor" {
		return sessionInfo{"claude", sid, project}, true
	}
	if sid, proj := cursorSession(cwd); sid != "" {
		return sessionInfo{"cursor", sid, proj}, true
	}
	return sessionInfo{}, false
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
