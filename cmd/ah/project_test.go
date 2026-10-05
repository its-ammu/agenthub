package main

import (
	"strings"
	"testing"
)

func TestChannelSlug(t *testing.T) {
	cases := map[string]string{
		"AgentHub":              "agenthub",
		"hai_assistants":        "hai_assistants",
		"My Cool Repo!":         "my-cool-repo",
		"---":                   "project",
		"":                      "project",
		strings.Repeat("a", 50): strings.Repeat("a", 31),
		"feature/login.page":    "feature-login-page",
	}
	for in, want := range cases {
		if got := channelSlug(in); got != want {
			t.Errorf("channelSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsTrivialCommit(t *testing.T) {
	for _, s := range []string{"WIP", "wip: scratch", "fixup! add thing", "Merge branch 'main'", "fix typo in readme", "formatting"} {
		if !isTrivialCommit(s) {
			t.Errorf("%q should be trivial", s)
		}
	}
	for _, s := range []string{"feat: add sharepoint folder linking", "Fix race in session registration", "Add light mode"} {
		if isTrivialCommit(s) {
			t.Errorf("%q should not be trivial", s)
		}
	}
}

func TestStripHookKeepsOtherContent(t *testing.T) {
	orig := "#!/bin/sh\necho existing\n"
	withBlock := orig + "\n" + hookBlock("/bin/ah")
	if !strings.Contains(withBlock, hookBegin) {
		t.Fatal("block not added")
	}
	got := stripHook(withBlock)
	if strings.Contains(got, "agenthub") || !strings.Contains(got, "echo existing") {
		t.Fatalf("strip removed the wrong thing: %q", got)
	}
	// installing twice must not duplicate the block
	twice := stripHook(withBlock) + "\n" + hookBlock("/bin/ah")
	if strings.Count(twice, hookBegin) != 1 {
		t.Fatal("block duplicated")
	}
}
