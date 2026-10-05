package tools

import (
	"strings"
	"testing"
)

func TestBuiltinToolsAreValid(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no user overrides
	list, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tl := range list {
		if seen[tl.ID] {
			t.Errorf("duplicate tool id %s", tl.ID)
		}
		seen[tl.ID] = true
	}
	for _, id := range []string{"claude", "cursor", "codex", "gemini", "windsurf", "copilot", "aider", "agents"} {
		if !seen[id] {
			t.Errorf("missing built-in tool %s", id)
		}
	}
}

func TestRenderSkillAndSnippet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	list, _ := Load()
	claude, _ := Find(list, "claude")
	codex, _ := Find(list, "codex")

	skill, err := claude.Render("/bin/ah", "http://hub:1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(skill, "---\nname: blackboard") {
		t.Error("skill should start with frontmatter")
	}
	if !strings.Contains(skill, "running in Claude Code") || !strings.Contains(skill, "`/bin/ah <cmd>`") || !strings.Contains(skill, "http://hub:1") {
		t.Errorf("skill not rendered for claude:\n%s", skill)
	}
	for _, want := range []string{"renders posts as Markdown", "channel is archived", "ah channel unarchive"} {
		if !strings.Contains(skill, want) {
			t.Errorf("skill missing guidance %q", want)
		}
	}
	if strings.Contains(skill, "{{") || strings.Contains(skill, "@@") {
		t.Error("unrendered placeholder left in skill")
	}

	snip, err := codex.Render("/bin/ah", "http://hub:1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(snip, SnippetBegin) || !strings.HasSuffix(strings.TrimSpace(snip), SnippetEnd) {
		t.Error("snippet should be wrapped in markers")
	}
	if strings.Contains(snip, "name: blackboard") {
		t.Error("snippet should not carry skill frontmatter")
	}
	// Tools with no session id get AH_TOOL baked into the command.
	if !strings.Contains(snip, "`AH_TOOL=codex /bin/ah <cmd>`") {
		t.Error("workspace tool should call ah with AH_TOOL set")
	}
}

func TestSnippetReplaceAndRemove(t *testing.T) {
	block := SnippetBegin + "\nv1\n" + SnippetEnd + "\n"
	got := ReplaceSnippet("# my rules\n\nbe nice\n", block)
	if !strings.HasPrefix(got, "# my rules") || !HasSnippet(got) {
		t.Fatalf("existing content lost or block missing:\n%s", got)
	}
	// Reinstalling replaces rather than duplicates.
	again := ReplaceSnippet(got, strings.Replace(block, "v1", "v2", 1))
	if strings.Count(again, SnippetBegin) != 1 || !strings.Contains(again, "v2") || strings.Contains(again, "v1") {
		t.Fatalf("block not replaced:\n%s", again)
	}
	if rest := RemoveSnippet(again); strings.TrimSpace(rest) != "# my rules\n\nbe nice" || HasSnippet(rest) {
		t.Fatalf("remove left %q", rest)
	}
	if RemoveSnippet(block) != "" {
		t.Error("removing the only block should leave nothing")
	}
}

func TestUserToolsOverrideAndExtend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, home+"/.agenthub/tools.json", `{"tools":[
	  {"id":"codex","name":"Codex 2","install":{"type":"snippet","path":"~/x.md"},"session":{"type":"env","env":["CODEX_SESSION"]}},
	  {"id":"mytool","install":{"type":"snippet","path":"RULES.md"}}]}`)
	list, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	codex, _ := Find(list, "codex")
	if codex.Name != "Codex 2" || codex.Session.Type != "env" {
		t.Errorf("codex not overridden: %+v", codex)
	}
	my, ok := Find(list, "mytool")
	if !ok || my.Name != "mytool" || my.Scope != "global" || my.Session.Type != "workspace" {
		t.Errorf("custom tool wrong or missing: %+v", my)
	}
}

func TestInvalidUserToolRejected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, home+"/.agenthub/tools.json", `{"tools":[{"id":"Bad Id","install":{"type":"snippet","path":"x"}}]}`)
	if _, err := Load(); err == nil {
		t.Fatal("expected an error for an invalid tool id")
	}
}
