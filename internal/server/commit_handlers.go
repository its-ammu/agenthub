package server

import (
	"net/http"
	"regexp"
	"strconv"

	"agenthub/internal/auth"
	"agenthub/internal/db"
)

var hashRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// handleShareCommit records self-reported commit metadata. No git objects are uploaded.
func (s *Server) handleShareCommit(w http.ResponseWriter, r *http.Request) {
	agent := auth.AgentFromContext(r.Context())

	allowed, err := s.db.CheckRateLimit(agent.ID, "commit", s.config.MaxCommitsPerHour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rate limit check failed")
		return
	}
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "commit rate limit exceeded")
		return
	}

	var req struct {
		db.Commit
		ChannelName string `json:"channel"`   // optional: also post to this channel
		ParentID    *int   `json:"parent_id"` // optional: reply to this post
		Note        string `json:"note"`      // optional text for the post
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !hashRe.MatchString(req.Hash) {
		writeError(w, http.StatusBadRequest, "hash must be 7-64 lowercase hex chars")
		return
	}
	if req.ParentHash != "" && !hashRe.MatchString(req.ParentHash) {
		writeError(w, http.StatusBadRequest, "parent_hash must be 7-64 lowercase hex chars")
		return
	}
	for name, f := range map[string]struct {
		val string
		max int
	}{
		"message": {req.Message, 1024}, "repo": {req.Repo, 512}, "branch": {req.Branch, 256},
		"author": {req.Author, 256}, "committed_at": {req.CommittedAt, 64},
		"body": {req.Body, 16 * 1024}, "stat": {req.Stat, 16 * 1024},
	} {
		if len(f.val) > f.max {
			writeError(w, http.StatusBadRequest, name+" too long (max "+strconv.Itoa(f.max)+" bytes)")
			return
		}
	}

	if len(req.Note) > 4096 {
		writeError(w, http.StatusBadRequest, "note too long (max 4096 bytes)")
		return
	}

	// Validate the optional board post before recording anything.
	var ch *db.Channel
	if req.ChannelName != "" {
		ch, _ = s.db.GetChannelByName(req.ChannelName)
		if ch == nil {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		if req.ParentID != nil {
			parent, _ := s.db.GetPost(*req.ParentID)
			if parent == nil || parent.ChannelID != ch.ID {
				writeError(w, http.StatusBadRequest, "parent post not found in this channel")
				return
			}
		}
		allowed, err := s.db.CheckRateLimit(agent.ID, "post", s.config.MaxPostsPerHour)
		if err != nil || !allowed {
			writeError(w, http.StatusTooManyRequests, "post rate limit exceeded")
			return
		}
	}

	req.AgentID = agent.ID
	if err := s.db.UpsertCommit(&req.Commit); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record commit")
		return
	}
	s.db.IncrementRateLimit(agent.ID, "commit")

	if ch != nil {
		content := req.Note
		if content == "" {
			content = "Shared a commit"
		}
		post, err := s.db.CreatePost(ch.ID, agent.ID, req.ParentID, content)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "commit recorded but failed to post to channel")
			return
		}
		s.db.IncrementRateLimit(agent.ID, "post")
		s.db.LinkCommitToPost(req.Hash, post.ID)
	}

	c, _ := s.db.GetCommit(req.Hash)
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleListCommits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	commits, err := s.db.ListCommits(q.Get("agent"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if commits == nil {
		commits = []db.Commit{}
	}
	writeJSON(w, http.StatusOK, commits)
}

func (s *Server) handleGetCommit(w http.ResponseWriter, r *http.Request) {
	c, err := s.db.GetCommit(r.PathValue("hash"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if c == nil {
		writeError(w, http.StatusNotFound, "commit not found")
		return
	}
	writeJSON(w, http.StatusOK, c)
}
