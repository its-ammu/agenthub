// Package usage reads local agent session transcripts to report token usage and cost.
package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ModelUsage struct {
	Model        string  `json:"model"`
	Input        int64   `json:"input_tokens"`
	Output       int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read_tokens"`
	CacheWrite5m int64   `json:"cache_write_5m_tokens"`
	CacheWrite1h int64   `json:"cache_write_1h_tokens"`
	Cost         float64 `json:"cost_usd"`
	Priced       bool    `json:"priced"`
}

type Report struct {
	Tool      string       `json:"tool"`
	Found     bool         `json:"found"`
	Turns     int          `json:"turns"`
	FirstAt   string       `json:"first_at,omitempty"`
	LastAt    string       `json:"last_at,omitempty"`
	Models    []ModelUsage `json:"models,omitempty"`
	TotalCost float64      `json:"total_cost_usd"`
	CostKnown bool         `json:"cost_known"` // false when no cost could be computed
	EstTokens int64        `json:"estimated_tokens,omitempty"`
	Notes     []string     `json:"notes,omitempty"`
}

// price is USD per million tokens. Cache writes are 1.25x (5m) and 2x (1h) of input.
type price struct{ in, out, cacheRead float64 }

// Anthropic first-party API list prices. Estimates only: they ignore batch,
// fast-mode and plan discounts, so a subscription user's real spend differs.
var prices = map[string]price{
	"claude-fable-5-1":  {10, 50, 0.25},
	"claude-mythos-5-1": {10, 50, 0.25},
	"claude-fable-5":    {10, 50, 1},
	"claude-mythos-5":   {10, 50, 1},
	"claude-opus-5-5":   {4, 20, 0.20},
	"claude-opus-5":     {5, 25, 0.50},
	"claude-opus-4-8":   {5, 25, 0.50},
	"claude-opus-4-7":   {5, 25, 0.50},
	"claude-opus-4-6":   {5, 25, 0.50},
	"claude-opus-4-5":   {5, 25, 0.50},
	"claude-sonnet-5-5": {2, 10, 0.20},
	"claude-sonnet-5":   {2, 10, 0.20},
	"claude-sonnet-4-6": {3, 15, 0.30},
	"claude-sonnet-4-5": {3, 15, 0.30},
	"claude-sonnet-4":   {3, 15, 0.30},
	"claude-haiku-4-5":  {1, 5, 0.10},
}

func lookup(model string) (price, bool) {
	best, found := "", false
	for k := range prices {
		if strings.HasPrefix(model, k) && len(k) > len(best) {
			best, found = k, true
		}
	}
	return prices[best], found
}

type claudeLine struct {
	Timestamp string `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input         int64 `json:"input_tokens"`
			Output        int64 `json:"output_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			CacheDetail   *struct {
				W5m int64 `json:"ephemeral_5m_input_tokens"`
				W1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// Claude reads a Claude Code session transcript (and its subagent transcripts).
func Claude(sessionID string) Report {
	r := Report{Tool: "claude"}
	if !safeID(sessionID) {
		return r
	}
	root := filepath.Join(home(), ".claude", "projects")
	files, _ := filepath.Glob(filepath.Join(root, "*", sessionID+".jsonl"))
	if len(files) == 0 {
		r.Notes = append(r.Notes, "No Claude Code transcript found for this session on this machine.")
		return r
	}
	sub, _ := filepath.Glob(filepath.Join(root, "*", sessionID, "subagents", "*.jsonl"))
	files = append(files, sub...)
	r.Found = true

	// Streaming writes the same message several times; keep the last usage per id.
	type entry struct {
		model string
		u     ModelUsage
	}
	byID := map[string]entry{}
	var anon []entry
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
		for sc.Scan() {
			var l claudeLine
			if json.Unmarshal(sc.Bytes(), &l) != nil {
				continue
			}
			if l.Timestamp != "" {
				if r.FirstAt == "" || l.Timestamp < r.FirstAt {
					r.FirstAt = l.Timestamp
				}
				if l.Timestamp > r.LastAt {
					r.LastAt = l.Timestamp
				}
			}
			u := l.Message.Usage
			if u == nil || l.Message.Model == "" || strings.HasPrefix(l.Message.Model, "<") {
				continue
			}
			mu := ModelUsage{Model: l.Message.Model, Input: u.Input, Output: u.Output, CacheRead: u.CacheRead}
			if u.CacheDetail != nil {
				mu.CacheWrite5m, mu.CacheWrite1h = u.CacheDetail.W5m, u.CacheDetail.W1h
			} else {
				mu.CacheWrite5m = u.CacheCreation
			}
			e := entry{l.Message.Model, mu}
			if l.Message.ID != "" {
				byID[l.Message.ID] = e
			} else {
				anon = append(anon, e)
			}
		}
		fh.Close()
	}

	agg := map[string]*ModelUsage{}
	add := func(e entry) {
		r.Turns++
		m := agg[e.model]
		if m == nil {
			m = &ModelUsage{Model: e.model}
			agg[e.model] = m
		}
		m.Input += e.u.Input
		m.Output += e.u.Output
		m.CacheRead += e.u.CacheRead
		m.CacheWrite5m += e.u.CacheWrite5m
		m.CacheWrite1h += e.u.CacheWrite1h
	}
	for _, e := range byID {
		add(e)
	}
	for _, e := range anon {
		add(e)
	}

	for _, m := range agg {
		if p, ok := lookup(m.Model); ok {
			m.Priced = true
			m.Cost = (float64(m.Input)*p.in + float64(m.Output)*p.out + float64(m.CacheRead)*p.cacheRead +
				float64(m.CacheWrite5m)*p.in*1.25 + float64(m.CacheWrite1h)*p.in*2) / 1e6
			r.TotalCost += m.Cost
			r.CostKnown = true
		} else {
			r.Notes = append(r.Notes, "No list price known for "+m.Model+"; it is excluded from the cost total.")
		}
		r.Models = append(r.Models, *m)
	}
	sort.Slice(r.Models, func(i, j int) bool { return r.Models[i].Cost > r.Models[j].Cost })
	r.Notes = append(r.Notes, "Cost is an estimate at Anthropic API list prices. Subscription plans bill differently.")
	return r
}

type cursorLine struct {
	Role    string `json:"role"`
	Message struct {
		Content []json.RawMessage `json:"content"`
	} `json:"message"`
}

// Cursor reads a Cursor agent transcript. Cursor stores no token counts locally,
// so this only estimates volume from the transcript text.
func Cursor(sessionID string) Report {
	r := Report{Tool: "cursor"}
	if !safeID(sessionID) {
		return r
	}
	files, _ := filepath.Glob(filepath.Join(home(), ".cursor", "projects", "*", "agent-transcripts", sessionID, sessionID+".jsonl"))
	if len(files) == 0 {
		r.Notes = append(r.Notes, "No Cursor transcript found for this session on this machine.")
		return r
	}
	r.Found = true
	fh, err := os.Open(files[0])
	if err != nil {
		return r
	}
	defer fh.Close()
	if st, err := fh.Stat(); err == nil {
		r.LastAt = st.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	var chars int64
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var l cursorLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.Role == "assistant" {
			r.Turns++
		}
		for _, c := range l.Message.Content {
			chars += int64(len(c))
		}
	}
	r.EstTokens = chars / 4
	r.Notes = append(r.Notes,
		"Cursor does not store token usage locally, so no cost can be computed here.",
		"Estimated tokens come from transcript text (~4 characters per token). Real usage is higher because hidden context and system prompts are not in the transcript.",
		"For billed usage, check the Cursor dashboard (cursor.com/dashboard).")
	return r
}

func safeID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c == '-' || c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}
