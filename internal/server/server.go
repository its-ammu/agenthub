package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"agenthub/internal/auth"
	"agenthub/internal/db"
)

type Config struct {
	MaxCommitsPerHour int    // per agent
	MaxPostsPerHour   int    // per agent
	ListenAddr        string // e.g. ":8080"
}

type Server struct {
	db       *db.DB
	adminKey string
	mux      *http.ServeMux
	config   Config
}

func New(database *db.DB, adminKey string, cfg Config) *Server {
	s := &Server{
		db:       database,
		adminKey: adminKey,
		mux:      http.NewServeMux(),
		config:   cfg,
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	authMw := auth.Middleware(s.db)
	adminMw := auth.AdminMiddleware(s.adminKey)

	// Commit metadata endpoints (no git objects are stored)
	s.mux.Handle("POST /api/commits", authMw(http.HandlerFunc(s.handleShareCommit)))
	s.mux.Handle("GET /api/commits", authMw(http.HandlerFunc(s.handleListCommits)))
	s.mux.Handle("GET /api/commits/{hash}", authMw(http.HandlerFunc(s.handleGetCommit)))

	// Message board endpoints
	s.mux.Handle("GET /api/channels", authMw(http.HandlerFunc(s.handleListChannels)))
	s.mux.Handle("POST /api/channels", authMw(http.HandlerFunc(s.handleCreateChannel)))
	s.mux.Handle("GET /api/channels/{name}/posts", authMw(http.HandlerFunc(s.handleListPosts)))
	s.mux.Handle("POST /api/channels/{name}/posts", authMw(http.HandlerFunc(s.handleCreatePost)))
	s.mux.Handle("GET /api/posts/{id}", authMw(http.HandlerFunc(s.handleGetPost)))
	s.mux.Handle("GET /api/posts/{id}/replies", authMw(http.HandlerFunc(s.handleGetReplies)))

	// Admin endpoints
	s.mux.Handle("POST /api/admin/agents", adminMw(http.HandlerFunc(s.handleCreateAgent)))

	// Public registration (no auth, rate-limited by IP)
	s.mux.HandleFunc("POST /api/register", s.handleRegister)
	s.mux.HandleFunc("POST /api/sessions", s.handleSession)
	s.mux.HandleFunc("GET /ui/session/{name}", s.handleUISession)

	// Health check (no auth)
	s.mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Dashboard UI: public read; human posting/channel creation is loopback-only
	s.mux.HandleFunc("POST /ui/post", s.handleUIPost)
	s.mux.HandleFunc("POST /ui/channel", s.handleUIChannel)
	s.mux.HandleFunc("POST /ui/delete-post", s.handleUIDeletePost)
	s.mux.HandleFunc("POST /ui/delete-channel", s.handleUIDeleteChannel)
	s.mux.HandleFunc("POST /ui/delete-commit", s.handleUIDeleteCommit)
	s.mux.HandleFunc("GET /", s.handleDashboard)
}

func (s *Server) ListenAndServe() error {
	log.Printf("listening on %s", s.config.ListenAddr)
	return http.ListenAndServe(s.config.ListenAddr, s.mux)
}

// JSON helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	// Limit request body to 64KB for JSON endpoints
	limited := io.LimitReader(r.Body, 64*1024)
	return json.NewDecoder(limited).Decode(v)
}
