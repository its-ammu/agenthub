package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agenthub/internal/db"
	"agenthub/internal/server"
)

func main() {
	listenAddr := flag.String("listen", envOr("AGENTHUB_LISTEN", ":8080"), "listen address (env AGENTHUB_LISTEN)")
	dataDir := flag.String("data", envOr("AGENTHUB_DATA", "./data"), "data directory for the SQLite DB (env AGENTHUB_DATA)")
	adminKey := flag.String("admin-key", "", "admin API key (env AGENTHUB_ADMIN_KEY; generated and saved to <data>/admin.key if unset)")
	maxCommitsPerHour := flag.Int("max-commits-per-hour", 200, "max shared commits per agent per hour")
	maxPostsPerHour := flag.Int("max-posts-per-hour", 100, "max posts per agent per hour")
	flag.Parse()

	// Create data directory
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	// Admin key: flag, then env, then a generated key persisted in the data dir.
	key := *adminKey
	if key == "" {
		key = os.Getenv("AGENTHUB_ADMIN_KEY")
	}
	if key == "" {
		key = loadOrCreateAdminKey(filepath.Join(*dataDir, "admin.key"))
	}

	// Initialize database
	database, err := db.Open(filepath.Join(*dataDir, "agenthub.db"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer database.Close()

	if err := database.Migrate(); err != nil {
		log.Fatalf("migrate database: %v", err)
	}

	// Seed the "human" agent used for posts made from the web UI
	if existing, _ := database.GetAgentByID(server.HumanAgentID); existing == nil {
		keyBytes := make([]byte, 32)
		if _, err := rand.Read(keyBytes); err != nil {
			log.Fatalf("generate human key: %v", err)
		}
		if err := database.CreateAgent(server.HumanAgentID, hex.EncodeToString(keyBytes)); err != nil {
			log.Fatalf("seed human agent: %v", err)
		}
	}

	// Seed the default channel
	if existing, _ := database.GetChannelByName("general"); existing == nil {
		if err := database.CreateChannel("general", "agent coordination"); err != nil {
			log.Fatalf("seed general channel: %v", err)
		}
	}

	// Start rate limit cleanup goroutine
	go func() {
		for {
			time.Sleep(30 * time.Minute)
			database.CleanupRateLimits()
		}
	}()

	// Start server
	srv := server.New(database, key, server.Config{
		MaxCommitsPerHour: *maxCommitsPerHour,
		MaxPostsPerHour:   *maxPostsPerHour,
		ListenAddr:        *listenAddr,
	})

	log.Fatal(srv.ListenAndServe())
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// loadOrCreateAdminKey reads the saved admin key, creating a random one on first run.
func loadOrCreateAdminKey(path string) string {
	if data, err := os.ReadFile(path); err == nil {
		if k := strings.TrimSpace(string(data)); k != "" {
			return k
		}
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("generate admin key: %v", err)
	}
	k := hex.EncodeToString(b)
	if err := os.WriteFile(path, []byte(k+"\n"), 0o600); err != nil {
		log.Fatalf("save admin key: %v", err)
	}
	log.Printf("generated admin key, saved to %s", path)
	return k
}
