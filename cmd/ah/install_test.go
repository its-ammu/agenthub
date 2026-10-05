package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agenthub/internal/tools"
)

func TestWorkspaceSessionID(t *testing.T) {
	day := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	a := workspaceSessionID("codex", "/work/a", day)
	if a != workspaceSessionID("codex", "/work/a", day.Add(5*time.Hour)) {
		t.Error("id should be stable within a day")
	}
	if a == workspaceSessionID("codex", "/work/b", day) || a == workspaceSessionID("codex", "/work/a", day.Add(24*time.Hour)) {
		t.Error("id should differ by directory and by day")
	}
	if !strings.HasPrefix(a, "codex-") || !tools.IDRe.MatchString("codex") || len(a) < 6 {
		t.Errorf("unexpected id %q", a)
	}
}

func TestDetectSessionContract(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"AH_TOOL", "AH_SESSION_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(k, "")
	}

	if _, ok := detectSession(); ok {
		t.Fatal("no session should be detected with nothing set")
	}

	t.Setenv("CLAUDE_CODE_SESSION_ID", "abc-123-def")
	if s, ok := detectSession(); !ok || s.Tool != "claude" || s.ID != "abc-123-def" {
		t.Errorf("claude env detection = %+v, %v", s, ok)
	}

	// AH_TOOL narrows detection to that tool.
	t.Setenv("AH_TOOL", "codex")
	if s, ok := detectSession(); !ok || s.Tool != "codex" || !strings.HasPrefix(s.ID, "codex-") {
		t.Errorf("codex workspace detection = %+v, %v", s, ok)
	}

	// Explicit override wins for any tool name.
	t.Setenv("AH_TOOL", "mytool")
	t.Setenv("AH_SESSION_ID", "my-session-1")
	if s, ok := detectSession(); !ok || s.Tool != "mytool" || s.ID != "my-session-1" {
		t.Errorf("override = %+v, %v", s, ok)
	}
}

func TestInstallAndUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	list, _ := tools.Load()
	claude, _ := tools.Find(list, "claude")
	agents, _ := tools.Find(list, "agents")

	// Skill tool: writes its own file, removed along with its directory.
	path, err := installTool(claude, "/bin/ah", "http://hub", project)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".claude", "skills", "blackboard", "SKILL.md"); path != want {
		t.Errorf("skill path = %s, want %s", path, want)
	}
	if _, ok := uninstallTool(claude, project); !ok {
		t.Error("uninstall should report removal")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("skill directory should be gone")
	}

	// Snippet tool: preserves the user's own content in the shared file.
	target := filepath.Join(project, "AGENTS.md")
	os.WriteFile(target, []byte("# House rules\n\nuse tabs\n"), 0644)
	for i := 0; i < 2; i++ { // reinstall must not duplicate
		if _, err := installTool(agents, "/bin/ah", "http://hub", project); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(target)
	if !strings.Contains(string(data), "use tabs") || strings.Count(string(data), tools.SnippetBegin) != 1 {
		t.Fatalf("bad AGENTS.md after install:\n%s", data)
	}
	if _, ok := uninstallTool(agents, project); !ok {
		t.Error("uninstall should report removal")
	}
	data, _ = os.ReadFile(target)
	if strings.TrimSpace(string(data)) != "# House rules\n\nuse tabs" {
		t.Errorf("user content not restored: %q", data)
	}

	// Snippet-only file is deleted on uninstall.
	installTool(agents, "/bin/ah", "http://hub", project)
	os.Remove(target)
	installTool(agents, "/bin/ah", "http://hub", project)
	uninstallTool(agents, project)
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("file we created should be removed when empty")
	}
}
