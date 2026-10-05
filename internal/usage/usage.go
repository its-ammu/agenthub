// Package usage reads local agent session transcripts to report token usage and cost.
package usage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	FastTurns    int     `json:"fast_turns,omitempty"`
}

// Bucket is cost over one slice of the session timeline.
type Bucket struct {
	Start string  `json:"start"`
	Cost  float64 `json:"cost_usd"`
	Turns int     `json:"turns"`
}

type Report struct {
	Tool      string       `json:"tool"`
	Found     bool         `json:"found"`
	Turns     int          `json:"turns"`
	FirstAt   string       `json:"first_at,omitempty"`
	LastAt    string       `json:"last_at,omitempty"`
	Models    []ModelUsage `json:"models,omitempty"`
	TotalCost float64      `json:"total_cost_usd"`
	// Breakdown of TotalCost by where the turns ran.
	MainCost      float64  `json:"main_cost_usd,omitempty"`
	SubagentCost  float64  `json:"subagent_cost_usd,omitempty"`
	SubagentTurns int      `json:"subagent_turns,omitempty"`
	FastTurns     int      `json:"fast_turns,omitempty"`
	BucketMinutes int      `json:"bucket_minutes,omitempty"`
	Timeline      []Bucket `json:"timeline,omitempty"`
	CostKnown     bool     `json:"cost_known"` // false when no cost could be computed
	EstTokens     int64    `json:"estimated_tokens,omitempty"`
	Notes         []string `json:"notes,omitempty"`
}

type claudeLine struct {
	Timestamp string `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Speed         string `json:"speed"`
			Input         int64  `json:"input_tokens"`
			Output        int64  `json:"output_tokens"`
			CacheRead     int64  `json:"cache_read_input_tokens"`
			CacheCreation int64  `json:"cache_creation_input_tokens"`
			CacheDetail   *struct {
				W5m int64 `json:"ephemeral_5m_input_tokens"`
				W1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// For returns the usage report for a session of the given tool. Tools without
// a transcript reader get a report that says so.
func For(tool, sessionID string) Report {
	switch tool {
	case "claude":
		return Claude(sessionID)
	case "cursor":
		return Cursor(sessionID)
	}
	return Report{Tool: tool, Notes: []string{"Usage reporting is not available for this tool: there is no transcript reader for it."}}
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// turn is one deduplicated assistant message with its token usage.
type turn struct {
	model string
	u     ModelUsage
	at    string
	fast  bool
	sub   bool // ran in a subagent transcript
}

// Claude reads a Claude Code session transcript (and its subagent transcripts).
func Claude(sessionID string) Report {
	r := Report{Tool: "claude"}
	if !safeID(sessionID) {
		return r
	}
	root := filepath.Join(home(), ".claude", "projects")
	mains, _ := filepath.Glob(filepath.Join(root, "*", sessionID+".jsonl"))
	if len(mains) == 0 {
		r.Notes = append(r.Notes, "No Claude Code transcript found for this session on this machine.")
		return r
	}
	subs, _ := filepath.Glob(filepath.Join(root, "*", sessionID, "subagents", "*.jsonl"))
	r.Found = true

	// Streaming writes the same message several times; keep the last usage per id.
	// Ids are scoped per file so a main and a subagent transcript never collide.
	var turns []turn
	read := func(path string, sub bool) {
		fh, err := os.Open(path)
		if err != nil {
			return
		}
		defer fh.Close()
		byID := map[string]int{}
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
			t := turn{model: l.Message.Model, u: mu, at: l.Timestamp, fast: u.Speed == "fast", sub: sub}
			if id := l.Message.ID; id != "" {
				if i, ok := byID[id]; ok {
					turns[i] = t
					continue
				}
				byID[id] = len(turns)
			}
			turns = append(turns, t)
		}
	}
	for _, f := range mains {
		read(f, false)
	}
	for _, f := range subs {
		read(f, true)
	}

	agg := map[string]*ModelUsage{}
	unpriced := map[string]bool{}
	var priced []turn
	costs := []float64{}
	for _, t := range turns {
		r.Turns++
		m := agg[t.model]
		if m == nil {
			m = &ModelUsage{Model: t.model}
			agg[t.model] = m
		}
		m.Input += t.u.Input
		m.Output += t.u.Output
		m.CacheRead += t.u.CacheRead
		m.CacheWrite5m += t.u.CacheWrite5m
		m.CacheWrite1h += t.u.CacheWrite1h
		if t.fast {
			m.FastTurns++
			r.FastTurns++
		}
		if t.sub {
			r.SubagentTurns++
		}
		p, ok := lookup(t.model)
		if !ok {
			unpriced[t.model] = true
			continue
		}
		m.Priced = true
		c := p.cost(t.u.Input, t.u.Output, t.u.CacheRead, t.u.CacheWrite5m, t.u.CacheWrite1h, t.fast)
		m.Cost += c
		r.TotalCost += c
		r.CostKnown = true
		if t.sub {
			r.SubagentCost += c
		} else {
			r.MainCost += c
		}
		priced = append(priced, t)
		costs = append(costs, c)
	}
	for model := range unpriced {
		r.Notes = append(r.Notes, "No price known for "+model+"; it is excluded from the cost total. Add it with --prices.")
	}
	for _, m := range agg {
		r.Models = append(r.Models, *m)
	}
	sort.Slice(r.Models, func(i, j int) bool { return r.Models[i].Cost > r.Models[j].Cost })

	r.Timeline, r.BucketMinutes = buildTimeline(priced, costs)

	r.Notes = append(r.Notes, "Cost is an estimate at Anthropic API list prices. Subscription plans bill differently.")
	if r.FastTurns > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("%d turns ran in fast mode and are priced with the model's fast multiplier where one is configured.", r.FastTurns))
	}
	return r
}

var bucketSizes = []int{1, 5, 10, 15, 30, 60, 120, 360, 720, 1440} // minutes

// buildTimeline sums turn costs into at most ~40 equal time buckets.
func buildTimeline(turns []turn, costs []float64) ([]Bucket, int) {
	type pt struct {
		t time.Time
		c float64
	}
	var pts []pt
	for i, t := range turns {
		ts, err := time.Parse(time.RFC3339Nano, t.at)
		if err != nil {
			continue
		}
		pts = append(pts, pt{ts, costs[i]})
	}
	if len(pts) == 0 {
		return nil, 0
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].t.Before(pts[j].t) })
	span := pts[len(pts)-1].t.Sub(pts[0].t)
	size := bucketSizes[len(bucketSizes)-1]
	for _, m := range bucketSizes {
		if span <= time.Duration(m)*time.Minute*40 {
			size = m
			break
		}
	}
	step := time.Duration(size) * time.Minute
	start := pts[0].t.Truncate(step)
	n := int(pts[len(pts)-1].t.Sub(start)/step) + 1
	out := make([]Bucket, n)
	for i := range out {
		out[i].Start = start.Add(time.Duration(i) * step).UTC().Format(time.RFC3339)
	}
	for _, p := range pts {
		i := int(p.t.Sub(start) / step)
		out[i].Cost += p.c
		out[i].Turns++
	}
	return out, size
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
