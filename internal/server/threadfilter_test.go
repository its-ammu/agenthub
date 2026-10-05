package server

import (
	"testing"

	"agenthub/internal/db"
)

func tv(id int, agent, tool, content string) postView {
	return postView{PostWithChannel: db.PostWithChannel{Post: db.Post{ID: id, AgentID: agent, Content: content}, ChannelName: "c"}, Tool: tool}
}

func sampleThreads() []thread {
	return []thread{
		{Root: tv(3, "neon", "claude", "Rounding bug is in tax.ts"), Replies: []postView{tv(4, "quiet", "cursor", "That is me, hold on")}},
		{Root: tv(2, "amber", "codex", "Lint is green")},
		{Root: tv(1, HumanAgentID, "", "use the staging db")},
	}
}

func ids(ts []thread) []int {
	var out []int
	for _, t := range ts {
		out = append(out, t.Root.ID)
	}
	return out
}

func TestFilterThreads(t *testing.T) {
	cases := []struct {
		name string
		f    threadFilter
		want []int
	}{
		{"no filter keeps all", threadFilter{}, []int{3, 2, 1}},
		{"query is case-insensitive", threadFilter{Query: "TAX.TS"}, []int{3}},
		{"query matches a reply", threadFilter{Query: "hold on"}, []int{3}},
		{"query matches the author", threadFilter{Query: "amber"}, []int{2}},
		{"agent keeps threads they replied in", threadFilter{Agent: "quiet"}, []int{3}},
		{"tool filter", threadFilter{Tool: "codex"}, []int{2}},
		{"human tool", threadFilter{Tool: "human"}, []int{1}},
		{"conditions combine", threadFilter{Tool: "cursor", Query: "lint"}, nil},
		{"no match", threadFilter{Query: "zzz"}, nil},
	}
	for _, c := range cases {
		got := ids(filterThreads(sampleThreads(), c.f))
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			}
		}
	}
}

func TestFilterChoices(t *testing.T) {
	agents, tools := filterChoices(sampleThreads())
	if len(agents) != 4 || agents[0] != "amber" {
		t.Errorf("agents = %v", agents)
	}
	want := map[string]bool{"claude": true, "cursor": true, "codex": true, "human": true}
	for _, tl := range tools {
		delete(want, tl)
	}
	if len(want) != 0 {
		t.Errorf("tools missing %v (got %v)", want, tools)
	}
}

func TestPageSizeAndBoardURL(t *testing.T) {
	for raw, want := range map[string]int{"": 25, "abc": 25, "10": 25, "50": 50, "99999": 500} {
		if got := pageSize(raw); got != want {
			t.Errorf("pageSize(%q) = %d, want %d", raw, got, want)
		}
	}
	if got := boardURL("general", threadFilter{Query: "a b", Agent: "x"}, 50); got != "/?agent=x&channel=general&n=50&q=a+b" {
		t.Errorf("boardURL = %s", got)
	}
	if got := boardURL("general", threadFilter{}, 25); got != "/?channel=general" {
		t.Errorf("boardURL = %s", got)
	}
}
