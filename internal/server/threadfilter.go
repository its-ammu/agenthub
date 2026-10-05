package server

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Defaults for the board: how many threads to show at first, and how many more
// each "load older" click adds.
const (
	threadPage    = 25
	threadMax     = 500
	postWindow    = 500  // posts loaded for a normal view
	postWindowBig = 2000 // posts loaded while searching or filtering
)

type threadFilter struct {
	Query string // case-insensitive text in a post, its author or its commit
	Agent string // exact agent id
	Tool  string // tool id; "human" matches posts made from the dashboard
}

func (f threadFilter) active() bool { return f.Query != "" || f.Agent != "" || f.Tool != "" }

// matches reports whether any post in the thread satisfies every active
// condition. Query, agent and tool are each checked against the whole thread,
// so a reply by the agent you filter on keeps its thread in view.
func (f threadFilter) matches(t thread) bool {
	posts := append([]postView{t.Root}, t.Replies...)
	hasAgent, hasTool, hasQuery := f.Agent == "", f.Tool == "", f.Query == ""
	q := strings.ToLower(f.Query)
	for _, p := range posts {
		if p.AgentID == f.Agent {
			hasAgent = true
		}
		tool := p.Tool
		if p.AgentID == HumanAgentID {
			tool = "human"
		}
		if tool == f.Tool {
			hasTool = true
		}
		if !hasQuery {
			hay := strings.ToLower(p.Content + " " + p.AgentID)
			if p.Commit != nil {
				hay += " " + strings.ToLower(p.Commit.Message+" "+p.Commit.Hash+" "+p.Commit.Branch)
			}
			if strings.Contains(hay, q) {
				hasQuery = true
			}
		}
	}
	return hasAgent && hasTool && hasQuery
}

func filterThreads(threads []thread, f threadFilter) []thread {
	if !f.active() {
		return threads
	}
	var out []thread
	for _, t := range threads {
		if f.matches(t) {
			out = append(out, t)
		}
	}
	return out
}

// filterChoices lists the agents and tools that appear in the threads, for the
// filter dropdowns.
func filterChoices(threads []thread) (agents, tools []string) {
	seenA, seenT := map[string]bool{}, map[string]bool{}
	add := func(p postView) {
		if !seenA[p.AgentID] {
			seenA[p.AgentID] = true
			agents = append(agents, p.AgentID)
		}
		tool := p.Tool
		if p.AgentID == HumanAgentID {
			tool = "human"
		}
		if tool != "" && !seenT[tool] {
			seenT[tool] = true
			tools = append(tools, tool)
		}
	}
	for _, t := range threads {
		add(t.Root)
		for _, r := range t.Replies {
			add(r)
		}
	}
	sort.Strings(agents)
	sort.Strings(tools)
	return
}

// pageSize reads the ?n= parameter: how many threads to show.
func pageSize(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < threadPage {
		return threadPage
	}
	if n > threadMax {
		return threadMax
	}
	return n
}

// boardURL builds a link back to the board that keeps the current channel and filters.
func boardURL(channel string, f threadFilter, n int) string {
	v := url.Values{}
	v.Set("channel", channel)
	if f.Query != "" {
		v.Set("q", f.Query)
	}
	if f.Agent != "" {
		v.Set("agent", f.Agent)
	}
	if f.Tool != "" {
		v.Set("tool", f.Tool)
	}
	if n > threadPage {
		v.Set("n", strconv.Itoa(n))
	}
	return "/?" + v.Encode()
}
