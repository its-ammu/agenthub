package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func get(t *testing.T, s *Server, path string) string {
	t.Helper()
	w := do(t, s, "GET", path, "", "")
	expect(t, w, http.StatusOK)
	return w.Body.String()
}

// uiPost sends a dashboard form post from loopback (or, with remote set, from elsewhere).
func uiPost(t *testing.T, s *Server, path string, form url.Values, remote string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "127.0.0.1:5555"
	if remote != "" {
		req.RemoteAddr = remote
	}
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

func seedBoard(t *testing.T, s *Server) (claude, cursor string) {
	t.Helper()
	claude = register(t, s, "neon-axolotl")
	cursor = register(t, s, "quiet-heron")
	for _, name := range []string{"dev", "ops"} {
		expect(t, do(t, s, "POST", "/api/channels", claude, `{"name":"`+name+`"}`), http.StatusCreated)
	}
	return
}

func TestArchiveChannelAPI(t *testing.T) {
	s := newTestServer(t)
	key, _ := seedBoard(t, s)

	expect(t, do(t, s, "POST", "/api/channels/dev/archive", "", ""), http.StatusUnauthorized)
	expect(t, do(t, s, "POST", "/api/channels/nope/archive", key, ""), http.StatusNotFound)
	expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"before"}`), http.StatusCreated)

	w := do(t, s, "POST", "/api/channels/dev/archive", key, "")
	expect(t, w, http.StatusOK)

	// Hidden from the default list, present with ?archived=1.
	if body := do(t, s, "GET", "/api/channels", key, "").Body.String(); strings.Contains(body, `"dev"`) {
		t.Errorf("archived channel still listed: %s", body)
	}
	if body := do(t, s, "GET", "/api/channels?archived=1", key, "").Body.String(); !strings.Contains(body, `"dev"`) || !strings.Contains(body, `"archived":true`) {
		t.Errorf("archived channel missing from ?archived=1: %s", body)
	}

	// Read-only: reading works, posting and commit-posting are refused.
	expect(t, do(t, s, "GET", "/api/channels/dev/posts", key, ""), http.StatusOK)
	expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"after"}`), http.StatusConflict)
	expect(t, do(t, s, "POST", "/api/commits", key, `{"hash":"abcdef1","message":"m","channel":"dev"}`), http.StatusConflict)

	// Reversible.
	expect(t, do(t, s, "POST", "/api/channels/dev/unarchive", key, ""), http.StatusOK)
	expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"after"}`), http.StatusCreated)
	if body := do(t, s, "GET", "/api/channels", key, "").Body.String(); !strings.Contains(body, `"dev"`) {
		t.Errorf("restored channel not listed: %s", body)
	}
}

func TestArchiveChannelDashboard(t *testing.T) {
	s := newTestServer(t)
	key, _ := seedBoard(t, s)
	expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"hello from dev"}`), http.StatusCreated)
	expect(t, do(t, s, "POST", "/api/channels/ops/posts", key, `{"content":"hello from ops"}`), http.StatusCreated)

	// Only localhost may archive.
	w := uiPost(t, s, "/ui/archive-channel", url.Values{"channel": {"dev"}}, "203.0.113.9:4000")
	if w.Code != http.StatusForbidden {
		t.Fatalf("remote archive status = %d", w.Code)
	}
	if ch, _ := s.db.GetChannelByName("dev"); ch.Archived {
		t.Fatal("remote caller archived a channel")
	}

	w = uiPost(t, s, "/ui/archive-channel", url.Values{"channel": {"dev"}}, "")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("archive status = %d", w.Code)
	}

	// The sidebar tucks it into "archived", and the all-channels view drops its posts.
	all := get(t, s, "/?channel=")
	if !strings.Contains(all, "archived (1)") {
		t.Error("archived section missing from sidebar")
	}
	if strings.Contains(all, "hello from dev") || !strings.Contains(all, "hello from ops") {
		t.Error("all-channels view should hide archived posts and keep the rest")
	}

	// Viewing the archived channel shows its posts, a restore button, and no composer.
	page := get(t, s, "/?channel=dev")
	if !strings.Contains(page, "hello from dev") || !strings.Contains(page, "Restore channel") || strings.Contains(page, "Start a thread in #dev") {
		t.Error("archived channel page is wrong")
	}

	// Posting from the UI is refused.
	uiPost(t, s, "/ui/post", url.Values{"channel": {"dev"}, "content": {"nope"}}, "")
	if strings.Contains(get(t, s, "/?channel=dev"), "nope") {
		t.Error("posted to an archived channel from the UI")
	}

	// Restore.
	uiPost(t, s, "/ui/archive-channel", url.Values{"channel": {"dev"}, "restore": {"1"}}, "")
	if ch, _ := s.db.GetChannelByName("dev"); ch.Archived {
		t.Error("channel still archived after restore")
	}
}

func TestDashboardSearchAndFilters(t *testing.T) {
	s := newTestServer(t)
	claude, cursor := seedBoard(t, s)
	post := func(key, content string) {
		expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"`+content+`"}`), http.StatusCreated)
	}
	post(claude, "Rounding bug lives in tax.ts")
	post(cursor, "Lint is green on main")
	// A session registered as a tool, so the tool filter has something to match.
	expect(t, do(t, s, "POST", "/api/sessions", "", `{"tool":"cursor","session_id":"cursor-session-001"}`), http.StatusCreated)

	page := get(t, s, "/?channel=dev&q=rounding")
	if !strings.Contains(page, "Rounding bug") || strings.Contains(page, "Lint is green") {
		t.Error("search did not narrow the board")
	}
	if !strings.Contains(page, "1 thread match") && !strings.Contains(page, "1 thread") {
		t.Errorf("missing match summary")
	}

	page = get(t, s, "/?channel=dev&agent=quiet-heron")
	if !strings.Contains(page, "Lint is green") || strings.Contains(page, "Rounding bug") {
		t.Error("agent filter did not narrow the board")
	}

	page = get(t, s, "/?channel=dev&q=zzz")
	if !strings.Contains(page, "nothing matches") {
		t.Error("empty search result message missing")
	}

	// Filter inputs keep their values so the form reflects the current search.
	if page := get(t, s, "/?channel=dev&q=rounding"); !strings.Contains(page, `value="rounding"`) {
		t.Error("search box did not keep its value")
	}
	// Search terms are escaped, not injected.
	if page := get(t, s, "/?channel=dev&q="+url.QueryEscape(`"><script>alert(1)</script>`)); strings.Contains(page, "<script>alert(1)") {
		t.Error("search term was injected into the page")
	}
}

func TestDashboardLoadOlder(t *testing.T) {
	s := newTestServer(t)
	key, _ := seedBoard(t, s)
	for i := 0; i < 30; i++ {
		expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"thread number `+strconv.Itoa(i)+`"}`), http.StatusCreated)
	}
	page := get(t, s, "/?channel=dev")
	if !strings.Contains(page, "Load older (5 more threads)") {
		t.Fatalf("expected a load-older link for 5 more threads")
	}
	if !strings.Contains(page, "thread number 29") || strings.Contains(page, "thread number 0<") {
		t.Error("first page should show the newest 25 threads only")
	}
	full := get(t, s, "/?channel=dev&n=50")
	if strings.Contains(full, "Load older") || !strings.Contains(full, "thread number 0<") {
		t.Error("n=50 should show every thread and no load-older link")
	}
}

func TestDashboardRendersMarkdown(t *testing.T) {
	s := newTestServer(t)
	key, _ := seedBoard(t, s)
	body := `{"content":"Fix is in **tax.ts**\n\n- run ` + "`make test`" + `\n- see https://example.com/pr/1\n\n<script>alert(1)</script>"}`
	expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, body), http.StatusCreated)
	page := get(t, s, "/?channel=dev")
	for _, want := range []string{"<strong>tax.ts</strong>", "<code>make test</code>", `<a href="https://example.com/pr/1"`, "<li>", "&lt;script&gt;alert(1)&lt;/script&gt;"} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	if strings.Contains(page, "<script>alert(1)") {
		t.Error("script from a post was not escaped")
	}
}
