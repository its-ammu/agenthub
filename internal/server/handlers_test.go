package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agenthub/internal/db"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d, "admin-secret", Config{MaxCommitsPerHour: 100, MaxPostsPerHour: 100})
}

// do sends a request through the mux. remote defaults to loopback.
func do(t *testing.T, s *Server, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:5555"
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("bad json %q: %v", w.Body.String(), err)
	}
	return v
}

func expect(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", w.Code, status, w.Body.String())
	}
}

// register creates an agent and returns its api key.
func register(t *testing.T, s *Server, id string) string {
	t.Helper()
	w := do(t, s, "POST", "/api/register", "", `{"id":"`+id+`"}`)
	expect(t, w, http.StatusCreated)
	return decode[map[string]string](t, w)["api_key"]
}

func TestHealth(t *testing.T) {
	s := newTestServer(t)
	expect(t, do(t, s, "GET", "/api/health", "", ""), http.StatusOK)
}

func TestAuthRequired(t *testing.T) {
	s := newTestServer(t)
	expect(t, do(t, s, "GET", "/api/channels", "", ""), http.StatusUnauthorized)
	expect(t, do(t, s, "GET", "/api/channels", "bogus", ""), http.StatusUnauthorized)
}

func TestRegister(t *testing.T) {
	s := newTestServer(t)
	key := register(t, s, "agent-one")
	if key == "" {
		t.Fatal("no api key returned")
	}
	expect(t, do(t, s, "POST", "/api/register", "", `{"id":"agent-one"}`), http.StatusConflict)
	expect(t, do(t, s, "POST", "/api/register", "", `{"id":"bad id!"}`), http.StatusBadRequest)
	expect(t, do(t, s, "GET", "/api/channels", key, ""), http.StatusOK)
}

func TestAdminCreateAgent(t *testing.T) {
	s := newTestServer(t)
	expect(t, do(t, s, "POST", "/api/admin/agents", "wrong", `{"id":"x1"}`), http.StatusUnauthorized)
	expect(t, do(t, s, "POST", "/api/admin/agents", "admin-secret", `{"id":"x1"}`), http.StatusCreated)
	expect(t, do(t, s, "POST", "/api/admin/agents", "admin-secret", `{"id":"x1"}`), http.StatusConflict)
}

func TestChannels(t *testing.T) {
	s := newTestServer(t)
	key := register(t, s, "a1")

	expect(t, do(t, s, "POST", "/api/channels", key, `{"name":"dev","description":"d"}`), http.StatusCreated)
	expect(t, do(t, s, "POST", "/api/channels", key, `{"name":"dev"}`), http.StatusConflict)
	for _, bad := range []string{"Dev", "has space", "", strings.Repeat("a", 32)} {
		body, _ := json.Marshal(map[string]string{"name": bad})
		expect(t, do(t, s, "POST", "/api/channels", key, string(body)), http.StatusBadRequest)
	}

	chans := decode[[]db.Channel](t, do(t, s, "GET", "/api/channels", key, ""))
	found := false
	for _, c := range chans {
		if c.Name == "dev" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dev channel not listed: %+v", chans)
	}
}

func TestPostsAndReplies(t *testing.T) {
	s := newTestServer(t)
	key := register(t, s, "a1")
	expect(t, do(t, s, "POST", "/api/channels", key, `{"name":"dev"}`), http.StatusCreated)
	expect(t, do(t, s, "POST", "/api/channels", key, `{"name":"other"}`), http.StatusCreated)

	expect(t, do(t, s, "POST", "/api/channels/nope/posts", key, `{"content":"x"}`), http.StatusNotFound)
	expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":""}`), http.StatusBadRequest)
	expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"`+strings.Repeat("a", 32*1024+1)+`"}`), http.StatusBadRequest)

	w := do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"root"}`)
	expect(t, w, http.StatusCreated)
	root := decode[db.Post](t, w)
	if root.AgentID != "a1" || root.Content != "root" {
		t.Fatalf("unexpected post %+v", root)
	}

	// Reply in the same channel works; bad or cross-channel parent is rejected.
	reply := `{"content":"re","parent_id":` + itoa(root.ID) + `}`
	expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, reply), http.StatusCreated)
	expect(t, do(t, s, "POST", "/api/channels/other/posts", key, reply), http.StatusBadRequest)
	expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"x","parent_id":9999}`), http.StatusBadRequest)

	replies := decode[[]db.Post](t, do(t, s, "GET", "/api/posts/"+itoa(root.ID)+"/replies", key, ""))
	if len(replies) != 1 || replies[0].Content != "re" {
		t.Fatalf("replies = %+v", replies)
	}
	expect(t, do(t, s, "GET", "/api/posts/9999", key, ""), http.StatusNotFound)

	posts := decode[[]db.Post](t, do(t, s, "GET", "/api/channels/dev/posts?limit=1", key, ""))
	if len(posts) != 1 {
		t.Fatalf("limit=1 returned %d posts", len(posts))
	}
}

func TestPostRateLimit(t *testing.T) {
	s := newTestServer(t)
	s.config.MaxPostsPerHour = 2
	key := register(t, s, "a1")
	expect(t, do(t, s, "POST", "/api/channels", key, `{"name":"dev"}`), http.StatusCreated)
	for i := 0; i < 2; i++ {
		expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"x"}`), http.StatusCreated)
	}
	expect(t, do(t, s, "POST", "/api/channels/dev/posts", key, `{"content":"x"}`), http.StatusTooManyRequests)
}

func TestShareCommit(t *testing.T) {
	s := newTestServer(t)
	key := register(t, s, "a1")
	expect(t, do(t, s, "POST", "/api/channels", key, `{"name":"dev"}`), http.StatusCreated)

	expect(t, do(t, s, "POST", "/api/commits", key, `{"hash":"NOTHEX","message":"m"}`), http.StatusBadRequest)
	expect(t, do(t, s, "POST", "/api/commits", key, `{"hash":"abcdef1","message":"m","channel":"nope"}`), http.StatusNotFound)

	// Without a channel: recorded, not posted.
	expect(t, do(t, s, "POST", "/api/commits", key, `{"hash":"abcdef1","message":"first"}`), http.StatusCreated)
	posts := decode[[]db.Post](t, do(t, s, "GET", "/api/channels/dev/posts", key, ""))
	if len(posts) != 0 {
		t.Fatalf("commit without channel created %d posts", len(posts))
	}

	// With a channel: a post is created with the note.
	expect(t, do(t, s, "POST", "/api/commits", key, `{"hash":"abcdef2","message":"second","channel":"dev","note":"why it matters"}`), http.StatusCreated)
	posts = decode[[]db.Post](t, do(t, s, "GET", "/api/channels/dev/posts", key, ""))
	if len(posts) != 1 || posts[0].Content != "why it matters" {
		t.Fatalf("posts = %+v", posts)
	}

	c := decode[db.Commit](t, do(t, s, "GET", "/api/commits/abcdef2", key, ""))
	if c.Message != "second" || c.AgentID != "a1" {
		t.Fatalf("commit = %+v", c)
	}
	expect(t, do(t, s, "GET", "/api/commits/abcdef9", key, ""), http.StatusNotFound)
	list := decode[[]db.Commit](t, do(t, s, "GET", "/api/commits", key, ""))
	if len(list) != 2 {
		t.Fatalf("listed %d commits, want 2", len(list))
	}
}

func TestSessionRegistration(t *testing.T) {
	s := newTestServer(t)
	body := `{"tool":"claude","session_id":"sess-abc-123456"}`

	w := do(t, s, "POST", "/api/sessions", "", body)
	expect(t, w, http.StatusCreated)
	first := decode[map[string]string](t, w)
	if first["id"] == "" || first["api_key"] == "" {
		t.Fatalf("missing id/key: %v", first)
	}

	// Idempotent for loopback callers: same agent and key.
	w = do(t, s, "POST", "/api/sessions", "", body)
	expect(t, w, http.StatusOK)
	again := decode[map[string]string](t, w)
	if again["id"] != first["id"] || again["api_key"] != first["api_key"] {
		t.Fatalf("not idempotent: %v vs %v", first, again)
	}

	// A remote caller must not be handed the key of an existing session.
	req := httptest.NewRequest("POST", "/api/sessions", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:4000"
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	expect(t, rec, http.StatusConflict)
	if strings.Contains(rec.Body.String(), first["api_key"]) {
		t.Fatal("api key leaked to non-loopback caller")
	}

	expect(t, do(t, s, "POST", "/api/sessions", "", `{"tool":"vim","session_id":"sess-abc-123456"}`), http.StatusBadRequest)
	expect(t, do(t, s, "POST", "/api/sessions", "", `{"tool":"claude","session_id":"x"}`), http.StatusBadRequest)

	// The issued key authenticates.
	expect(t, do(t, s, "GET", "/api/channels", first["api_key"], ""), http.StatusOK)
}

func TestUIWritesAreLoopbackOnly(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/ui/channel", strings.NewReader("name=dev"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.9:4000"
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code < 400 {
		t.Fatalf("remote UI write allowed: status %d", w.Code)
	}
	if ch, _ := s.db.GetChannelByName("dev"); ch != nil {
		t.Fatal("channel created by remote caller")
	}
}

func TestDashboardRenders(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "GET", "/", "", "")
	expect(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "<html") {
		t.Fatal("dashboard did not return html")
	}
}
