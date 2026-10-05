package server

import (
	"html/template"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"agenthub/internal/db"
)

// HumanAgentID is the agent identity used for posts made from the web UI.
const HumanAgentID = "human"

type postView struct {
	db.PostWithChannel
	ReplyToAgent string // agent being replied to, for nested replies
	IsReply      bool
	ReplyCount   int  // replies in the thread (roots only)
	ShowChannel  bool // show channel tag (all-channels view)
	Commit       *db.Commit
	Tool         string // tool id (claude, cursor, codex, ...) for session agents
}

type thread struct {
	Root    postView
	Replies []postView
	latest  int // highest post id in the thread; ids grow with time
}

type dashboardData struct {
	Stats        *db.Stats
	Sessions     []db.Agent // session agents, newest first
	MoreSessions int        // sessions beyond the first few shown
	Channels     []db.Channel
	View         string // "board" or "commits"
	Selected     string // channel name; "" means all channels
	Threads      []thread
	Commits      []db.Commit
	Error        string
	Now          time.Time
	ChannelDesc  string
	PostCount    int
}

// buildThreads groups posts into top-level threads with all descendant replies
// flattened beneath each root (oldest first, so a conversation reads top to
// bottom). Threads are ordered by latest activity, newest first, so a fresh
// reply moves its thread to the top.
func buildThreads(posts []db.PostWithChannel, showChannel bool, commits map[int]*db.Commit, tools map[string]string) []thread {
	byID := make(map[int]db.PostWithChannel, len(posts))
	for _, p := range posts {
		byID[p.ID] = p
	}
	rootOf := func(p db.PostWithChannel) (int, bool) {
		for i := 0; i < 1000; i++ {
			if p.ParentID == nil {
				return p.ID, true
			}
			parent, ok := byID[*p.ParentID]
			if !ok {
				return 0, false
			}
			p = parent
		}
		return 0, false
	}

	idx := map[int]int{}
	var threads []thread
	// posts arrive newest first; roots keep that order
	for _, p := range posts {
		if p.ParentID == nil {
			idx[p.ID] = len(threads)
			threads = append(threads, thread{latest: p.ID, Root: postView{PostWithChannel: p, ShowChannel: showChannel, Commit: commits[p.ID], Tool: tools[p.AgentID]}})
		}
	}
	// replies oldest first
	for i := len(posts) - 1; i >= 0; i-- {
		p := posts[i]
		if p.ParentID == nil {
			continue
		}
		root, ok := rootOf(p)
		if !ok {
			continue
		}
		v := postView{PostWithChannel: p, IsReply: true, ShowChannel: showChannel, Commit: commits[p.ID], Tool: tools[p.AgentID]}
		if parent, ok := byID[*p.ParentID]; ok && parent.ID != root {
			v.ReplyToAgent = parent.AgentID
		}
		t := &threads[idx[root]]
		t.Replies = append(t.Replies, v)
		t.Root.ReplyCount++
		if p.ID > t.latest {
			t.latest = p.ID
		}
	}
	sort.SliceStable(threads, func(i, j int) bool { return threads[i].latest > threads[j].latest })
	return threads
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	q := r.URL.Query()
	data := dashboardData{Selected: "general", View: "board", Now: time.Now().UTC(), Error: q.Get("error")}
	if _, ok := q["channel"]; ok {
		data.Selected = q.Get("channel")
	}
	if q.Get("view") == "commits" {
		data.View = "commits"
		data.Selected = ""
	}

	data.Stats, _ = s.db.GetStats()
	data.Channels, _ = s.db.ListChannels()
	tools := map[string]string{}
	if agents, err := s.db.ListAgents(); err == nil {
		for i := len(agents) - 1; i >= 0; i-- {
			a := agents[i]
			if a.Tool != "" {
				tools[a.ID] = a.Tool
				if len(data.Sessions) < 200 {
					data.Sessions = append(data.Sessions, a)
				}
			}
		}
	}

	if n := len(data.Sessions) - 5; n > 0 {
		data.MoreSessions = n
	}

	if data.View == "commits" {
		data.Commits, _ = s.db.ListCommits(q.Get("agent"), 200, 0)
	} else {
		var posts []db.PostWithChannel
		if data.Selected == "" {
			posts, _ = s.db.RecentPosts(500)
		} else if ch, _ := s.db.GetChannelByName(data.Selected); ch != nil {
			data.ChannelDesc = ch.Description
			plain, _ := s.db.ListPosts(ch.ID, 500, 0)
			for _, p := range plain {
				posts = append(posts, db.PostWithChannel{Post: p, ChannelName: ch.Name})
			}
		}
		data.PostCount = len(posts)
		linked := map[int]*db.Commit{}
		if cs, err := s.db.ListLinkedCommits(); err == nil {
			for i := range cs {
				linked[*cs[i].PostID] = &cs[i]
			}
		}
		data.Threads = buildThreads(posts, data.Selected == "", linked, tools)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	dashboardTmpl.Execute(w, data)
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func redirectBoard(w http.ResponseWriter, r *http.Request, channel, errMsg string) {
	target := "/?channel=" + channel
	if errMsg != "" {
		target += "&error=" + strings.ReplaceAll(errMsg, " ", "+")
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleUIPost lets a human post from the dashboard as the "human" agent.
// Restricted to loopback requests since the dashboard itself is unauthenticated.
func (s *Server) handleUIPost(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "posting from the UI is only allowed from localhost", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	channel := r.FormValue("channel")
	content := strings.TrimSpace(r.FormValue("content"))

	ch, _ := s.db.GetChannelByName(channel)
	if ch == nil {
		redirectBoard(w, r, "", "channel not found")
		return
	}
	if content == "" || len(content) > 32*1024 {
		redirectBoard(w, r, channel, "message must be 1 to 32768 characters")
		return
	}

	var parent *int
	if id, err := strconv.Atoi(r.FormValue("parent_id")); err == nil && id > 0 {
		p, _ := s.db.GetPost(id)
		if p == nil || p.ChannelID != ch.ID {
			redirectBoard(w, r, channel, "reply target not found in this channel")
			return
		}
		parent = &id
	}

	if _, err := s.db.CreatePost(ch.ID, HumanAgentID, parent, content); err != nil {
		redirectBoard(w, r, channel, "failed to post")
		return
	}
	redirectBoard(w, r, channel, "")
}

// handleUIChannel lets a human create a channel from the dashboard.
func (s *Server) handleUIChannel(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "only allowed from localhost", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if !channelNameRe.MatchString(name) {
		redirectBoard(w, r, r.FormValue("current"), "channel name must be lowercase letters, digits, - or _ (max 31)")
		return
	}
	if existing, _ := s.db.GetChannelByName(name); existing == nil {
		channels, _ := s.db.ListChannels()
		if len(channels) >= 100 {
			redirectBoard(w, r, r.FormValue("current"), "channel limit reached")
			return
		}
		if err := s.db.CreateChannel(name, strings.TrimSpace(r.FormValue("description"))); err != nil {
			redirectBoard(w, r, r.FormValue("current"), "failed to create channel")
			return
		}
	}
	redirectBoard(w, r, name, "")
}

// handleUIDeletePost deletes a post (or reply) and everything beneath it.
func (s *Server) handleUIDeletePost(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "only allowed from localhost", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	channel := r.FormValue("channel")
	if id, err := strconv.Atoi(r.FormValue("id")); err == nil {
		s.db.DeletePost(id)
	}
	redirectBoard(w, r, channel, "")
}

// handleUIDeleteCommit deletes a commit record, plus its board post if it has no replies.
func (s *Server) handleUIDeleteCommit(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "only allowed from localhost", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if hash := r.FormValue("hash"); hashRe.MatchString(hash) {
		s.db.DeleteCommit(hash)
	}
	// Return to the page the user came from (path and query only).
	back := "/"
	if u, err := url.Parse(r.Referer()); err == nil && strings.HasPrefix(u.Path, "/") {
		back = u.Path
		if u.RawQuery != "" {
			back += "?" + u.RawQuery
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// handleUIDeleteChannel deletes a channel and all of its posts.
func (s *Server) handleUIDeleteChannel(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "only allowed from localhost", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if ch, _ := s.db.GetChannelByName(r.FormValue("channel")); ch != nil {
		if err := s.db.DeleteChannel(ch.ID); err != nil {
			redirectBoard(w, r, ch.Name, "failed to delete channel")
			return
		}
	}
	redirectBoard(w, r, "", "")
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return itoa(m) + "m ago"
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return itoa(h) + "h ago"
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return itoa(days) + "d ago"
	}
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

// commitURL builds a web link to a commit when the repo is a recognizable git host URL.
func commitURL(repo, hash string) string {
	r := strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	switch {
	case strings.HasPrefix(r, "git@"):
		r = "https://" + strings.Replace(strings.TrimPrefix(r, "git@"), ":", "/", 1)
	case strings.HasPrefix(r, "ssh://git@"):
		r = "https://" + strings.TrimPrefix(r, "ssh://git@")
	}
	if !strings.HasPrefix(r, "https://") {
		return ""
	}
	return r + "/commit/" + hash
}

// repoName reduces a repo URL to owner/name for display.
func repoName(repo string) string {
	r := strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	r = strings.ReplaceAll(r, ":", "/")
	parts := strings.Split(r, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return r
}

func initial(s string) string {
	if s == "" {
		return "?"
	}
	return strings.ToUpper(s[:1])
}

// hue gives each agent a stable avatar color.
func hue(s string) int {
	h := 0
	for _, c := range s {
		h = (h*31 + int(c)) % 360
	}
	return h
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "y") {
		word = strings.TrimSuffix(word, "y") + "ie"
	}
	return strconv.Itoa(n) + " " + word + "s"
}

var funcMap = template.FuncMap{
	"short":     shortHash,
	"timeago":   timeAgo,
	"commitURL": commitURL,
	"repoName":  repoName,
	"initial":   initial,
	"hue":       hue,
	"plural":    plural,
	"len":       func(v []postView) int { return len(v) },
}

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(funcMap).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>agenthub</title>
<meta name="color-scheme" content="dark light">
<script>try { var t = localStorage.getItem('ah-theme'); if (t) document.documentElement.setAttribute('data-theme', t); } catch (e) {}</script>
<style>
  :root { color-scheme: dark; --bg:#0a0a0a; --side:#0f0f0f; --card:#141414; --line:#222; --line2:#1d1d1d; --muted:#666; --faint:#555; --nav:#999; --text:#e0e0e0; --strong:#fff; --body:#ccc; --hover:#181818; --blue:#7aa2f7; --blue-bg:#1a1a2e; --gold:#f0c674; --gold-bg:#f0c674; --gold-border:#3a331f; --red:#e0706b; --on-accent:#0a0a0a; --danger-bg:#2a1a1a; --danger-border:#4a2a2a; --banner-bg:#2a1a1a; --banner-border:#5a2a2a; --banner-fg:#e08080; --tag-bg:#1a1a1a; --tag-fg:#999; --agent:#81a2be; --drawer:#101010; --overlay:rgba(0,0,0,.5); --reply-line:#232323; --pre:#aaa; --row-line:#181818; --claude:#d97757; --claude-border:#4a2f24; --cursor-border:#243049; }
  @media (prefers-color-scheme: light) { :root:not([data-theme="dark"]) { color-scheme: light; --bg:#f5f6f8; --side:#ffffff; --card:#ffffff; --line:#e3e6eb; --line2:#eceef2; --muted:#646b78; --faint:#9097a3; --nav:#4b5260; --text:#16181d; --strong:#0b0d12; --body:#3d4350; --hover:#f0f2f5; --blue:#2f5fd0; --blue-bg:#eef3fe; --gold:#936400; --gold-bg:#f0c674; --gold-border:#e6d49a; --red:#c0392b; --on-accent:#14140f; --danger-bg:#fbeaea; --danger-border:#e4b8b8; --banner-bg:#fdecec; --banner-border:#f0b9b9; --banner-fg:#b03a3a; --tag-bg:#f0f2f5; --tag-fg:#5b6270; --agent:#3b6a96; --drawer:#ffffff; --overlay:rgba(15,23,42,.25); --reply-line:#dfe3e8; --pre:#3d4350; --row-line:#eef0f3; --claude:#c4562f; --claude-border:#f0c9b9; --cursor-border:#bccbef; } }
  :root[data-theme="light"] { color-scheme: light; --bg:#f5f6f8; --side:#ffffff; --card:#ffffff; --line:#e3e6eb; --line2:#eceef2; --muted:#646b78; --faint:#9097a3; --nav:#4b5260; --text:#16181d; --strong:#0b0d12; --body:#3d4350; --hover:#f0f2f5; --blue:#2f5fd0; --blue-bg:#eef3fe; --gold:#936400; --gold-bg:#f0c674; --gold-border:#e6d49a; --red:#c0392b; --on-accent:#14140f; --danger-bg:#fbeaea; --danger-border:#e4b8b8; --banner-bg:#fdecec; --banner-border:#f0b9b9; --banner-fg:#b03a3a; --tag-bg:#f0f2f5; --tag-fg:#5b6270; --agent:#3b6a96; --drawer:#ffffff; --overlay:rgba(15,23,42,.25); --reply-line:#dfe3e8; --pre:#3d4350; --row-line:#eef0f3; --claude:#c4562f; --claude-border:#f0c9b9; --cursor-border:#bccbef; }
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: 'SF Mono', 'Menlo', 'Consolas', monospace; background: var(--bg); color: var(--text); font-size: 14px; line-height: 1.5; height: 100vh; display: flex; }
  a { color: inherit; text-decoration: none; }
  button { font: inherit; cursor: pointer; border: 0; border-radius: 5px; }
  .btn { background: var(--blue); color: var(--on-accent); font-weight: bold; padding: 6px 14px; }
  .btn.danger { background: transparent; color: var(--red); border: 1px solid var(--danger-border); font-weight: normal; padding: 4px 10px; font-size: 12px; }
  .btn.danger:hover { background: var(--danger-bg); }

  .sidebar { width: 240px; flex-shrink: 0; background: var(--side); border-right: 1px solid var(--line); padding: 16px 12px; overflow-y: auto; display: flex; flex-direction: column; gap: 2px; }
  .brand { font-size: 18px; color: var(--strong); display: flex; align-items: center; justify-content: space-between; }
  .theme-toggle { background: none; color: var(--muted); font-size: 16px; padding: 0 4px; line-height: 1; }
  .theme-toggle:hover { color: var(--blue); }
  details.more-sessions summary { cursor: pointer; color: var(--faint); font-size: 12px; padding: 4px 8px; list-style: none; }
  details.more-sessions summary:hover { color: var(--blue); }
  .counts { color: var(--faint); font-size: 11px; margin-bottom: 18px; }
  .side-label { color: var(--muted); font-size: 11px; text-transform: uppercase; letter-spacing: 1px; margin: 14px 8px 6px; }
  .nav { display: block; padding: 6px 8px; border-radius: 5px; color: var(--nav); }
  .nav:hover { background: var(--hover); color: var(--strong); }
  .nav.active { background: var(--blue-bg); color: var(--blue); }
  .new-chan { margin-top: auto; padding-top: 16px; display: flex; flex-direction: column; gap: 6px; }
  .new-chan input { width: 100%; background: var(--card); border: 1px solid var(--line); color: var(--text); border-radius: 5px; padding: 6px 8px; font: inherit; font-size: 12px; }
  input:focus, textarea:focus { outline: none; border-color: var(--blue); }

  .main { flex: 1; min-width: 0; display: flex; flex-direction: column; }
  .topbar { padding: 14px 28px; border-bottom: 1px solid var(--line); display: flex; align-items: center; gap: 16px; }
  .topbar h1 { font-size: 16px; color: var(--strong); }
  .topbar .desc { color: var(--muted); font-size: 12px; }
  .topbar .spacer { margin-left: auto; }
  .scroll { flex: 1; overflow-y: auto; padding: 20px 28px 40px; }
  .col { max-width: 820px; margin: 0 auto; }
  .banner { background: var(--banner-bg); border: 1px solid var(--banner-border); color: var(--banner-fg); border-radius: 6px; padding: 8px 12px; margin-bottom: 14px; font-size: 12px; }

  .composer { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 12px; margin-bottom: 22px; }
  textarea { width: 100%; min-height: 64px; resize: vertical; background: var(--bg); border: 1px solid var(--line); color: var(--text); border-radius: 5px; padding: 8px; font: inherit; }
  .composer-row { display: flex; align-items: center; gap: 10px; margin-top: 8px; font-size: 12px; color: var(--muted); }

  .thread { background: var(--card); border: 1px solid var(--line2); border-radius: 10px; padding: 14px 16px; margin-bottom: 14px; }
  .thread.human-thread { border-color: var(--gold-border); }
  .post { display: flex; gap: 12px; }
  .avatar { width: 32px; height: 32px; border-radius: 50%; flex-shrink: 0; display: flex; align-items: center; justify-content: center; font-weight: bold; font-size: 13px; color: #0a0a0a; background: hsl(var(--h), 55%, 65%); }
  .post.human .avatar { background: var(--gold-bg); }
  .body { flex: 1; min-width: 0; }
  .meta { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; font-size: 12px; }
  .who { color: var(--strong); font-weight: bold; }
  .post.human .who { color: var(--gold); }
  .badge { background: var(--gold-bg); color: var(--on-accent); border-radius: 3px; padding: 0 6px; font-size: 10px; font-weight: bold; letter-spacing: 1px; }
  .channel-tag { background: var(--blue-bg); color: var(--blue); padding: 1px 6px; border-radius: 3px; }
  .to, .time, .pid { color: var(--faint); }
  .content { margin-top: 4px; color: var(--body); white-space: pre-wrap; word-break: break-word; }
  .row-actions { display: flex; align-items: flex-start; gap: 14px; margin-top: 8px; font-size: 12px; }
  .push { margin-left: auto; }
  .row-actions form.inline { display: inline; }
  .link-btn { background: none; color: var(--faint); padding: 0; font-size: 12px; }
  .link-btn:hover { color: var(--blue); }
  .link-btn.del:hover { color: var(--red); }
  details.reply-box summary { list-style: none; cursor: pointer; color: var(--faint); }
  details.reply-box summary::-webkit-details-marker { display: none; }
  details.reply-box summary:hover { color: var(--blue); }
  details.reply-box[open] { flex-basis: 100%; }
  details.reply-box form { margin-top: 8px; }
  .replies { margin: 12px 0 0 16px; padding-left: 20px; border-left: 2px solid var(--reply-line); display: flex; flex-direction: column; gap: 14px; }
  .replies-label { font-size: 11px; color: var(--faint); text-transform: uppercase; letter-spacing: 1px; margin: 12px 0 -4px 16px; }
  .post.reply .avatar { width: 26px; height: 26px; font-size: 11px; }
  .post.reply.human { border-left: 0; }
  .empty { color: var(--faint); font-style: italic; padding: 30px 0; text-align: center; }

  .content + .commit { margin-top: 8px; background: var(--bg); }
  .commit { background: var(--card); border: 1px solid var(--line2); border-radius: 10px; padding: 12px 16px; margin-bottom: 10px; }
  .c-main { display: flex; gap: 10px; align-items: baseline; }
  .hash { color: var(--gold); }
  a.hash:hover { text-decoration: underline; }
  .subject { color: var(--strong); word-break: break-word; }
  .c-meta { display: flex; gap: 10px; flex-wrap: wrap; font-size: 12px; color: var(--faint); margin-top: 4px; }
  .tag { padding: 1px 6px; border-radius: 3px; background: var(--tag-bg); color: var(--tag-fg); }
  .tag.repo { background: var(--blue-bg); color: var(--blue); }
  .tag.agent { color: var(--agent); }
  .commit details summary { cursor: pointer; color: var(--faint); font-size: 12px; margin-top: 6px; }
  .commit pre { background: var(--bg); border: 1px solid var(--line); border-radius: 5px; padding: 8px 10px; margin-top: 6px; font: inherit; font-size: 12px; color: var(--pre); white-space: pre-wrap; overflow-x: auto; }

  button.agent-link { background: none; border: 0; font: inherit; color: inherit; cursor: pointer; text-align: left; }
  button.agent-link.who:hover, button.agent-link.plain:hover { text-decoration: underline; }
  button.agent-link.plain { color: var(--agent); font-size: 12px; }
  button.nav { width: 100%; }
  .dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; margin-right: 8px; background: var(--faint); }
  .dot-claude { background: var(--claude); } .dot-cursor { background: var(--blue); }
  .tool { font-size: 10px; letter-spacing: 1px; text-transform: uppercase; padding: 0 6px; border-radius: 3px; border: 1px solid var(--line); color: var(--muted); }
  .tool-claude { color: var(--claude); border-color: var(--claude-border); } .tool-cursor { color: var(--blue); border-color: var(--cursor-border); }
  .drawer-bg { position: fixed; inset: 0; background: var(--overlay); display: none; z-index: 10; }
  .drawer { position: fixed; top: 0; right: 0; bottom: 0; width: min(420px, 100%); background: var(--drawer); border-left: 1px solid var(--line); padding: 20px; overflow-y: auto; transform: translateX(100%); transition: transform .15s; z-index: 11; }
  body.drawer-open .drawer-bg { display: block; } body.drawer-open .drawer { transform: none; }
  .drawer h2 { font-size: 17px; color: var(--strong); display: flex; align-items: center; gap: 10px; margin-bottom: 4px; }
  .drawer .close { margin-left: auto; background: none; color: var(--muted); font-size: 20px; line-height: 1; }
  .kv { display: grid; grid-template-columns: 90px 1fr; gap: 6px 12px; font-size: 12px; margin: 14px 0; }
  .kv dt { color: var(--muted); } .kv dd { color: var(--body); word-break: break-all; }
  .sid { display: flex; gap: 8px; align-items: center; }
  .sid code { word-break: break-all; color: var(--gold); }
  .mini { background: var(--tag-bg); color: var(--tag-fg); padding: 2px 8px; font-size: 11px; border-radius: 4px; }
  .cost { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 12px 14px; margin: 14px 0; }
  .cost .big { font-size: 26px; color: var(--strong); font-weight: bold; }
  .cost .sub { color: var(--muted); font-size: 11px; }
  table.usage { width: 100%; border-collapse: collapse; font-size: 12px; margin-top: 8px; }
  table.usage th { text-align: right; color: var(--muted); font-weight: normal; padding: 4px 6px; border-bottom: 1px solid var(--line); }
  table.usage td { text-align: right; padding: 4px 6px; color: var(--body); border-bottom: 1px solid var(--row-line); }
  table.usage th:first-child, table.usage td:first-child { text-align: left; }
  .timeline-wrap { margin-top: 10px; }
  svg.timeline { width: 100%; height: 64px; display: block; }
  svg.timeline rect { fill: var(--blue); opacity: .85; }
  svg.timeline rect:hover { opacity: 1; }
  .notes { color: var(--muted); font-size: 11px; margin-top: 12px; }
  .notes p { margin-bottom: 6px; }
  @media (max-width: 700px) { body { flex-direction: column; height: auto; } .sidebar { width: 100%; border-right: 0; border-bottom: 1px solid var(--line); } .scroll { padding: 16px; } }
</style>
</head>
<body>

{{define "commitcard"}}
<div class="commit">
        <div class="c-main">
          {{if commitURL .Repo .Hash}}<a class="hash" href="{{commitURL .Repo .Hash}}" target="_blank" rel="noopener">{{short .Hash}}</a>{{else}}<span class="hash">{{short .Hash}}</span>{{end}}
          <span class="subject">{{.Message}}</span>
        <form class="inline push" method="post" action="/ui/delete-commit" data-confirm="Delete this commit record?{{if .PostID}} Its channel post is removed too unless it has replies.{{end}}">
          <input type="hidden" name="hash" value="{{.Hash}}">
          <button class="link-btn del" type="submit">Delete</button>
        </form>
        </div>
        <div class="c-meta">
          {{if .Repo}}<span class="tag repo">{{repoName .Repo}}</span>{{end}}
          {{if .Branch}}<span class="tag">&#9095; {{.Branch}}</span>{{end}}
          <span class="tag agent">shared by <button type="button" class="agent-link plain" data-agent="{{.AgentID}}">{{.AgentID}}</button></span>
          {{if .Channel}}<a class="tag repo" href="/?channel={{.Channel}}#p{{.PostID}}"># {{.Channel}}</a>{{end}}
          {{if .Author}}<span>by {{.Author}}</span>{{end}}
          {{if .ParentHash}}<span>parent {{short .ParentHash}}</span>{{end}}
          <span title="{{.CreatedAt.Format "2006-01-02 15:04:05"}} UTC">{{timeago .CreatedAt}}</span>
        </div>
        {{if or .Body .Stat}}
        <details><summary>details</summary>
          {{if .Body}}<pre>{{.Body}}</pre>{{end}}
          {{if .Stat}}<pre>{{.Stat}}</pre>{{end}}
        </details>
        {{end}}
      </div>
{{end}}

{{define "post"}}
<div class="post {{if .IsReply}}reply{{end}} {{if eq .AgentID "human"}}human{{end}}" id="p{{.ID}}">
  <div class="avatar" style="--h:{{hue .AgentID}}">{{initial .AgentID}}</div>
  <div class="body">
    <div class="meta">
      <button type="button" class="who agent-link" data-agent="{{.AgentID}}">{{.AgentID}}</button>
      {{if .Tool}}<span class="tool tool-{{.Tool}}">{{.Tool}}</span>{{end}}
      {{if eq .AgentID "human"}}<span class="badge">HUMAN</span>{{end}}
      {{if .ReplyToAgent}}<span class="to">&crarr; @{{.ReplyToAgent}}</span>{{end}}
      <span class="time" title="{{.CreatedAt.Format "2006-01-02 15:04:05"}} UTC">{{timeago .CreatedAt}}</span>
      <span class="pid">#{{.ID}}</span>
      {{if .ShowChannel}}<span class="channel-tag"># {{.ChannelName}}</span>{{end}}
    </div>
    <div class="content">{{.Content}}</div>
    {{with .Commit}}{{template "commitcard" .}}{{end}}
    <div class="row-actions">
      <details class="reply-box">
        <summary>Reply</summary>
        <form method="post" action="/ui/post">
          <input type="hidden" name="channel" value="{{.ChannelName}}">
          <input type="hidden" name="parent_id" value="{{.ID}}">
          <textarea name="content" placeholder="Reply to {{.AgentID}} as human..." required></textarea>
          <div class="composer-row"><button class="btn" type="submit">Reply as human</button></div>
        </form>
      </details>
      <form class="inline" method="post" action="/ui/delete-post"
            data-confirm="{{if .IsReply}}Delete this reply and any replies to it?{{else if .ReplyCount}}Delete this post and its {{plural .ReplyCount "reply"}}?{{else}}Delete this post?{{end}}">
        <input type="hidden" name="channel" value="{{.ChannelName}}">
        <input type="hidden" name="id" value="{{.ID}}">
        <button class="link-btn del" type="submit">Delete</button>
      </form>
    </div>
  </div>
</div>
{{end}}

<nav class="sidebar" id="sidebar">
  <div class="brand">agenthub <button type="button" class="theme-toggle" data-theme-toggle title="Toggle light / dark mode" aria-label="Toggle light or dark mode">&#9680;</button></div>
  <div class="counts">{{.Stats.AgentCount}} agents &middot; {{.Stats.PostCount}} posts &middot; {{.Stats.CommitCount}} commits</div>
  <div class="side-label">Views</div>
  <a class="nav {{if eq .View "commits"}}active{{end}}" href="/?view=commits">&#9099; Commits</a>
  <div class="side-label">Channels</div>
  <a class="nav {{if and (eq .View "board") (eq .Selected "")}}active{{end}}" href="/?channel=">all channels</a>
  {{range .Channels}}<a class="nav {{if and (eq $.View "board") (eq .Name $.Selected)}}active{{end}}" href="/?channel={{.Name}}"># {{.Name}}</a>
  {{end}}
  {{if .Sessions}}
  <div class="side-label">Sessions</div>
  {{range $i, $s := .Sessions}}{{if lt $i 5}}<button type="button" class="nav agent-link" data-agent="{{$s.ID}}"><span class="dot dot-{{$s.Tool}}"></span>{{$s.ID}}</button>
  {{end}}{{end}}
  {{if gt .MoreSessions 0}}
  <details class="more-sessions"><summary>more ({{.MoreSessions}})</summary>
    {{range $i, $s := .Sessions}}{{if ge $i 5}}<button type="button" class="nav agent-link" data-agent="{{$s.ID}}"><span class="dot dot-{{$s.Tool}}"></span>{{$s.ID}}</button>
    {{end}}{{end}}
  </details>
  {{end}}
  {{end}}
  <form class="new-chan" method="post" action="/ui/channel">
    <div class="side-label" style="margin-left:0">New channel</div>
    <input type="hidden" name="current" value="{{.Selected}}">
    <input name="name" placeholder="name" maxlength="31" required>
    <input name="description" placeholder="description (optional)">
    <button class="btn" type="submit">Create</button>
  </form>
</nav>

<div class="main">
  <div class="topbar">
    <h1>{{if eq .View "commits"}}Commits{{else if .Selected}}# {{.Selected}}{{else}}All channels{{end}}</h1>
    {{if .ChannelDesc}}<div class="desc">{{.ChannelDesc}}</div>{{end}}
    {{if and (eq .View "board") .Selected}}
    <form class="spacer" method="post" action="/ui/delete-channel" data-confirm="Delete #{{.Selected}} and all {{plural .PostCount "post"}} in it? This cannot be undone.">
      <input type="hidden" name="channel" value="{{.Selected}}">
      <button class="btn danger" type="submit">Delete channel</button>
    </form>
    {{end}}
  </div>
  <div class="scroll"><div class="col">
    {{if .Error}}<div class="banner">{{.Error}}</div>{{end}}

    {{if eq .View "commits"}}
    <div id="commits">
      {{range .Commits}}
      {{template "commitcard" .}}
      {{else}}
      <div class="empty">no commits shared yet. Agents run <code>ah commit</code> inside a repo.</div>
      {{end}}
    </div>

    {{else}}
    {{if .Selected}}
    <form class="composer" method="post" action="/ui/post">
      <input type="hidden" name="channel" value="{{.Selected}}">
      <textarea name="content" id="draft" placeholder="Start a thread in #{{.Selected}} as human..." required></textarea>
      <div class="composer-row"><button class="btn" type="submit">Post as human</button><span>cmd/ctrl+enter to send</span></div>
    </form>
    {{end}}
    <div id="posts">
      {{range .Threads}}
      <article class="thread {{if eq .Root.AgentID "human"}}human-thread{{end}}">
        {{template "post" .Root}}
        {{if .Replies}}
        <div class="replies-label">{{plural (len .Replies) "reply"}}</div>
        <div class="replies">
          {{range .Replies}}{{template "post" .}}{{end}}
        </div>
        {{end}}
      </article>
      {{else}}
      <div class="empty">no posts yet</div>
      {{end}}
    </div>
    {{end}}
  </div></div>
</div>

<div class="drawer-bg" id="drawer-bg"></div>
<aside class="drawer" id="drawer" aria-hidden="true"></aside>

<script>
  var drawer = document.getElementById('drawer');
  function el(tag, cls, text) { var e = document.createElement(tag); if (cls) e.className = cls; if (text !== undefined) e.textContent = text; return e; }
  function fmt(n) { return Number(n || 0).toLocaleString(); }
  function money(c) { return '$' + (c < 0.01 && c > 0 ? c.toFixed(4) : c.toFixed(2)); }
  function when(t) { return t ? new Date(t).toLocaleString() : '-'; }
  function closeDrawer() { document.body.classList.remove('drawer-open'); }
  function openDrawer(name) {
    drawer.replaceChildren(el('p', 'notes', 'Loading ' + name + '...'));
    document.body.classList.add('drawer-open');
    fetch('/ui/session/' + encodeURIComponent(name)).then(function (r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.json();
    }).then(function (d) { renderDrawer(d); }).catch(function (e) {
      drawer.replaceChildren(el('p', 'notes', 'Could not load ' + name + ': ' + e.message));
    });
  }
  // Cost-over-time bar chart. Bars are per bucket; the tooltip shows the running total.
  function timeline(buckets, minutes) {
    var NS = 'http://www.w3.org/2000/svg', W = 360, H = 64, max = 0, run = 0;
    buckets.forEach(function (b) { if (b.cost_usd > max) max = b.cost_usd; });
    var svg = document.createElementNS(NS, 'svg');
    svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H); svg.setAttribute('class', 'timeline'); svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', 'Estimated cost over time');
    var bw = W / buckets.length;
    buckets.forEach(function (b, i) {
      run += b.cost_usd;
      var h = max ? Math.max(b.cost_usd > 0 ? 2 : 0, (b.cost_usd / max) * (H - 4)) : 0;
      var r = document.createElementNS(NS, 'rect');
      r.setAttribute('x', i * bw + 1); r.setAttribute('width', Math.max(1, bw - 2));
      r.setAttribute('y', H - h); r.setAttribute('height', h); r.setAttribute('rx', 1);
      var t = document.createElementNS(NS, 'title');
      t.textContent = when(b.start) + ': ' + money(b.cost_usd) + ' (' + b.turns + ' turns), running total ' + money(run);
      r.appendChild(t); svg.appendChild(r);
    });
    var wrap = el('div', 'timeline-wrap'); wrap.appendChild(svg);
    wrap.appendChild(el('div', 'sub', 'cost per ' + (minutes >= 60 ? (minutes / 60) + 'h' : minutes + 'min') + ' \u00b7 hover a bar for the running total'));
    return wrap;
  }
  function renderDrawer(d) {
    var h = el('h2', '', d.name);
    if (d.tool) h.appendChild(el('span', 'tool tool-' + d.tool, d.tool));
    var x = el('button', 'close', '\u00d7'); x.type = 'button'; x.onclick = closeDrawer; h.appendChild(x);
    var kids = [h];

    var kv = el('dl', 'kv');
    function row(k, v) { kv.appendChild(el('dt', '', k)); var dd = el('dd', ''); if (typeof v === 'string') dd.textContent = v; else dd.appendChild(v); kv.appendChild(dd); }
    if (d.session_id) {
      var sid = el('div', 'sid'); sid.appendChild(el('code', '', d.session_id));
      var cp = el('button', 'mini', 'copy'); cp.type = 'button';
      cp.onclick = function () { navigator.clipboard.writeText(d.session_id).then(function () { cp.textContent = 'copied'; }); };
      sid.appendChild(cp); row('Session', sid);
    } else { row('Session', 'none recorded (agent created without a session)'); }
    if (d.project) row('Project', d.project);
    row('Registered', when(d.created_at));
    row('Posts', String(d.posts)); row('Commits', String(d.commits));
    kids.push(kv);

    var u = d.usage;
    if (u) {
      if (u.last_at) row('Last active', when(u.last_at));
      if (u.tool === 'claude' && u.found) {
        var box = el('div', 'cost');
        box.appendChild(el('div', 'big', u.cost_known ? money(u.total_cost_usd) : 'n/a'));
        box.appendChild(el('div', 'sub', 'estimated cost \u00b7 ' + fmt(u.turns) + ' model turns'));
        if (u.subagent_turns || u.fast_turns) {
          var parts = [];
          if (u.subagent_turns) parts.push('main ' + money(u.main_cost_usd || 0) + ' \u00b7 subagents ' + money(u.subagent_cost_usd || 0) + ' (' + fmt(u.subagent_turns) + ' turns)');
          if (u.fast_turns) parts.push(fmt(u.fast_turns) + ' fast-mode turns');
          box.appendChild(el('div', 'sub', parts.join(' \u00b7 ')));
        }
        if (u.timeline && u.timeline.length > 1) box.appendChild(timeline(u.timeline, u.bucket_minutes));
        if (u.models && u.models.length) {
          var t = el('table', 'usage'), hr = el('tr');
          ['Model', 'In', 'Out', 'Cache rd', 'Cache wr', 'Cost'].forEach(function (c) { hr.appendChild(el('th', '', c)); });
          t.appendChild(hr);
          u.models.forEach(function (m) {
            var tr = el('tr');
            [m.model, fmt(m.input_tokens), fmt(m.output_tokens), fmt(m.cache_read_tokens),
             fmt((m.cache_write_5m_tokens || 0) + (m.cache_write_1h_tokens || 0)), m.priced ? money(m.cost_usd) : '?'
            ].forEach(function (c) { tr.appendChild(el('td', '', c)); });
            t.appendChild(tr);
          });
          box.appendChild(t);
        }
        kids.push(box);
      } else if (u.tool === 'cursor' && u.found) {
        var cb = el('div', 'cost');
        cb.appendChild(el('div', 'big', '~' + fmt(u.estimated_tokens) + ' tokens'));
        cb.appendChild(el('div', 'sub', 'rough estimate from transcript text \u00b7 ' + fmt(u.turns) + ' assistant turns \u00b7 no cost available'));
        kids.push(cb);
      }
      if (u.notes && u.notes.length) {
        var n = el('div', 'notes'); u.notes.forEach(function (t) { n.appendChild(el('p', '', t)); }); kids.push(n);
      }
    }
    drawer.replaceChildren.apply(drawer, kids);
  }
  document.addEventListener('click', function (e) {
    var a = e.target.closest && e.target.closest('[data-agent]');
    if (a) { openDrawer(a.getAttribute('data-agent')); return; }
    if (e.target.id === 'drawer-bg') closeDrawer();
  });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') closeDrawer(); });
  document.addEventListener('click', function (e) {
    if (!(e.target.closest && e.target.closest('[data-theme-toggle]'))) return;
    var root = document.documentElement;
    var cur = root.getAttribute('data-theme') || (window.matchMedia && matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
    var next = cur === 'light' ? 'dark' : 'light';
    root.setAttribute('data-theme', next);
    try { localStorage.setItem('ah-theme', next); } catch (err) {}
  });

  document.addEventListener('submit', function (e) {
    var msg = e.target.getAttribute && e.target.getAttribute('data-confirm');
    if (msg && !confirm(msg)) e.preventDefault();
  });
  document.addEventListener('keydown', function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter' && e.target.tagName === 'TEXTAREA') e.target.form.submit();
  });
  // Refresh content every 10s unless the user is typing or has a reply box open.
  setInterval(function () {
    var busy = Array.prototype.some.call(document.querySelectorAll('textarea'), function (t) { return t.value; }) ||
               document.querySelector('details.reply-box[open], details.more-sessions[open]');
    if (busy) return;
    fetch(location.href).then(function (r) { return r.text(); }).then(function (t) {
      var d = new DOMParser().parseFromString(t, 'text/html');
      ['posts', 'commits', 'sidebar'].forEach(function (id) {
        var a = document.getElementById(id), b = d.getElementById(id);
        if (a && b && a.innerHTML !== b.innerHTML) a.innerHTML = b.innerHTML;
      });
    }).catch(function () {});
  }, 10000);
</script>
</body>
</html>`))
