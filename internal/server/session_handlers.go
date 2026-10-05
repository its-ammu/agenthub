package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"time"

	"agenthub/internal/names"
	"agenthub/internal/tools"
	"agenthub/internal/usage"
)

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{5,127}$`)

// handleSession registers (or looks up) the agent for a tool session. The agent
// gets a generated name derived from the session id. It is idempotent: repeating
// the call for a known session returns the same agent, but the api key is only
// handed back to localhost callers (a client that lost its key can recover it).
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tool      string `json:"tool"`
		SessionID string `json:"session_id"`
		Project   string `json:"project"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !tools.IDRe.MatchString(req.Tool) {
		writeError(w, http.StatusBadRequest, "tool must be a short lowercase id, e.g. claude, cursor, codex")
		return
	}
	if !sessionIDRe.MatchString(req.SessionID) {
		writeError(w, http.StatusBadRequest, "invalid session_id")
		return
	}
	if len(req.Project) > 200 {
		req.Project = req.Project[:200]
	}

	respond := func(status int, id, key string) {
		out := map[string]string{"id": id, "tool": req.Tool, "session_id": req.SessionID}
		if key != "" {
			out["api_key"] = key
		}
		writeJSON(w, status, out)
	}

	existing, err := s.db.GetAgentBySession(req.SessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if existing != nil {
		if !isLoopback(r) {
			writeError(w, http.StatusConflict, "session already registered")
			return
		}
		respond(http.StatusOK, existing.ID, existing.APIKey)
		return
	}

	ip := strings.Split(r.RemoteAddr, ":")[0]
	allowed, err := s.db.CheckRateLimit("ip:"+ip, "session", 60)
	if err != nil || !allowed {
		writeError(w, http.StatusTooManyRequests, "session registration rate limit exceeded")
		return
	}

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate api key")
		return
	}
	apiKey := hex.EncodeToString(keyBytes)

	// Pick the first unused generated name for this session.
	for attempt := 0; attempt < 20; attempt++ {
		name := names.Generate(req.SessionID, attempt)
		if taken, _ := s.db.GetAgentByID(name); taken != nil {
			continue
		}
		if err := s.db.CreateSessionAgent(name, apiKey, req.Tool, req.SessionID, req.Project); err != nil {
			continue
		}
		s.db.IncrementRateLimit("ip:"+ip, "session")
		respond(http.StatusCreated, name, apiKey)
		return
	}
	writeError(w, http.StatusInternalServerError, "could not allocate an agent name")
}

// handleUISession returns an agent's session id, activity and usage report for
// the dashboard. It reads local transcripts, so it is localhost-only.
func (s *Server) handleUISession(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "only allowed from localhost", http.StatusForbidden)
		return
	}
	a, err := s.db.GetAgentInfo(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if a == nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	posts, commits := s.db.AgentActivity(a.ID)
	out := map[string]any{
		"name":       a.ID,
		"tool":       a.Tool,
		"session_id": a.SessionID,
		"project":    a.Project,
		"created_at": a.CreatedAt.Format(time.RFC3339),
		"posts":      posts,
		"commits":    commits,
	}
	if a.SessionID != "" {
		out["usage"] = usage.For(a.Tool, a.SessionID)
	}
	writeJSON(w, http.StatusOK, out)
}
