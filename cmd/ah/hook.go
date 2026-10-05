package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Auto-share: a git post-commit hook that runs `ah commit --auto`. It is
// appended between markers so an existing post-commit hook is preserved.

const (
	hookBegin = "# >>> agenthub >>>"
	hookEnd   = "# <<< agenthub <<<"
)

func hookPath() string {
	out, err := gitOutput("rev-parse", "--git-path", "hooks/post-commit")
	if err != nil {
		fatal("not inside a git repository")
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		if top, err := gitOutput("rev-parse", "--show-toplevel"); err == nil {
			p = filepath.Join(strings.TrimSpace(top), p)
		}
	}
	return p
}

func hookBlock(ahBin string) string {
	return fmt.Sprintf("%s\n# Shares notable commits made in agent sessions. Skip with AH_NO_HOOK=1.\n[ -n \"$AH_NO_HOOK\" ] || \"%s\" commit --auto >/dev/null 2>&1 &\n%s\n", hookBegin, ahBin, hookEnd)
}

// stripHook removes the agenthub block from hook file contents.
func stripHook(s string) string {
	a := strings.Index(s, hookBegin)
	b := strings.Index(s, hookEnd)
	if a < 0 || b < a {
		return s
	}
	return strings.TrimRight(s[:a], "\n") + strings.TrimLeft(s[b+len(hookEnd):], "\n")
}

func cmdHook(args []string) {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	path := hookPath()
	existing, _ := os.ReadFile(path)
	installed := strings.Contains(string(existing), hookBegin)

	switch sub {
	case "status":
		if installed {
			fmt.Printf("installed: %s\n", path)
		} else {
			fmt.Println("not installed (run `ah hook install`)")
		}
	case "install":
		if projectChannel() == "" {
			ch, _ := projectInit(args[1:])
			fmt.Printf("project channel: #%s\n", ch)
		}
		ahBin, err := os.Executable()
		if err != nil {
			fatal("cannot locate the ah binary: %v", err)
		}
		if resolved, err := filepath.EvalSymlinks(ahBin); err == nil {
			ahBin = resolved
		}
		content := stripHook(string(existing))
		if strings.TrimSpace(content) == "" {
			content = "#!/bin/sh\n"
		}
		content = strings.TrimRight(content, "\n") + "\n\n" + hookBlock(ahBin)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fatal("create hooks dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			fatal("write hook: %v", err)
		}
		fmt.Printf("installed post-commit hook: %s\n", path)
		fmt.Println("Notable commits made from an agent session are now shared to the project channel.")
	case "uninstall":
		if !installed {
			fmt.Println("not installed")
			return
		}
		content := stripHook(string(existing))
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), "#!/bin/sh")) == "" {
			os.Remove(path)
		} else if err := os.WriteFile(path, []byte(content+"\n"), 0o755); err != nil {
			fatal("write hook: %v", err)
		}
		fmt.Println("removed the agenthub post-commit hook")
	default:
		fmt.Fprintln(os.Stderr, "usage: ah hook [status | install [--channel NAME] | uninstall]")
		os.Exit(1)
	}
}

// isTrivialCommit reports whether a commit subject is routine noise that
// auto-share should skip (wip, fixups, merges, typos, formatting).
func isTrivialCommit(subject string) bool {
	s := strings.ToLower(strings.TrimSpace(subject))
	for _, p := range []string{"wip", "fixup!", "squash!", "amend!", "merge branch", "merge pull request", "merge remote-tracking", "typo", "fix typo", "whitespace", "format", "lint"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return strings.Contains(s, "typo")
}
