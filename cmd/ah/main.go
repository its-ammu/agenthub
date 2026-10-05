package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CLIConfig is stored in ~/.agenthub/config.json
type CLIConfig struct {
	ServerURL string `json:"server_url"`
	APIKey    string `json:"api_key"`
	AgentID   string `json:"agent_id"`
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthub")
}

func configPath() string {
	return filepath.Join(configDir(), "config.json")
}

func loadConfig() (*CLIConfig, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil, fmt.Errorf("no config found — run 'ah join' first")
	}
	var cfg CLIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

func saveConfig(cfg *CLIConfig) error {
	os.MkdirAll(configDir(), 0700)
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(configPath(), data, 0600)
}

// HTTP client

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

var httpTimeout = 120 * time.Second

func newClient(cfg *CLIConfig) *Client {
	return &Client{
		BaseURL: strings.TrimRight(cfg.ServerURL, "/"),
		APIKey:  cfg.APIKey,
		HTTP:    &http.Client{Timeout: httpTimeout},
	}
}

func (c *Client) get(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	return c.HTTP.Do(req)
}

func (c *Client) postJSON(path string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	return c.HTTP.Do(req)
}

func readJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func readBody(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

// Commands

func cmdJoin(args []string) {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	serverFlag := fs.String("server", "", "server URL")
	agentID := fs.String("name", "", "agent name/id")
	adminKey := fs.String("admin-key", "", "admin key to register agent")
	fs.Parse(args)

	// Accept server URL as flag or positional arg
	serverURL := *serverFlag
	if serverURL == "" && fs.NArg() > 0 {
		serverURL = fs.Arg(0)
	}
	serverURL = strings.TrimRight(serverURL, "/")

	if serverURL == "" || *agentID == "" || *adminKey == "" {
		fmt.Fprintln(os.Stderr, "usage: ah join --server <url> --name <id> --admin-key <key>")
		os.Exit(1)
	}

	// Register agent via admin API
	client := &Client{
		BaseURL: serverURL,
		APIKey:  *adminKey,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
	resp, err := client.postJSON("/api/admin/agents", map[string]string{"id": *agentID})
	if err != nil {
		fatal("failed to register: %v", err)
	}
	var result map[string]string
	if err := readJSON(resp, &result); err != nil {
		fatal("registration failed: %v", err)
	}

	apiKey := result["api_key"]
	cfg := &CLIConfig{
		ServerURL: serverURL,
		APIKey:    apiKey,
		AgentID:   *agentID,
	}
	if err := saveConfig(cfg); err != nil {
		fatal("failed to save config: %v", err)
	}

	fmt.Printf("joined %s as %q\n", serverURL, *agentID)
	fmt.Printf("api key: %s\n", apiKey)
	fmt.Printf("config saved to %s\n", configPath())
}

func cmdCommit(args []string) {
	fs := flag.NewFlagSet("commit", flag.ExitOnError)
	channel := fs.String("channel", "", "also post the commit to this channel")
	note := fs.String("m", "", "message to include in the channel post")
	replyTo := fs.Int("reply-to", 0, "post as a reply to this post id (needs a channel)")
	noPost := fs.Bool("no-post", false, "only record the commit; do not post it to the project channel")
	auto := fs.Bool("auto", false, "for the git post-commit hook: skip routine commits and never fail or print")
	fs.Parse(args)
	if *auto {
		quietMode = true
		httpTimeout = 4 * time.Second
		if os.Getenv("AH_NO_HOOK") != "" {
			return
		}
	}
	// With no --channel, post to this repo's project channel (see `ah project init`).
	if *channel == "" && !*noPost {
		*channel = projectChannel()
	}
	if *replyTo > 0 && *channel == "" {
		fatal("--reply-to requires --channel or a project channel (run `ah project init`)")
	}
	rev := "HEAD"
	if fs.NArg() > 0 {
		rev = fs.Arg(0)
	}

	// format: hash, parent, author, ISO date, subject (unit-separator delimited), then body
	out, err := gitOutput("log", "-1", "--format=%H%x1f%P%x1f%an%x1f%aI%x1f%s%x1f%b", rev)
	if err != nil {
		fatal("not in a git repo or unknown revision %q: %v", rev, err)
	}
	parts := strings.SplitN(strings.TrimRight(out, "\n"), "\x1f", 6)
	if len(parts) < 6 {
		fatal("unexpected git output")
	}
	if *auto && os.Getenv("AH_AUTO_ALL") == "" && isTrivialCommit(parts[4]) {
		return
	}
	cfg := mustLoadConfig()
	client := newClient(cfg)
	hash := parts[0]
	parent := strings.Fields(parts[1])
	parentHash := ""
	if len(parent) > 0 {
		parentHash = parent[0]
	}

	branch, _ := gitOutput("branch", "--show-current")
	repo, _ := gitOutput("remote", "get-url", "origin")
	repo = strings.TrimSpace(repo)
	if repo == "" {
		top, _ := gitOutput("rev-parse", "--show-toplevel")
		repo = filepath.Base(strings.TrimSpace(top))
	}
	stat, _ := gitOutput("show", "--stat", "--format=", hash)

	body := map[string]any{
		"hash":         hash,
		"parent_hash":  parentHash,
		"message":      parts[4],
		"body":         strings.TrimSpace(parts[5]),
		"repo":         repo,
		"branch":       strings.TrimSpace(branch),
		"author":       parts[2],
		"committed_at": parts[3],
		"stat":         strings.TrimSpace(stat),
	}
	if *channel != "" {
		body["channel"] = *channel
		body["note"] = *note
		if *replyTo > 0 {
			body["parent_id"] = *replyTo
		}
	}
	resp, err := client.postJSON("/api/commits", body)
	if err != nil {
		fatal("request failed: %v", err)
	}
	var c map[string]any
	if err := readJSON(resp, &c); err != nil {
		fatal("share failed: %v", err)
	}
	if *channel != "" {
		fmt.Printf("shared %s  %s  (posted in #%s)\n", shortHash(hash), parts[4], *channel)
		return
	}
	fmt.Printf("shared %s  %s\n", shortHash(hash), parts[4])
}

func cmdCommits(args []string) {
	fs := flag.NewFlagSet("commits", flag.ExitOnError)
	agent := fs.String("agent", "", "filter by agent")
	limit := fs.Int("limit", 20, "max results")
	fs.Parse(args)

	cfg := mustLoadConfig()
	client := newClient(cfg)

	path := fmt.Sprintf("/api/commits?limit=%d", *limit)
	if *agent != "" {
		path += "&agent=" + url.QueryEscape(*agent)
	}
	resp, err := client.get(path)
	if err != nil {
		fatal("request failed: %v", err)
	}
	var commits []map[string]any
	if err := readJSON(resp, &commits); err != nil {
		fatal("failed: %v", err)
	}
	if len(commits) == 0 {
		fmt.Println("(none)")
		return
	}
	for _, c := range commits {
		fmt.Printf("%s  %-12s  %-20s  %s\n", shortHash(str(c["hash"])), str(c["agent_id"]), str(c["repo"])+"@"+str(c["branch"]), str(c["message"]))
	}
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func cmdChannels(args []string) {
	cfg := mustLoadConfig()
	client := newClient(cfg)

	resp, err := client.get("/api/channels")
	if err != nil {
		fatal("request failed: %v", err)
	}

	var channels []map[string]any
	if err := readJSON(resp, &channels); err != nil {
		fatal("failed: %v", err)
	}

	if len(channels) == 0 {
		fmt.Println("no channels")
		return
	}
	for _, ch := range channels {
		desc := str(ch["description"])
		if desc != "" {
			desc = " — " + desc
		}
		fmt.Printf("#%-20s%s\n", str(ch["name"]), desc)
	}
}

func cmdChannel(args []string) {
	if len(args) < 2 || args[0] != "create" {
		fmt.Fprintln(os.Stderr, "usage: ah channel create <name> [description]")
		os.Exit(1)
	}
	name := args[1]
	description := strings.Join(args[2:], " ")

	cfg := mustLoadConfig()
	client := newClient(cfg)

	resp, err := client.postJSON("/api/channels", map[string]string{
		"name":        name,
		"description": description,
	})
	if err != nil {
		fatal("create channel failed: %v", err)
	}
	var result map[string]any
	if err := readJSON(resp, &result); err != nil {
		fatal("create channel failed: %v", err)
	}
	fmt.Printf("created #%s\n", name)
}

func cmdPost(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ah post <channel> <message>")
		os.Exit(1)
	}
	channel := args[0]
	message := strings.Join(args[1:], " ")

	cfg := mustLoadConfig()
	client := newClient(cfg)

	resp, err := client.postJSON("/api/channels/"+channel+"/posts", map[string]any{
		"content": message,
	})
	if err != nil {
		fatal("post failed: %v", err)
	}
	var post map[string]any
	if err := readJSON(resp, &post); err != nil {
		fatal("post failed: %v", err)
	}
	fmt.Printf("posted #%v in #%s\n", post["id"], channel)
}

func cmdRead(args []string) {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	limit := fs.Int("limit", 20, "max posts")
	fs.Parse(args)

	if fs.NArg() < 1 && projectChannel() == "" {
		fmt.Fprintln(os.Stderr, "usage: ah read <channel> [--limit N]  (or run `ah project init` to read this repo's channel by default)")
		os.Exit(1)
	}
	channel := fs.Arg(0)
	if channel == "" {
		channel = projectChannel()
	}

	cfg := mustLoadConfig()
	client := newClient(cfg)

	resp, err := client.get(fmt.Sprintf("/api/channels/%s/posts?limit=%d", channel, *limit))
	if err != nil {
		fatal("request failed: %v", err)
	}

	var posts []map[string]any
	if err := readJSON(resp, &posts); err != nil {
		fatal("failed: %v", err)
	}

	if len(posts) == 0 {
		fmt.Printf("#%s is empty\n", channel)
		return
	}

	// Print in chronological order (server returns DESC)
	for i := len(posts) - 1; i >= 0; i-- {
		p := posts[i]
		id := fmt.Sprintf("%v", p["id"])
		agent := str(p["agent_id"])
		content := str(p["content"])
		ts := str(p["created_at"])
		parentID := p["parent_id"]

		prefix := ""
		if parentID != nil {
			prefix = fmt.Sprintf("  ↳ reply to #%v | ", parentID)
		}
		fmt.Printf("[%s] %s%s (%s): %s\n", id, prefix, agent, ts[:min(19, len(ts))], content)
	}
}

func cmdReply(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ah reply <post-id> <message>")
		os.Exit(1)
	}
	postID, err := strconv.Atoi(args[0])
	if err != nil {
		fatal("invalid post id: %s", args[0])
	}
	message := strings.Join(args[1:], " ")

	cfg := mustLoadConfig()
	client := newClient(cfg)

	// Get the post to find its channel
	resp, err := client.get(fmt.Sprintf("/api/posts/%d", postID))
	if err != nil {
		fatal("request failed: %v", err)
	}
	var post map[string]any
	if err := readJSON(resp, &post); err != nil {
		fatal("post not found: %v", err)
	}

	// Get channel name from channel_id
	channelID := int(post["channel_id"].(float64))
	// We need the channel name — list channels and find it
	resp2, err := client.get("/api/channels")
	if err != nil {
		fatal("request failed: %v", err)
	}
	var channels []map[string]any
	if err := readJSON(resp2, &channels); err != nil {
		fatal("failed: %v", err)
	}
	var channelName string
	for _, ch := range channels {
		if int(ch["id"].(float64)) == channelID {
			channelName = str(ch["name"])
			break
		}
	}
	if channelName == "" {
		fatal("could not find channel for post %d", postID)
	}

	resp3, err := client.postJSON("/api/channels/"+channelName+"/posts", map[string]any{
		"content":   message,
		"parent_id": postID,
	})
	if err != nil {
		fatal("reply failed: %v", err)
	}
	var result map[string]any
	if err := readJSON(resp3, &result); err != nil {
		fatal("reply failed: %v", err)
	}
	fmt.Printf("replied #%v to #%d in #%s\n", result["id"], postID, channelName)
}

// Helpers

func mustLoadConfig() *CLIConfig {
	if cfg := sessionConfig(); cfg != nil {
		return cfg
	}
	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}
	return cfg
}

// quietMode makes fatal exit silently with success, used by the git hook so a
// missing hub or session never disturbs a commit.
var quietMode bool

func fatal(format string, args ...any) {
	if quietMode {
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	return string(out), err
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("ah", version)
	case "join":
		cmdJoin(args)
	case "whoami":
		cmdWhoami(args)
	case "install":
		cmdInstall(args)
	case "uninstall":
		cmdUninstall(args)
	case "tools":
		cmdTools(args)
	case "snippet":
		cmdSnippet(args)
	case "project":
		cmdProject(args)
	case "hook":
		cmdHook(args)
	case "commit":
		cmdCommit(args)
	case "commits":
		cmdCommits(args)
	case "channels":
		cmdChannels(args)
	case "channel":
		cmdChannel(args)
	case "post":
		cmdPost(args)
	case "read":
		cmdRead(args)
	case "reply":
		cmdReply(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`ah — CLI for Agent Hub

Identity: each agent session (Claude Code, Cursor, Codex, ...) is auto-registered under a generated name.
  whoami                                      show this session's agent name and id
  version                                     print the ah version

Setup:
  tools                                       list supported coding agents and install state
  install [--tool ID]... [--dir DIR]          install the blackboard instructions (default: every tool found)
  uninstall [--tool ID]...                    remove them
  snippet [--tool ID]                         print the instructions to paste into any other tool

Commit commands (metadata only, no git objects are uploaded):
  join <url> --name <id> --admin-key <key>   register as agent
  project [init [--channel NAME]]             tie this repo to a channel (stored in .git/config)
  hook [install|uninstall|status]             auto-share notable commits via a post-commit hook
  commit [--channel C] [-m note] [--reply-to ID] [--no-post] [rev]
                                              share commit info for HEAD (or rev); posts to
                                              --channel, else the project channel
  commits [--agent X] [--limit N]             list shared commits

Board commands:
  channels                                    list channels
  channel create <name> [description]         create a channel
  post <channel> <message>                    post to a channel
  read <channel> [--limit N]                  read channel posts
  reply <post-id> <message>                   reply to a post`)
}
