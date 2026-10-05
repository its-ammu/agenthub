package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Project scoping: a repo can be tied to a hub channel, stored in the repo's own
// git config (agenthub.channel) so nothing is added to the working tree.

const channelConfigKey = "agenthub.channel"

// gitConfigGet reads a local git config value, or "" if unset or not in a repo.
func gitConfigGet(key string) string {
	out, err := exec.Command("git", "config", "--local", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitConfigSet(key, value string) error {
	return exec.Command("git", "config", "--local", key, value).Run()
}

// projectChannel returns the channel configured for the current repo, or "".
func projectChannel() string { return gitConfigGet(channelConfigKey) }

var slugBad = regexp.MustCompile(`[^a-z0-9_-]+`)

// channelSlug turns a repo name into a valid channel name (1-31 chars of
// lowercase letters, digits, - and _, starting with a letter or digit).
func channelSlug(name string) string {
	s := slugBad.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-_")
	if len(s) > 31 {
		s = strings.Trim(s[:31], "-_")
	}
	if s == "" {
		return "project"
	}
	return s
}

// repoDisplayName names the current repo from its origin URL, else its folder.
func repoDisplayName() string {
	if origin, err := gitOutput("remote", "get-url", "origin"); err == nil {
		o := strings.TrimSuffix(strings.TrimSpace(origin), ".git")
		o = strings.ReplaceAll(o, ":", "/")
		if i := strings.LastIndex(o, "/"); i >= 0 && i < len(o)-1 {
			return o[i+1:]
		}
	}
	if top, err := gitOutput("rev-parse", "--show-toplevel"); err == nil {
		return filepath.Base(strings.TrimSpace(top))
	}
	return ""
}

func cmdProject(args []string) {
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "show":
		if ch := projectChannel(); ch != "" {
			fmt.Printf("#%s\n", ch)
		} else {
			fmt.Println("(no project channel; run `ah project init`)")
		}
	case "init":
		ch, created := projectInit(args[1:])
		if created {
			fmt.Printf("created #%s and set it as this repo's project channel\n", ch)
		} else {
			fmt.Printf("this repo's project channel is #%s\n", ch)
		}
		fmt.Println("`ah commit` now posts here by default (use --no-post to skip).")
	default:
		fmt.Fprintln(os.Stderr, "usage: ah project [show | init [--channel NAME]]")
		os.Exit(1)
	}
}

// projectInit creates (if needed) and records the channel for the current repo.
func projectInit(args []string) (channel string, created bool) {
	name := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--channel" && i+1 < len(args) {
			name = args[i+1]
			i++
		} else if !strings.HasPrefix(args[i], "-") && name == "" {
			name = args[i]
		}
	}
	repo := repoDisplayName()
	if repo == "" {
		fatal("not inside a git repository")
	}
	if name == "" {
		name = channelSlug(repo)
	} else if channelSlug(name) != name {
		fatal("invalid channel name %q (lowercase letters, digits, - or _, max 31 chars)", name)
	}

	cfg := mustLoadConfig()
	client := newClient(cfg)
	resp, err := client.postJSON("/api/channels", map[string]string{
		"name":        name,
		"description": "project: " + repo,
	})
	if err != nil {
		fatal("request failed: %v", err)
	}
	if resp.StatusCode == 409 {
		resp.Body.Close() // channel already exists: reuse it
	} else {
		var out map[string]any
		if err := readJSON(resp, &out); err != nil {
			fatal("create channel failed: %v", err)
		}
		created = true
	}
	if err := gitConfigSet(channelConfigKey, name); err != nil {
		fatal("could not save to git config: %v", err)
	}
	return name, created
}
