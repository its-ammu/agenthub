package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agenthub/internal/db"
	"agenthub/internal/server"
	"agenthub/internal/usage"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	listenAddr := flag.String("listen", envOr("AGENTHUB_LISTEN", ":8080"), "listen address (env AGENTHUB_LISTEN)")
	dataDir := flag.String("data", envOr("AGENTHUB_DATA", defaultDataDir()), "data directory for the SQLite DB (env AGENTHUB_DATA; default ~/.agenthub/data)")
	adminKey := flag.String("admin-key", "", "admin API key (env AGENTHUB_ADMIN_KEY; generated and saved to <data>/admin.key if unset)")
	maxCommitsPerHour := flag.Int("max-commits-per-hour", 200, "max shared commits per agent per hour")
	maxPostsPerHour := flag.Int("max-posts-per-hour", 100, "max posts per agent per hour")
	pricesPath := flag.String("prices", os.Getenv("AGENTHUB_PRICES"), "JSON file of model prices that overrides the built-in table (env AGENTHUB_PRICES; default <data>/prices.json if present)")
	printPrices := flag.Bool("print-prices", false, "print the built-in price table as JSON and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("agenthub-server", version)
		return
	}
	if *printPrices {
		os.Stdout.Write(usage.DefaultPricesJSON())
		return
	}

	warnLegacyData(*dataDir)

	// Create data directory
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	// Price overrides for usage cost estimates.
	if *pricesPath == "" {
		if def := filepath.Join(*dataDir, "prices.json"); fileExists(def) {
			*pricesPath = def
		}
	}
	if *pricesPath != "" {
		if err := usage.LoadPrices(*pricesPath); err != nil {
			log.Fatalf("load prices: %v", err)
		}
		log.Printf("loaded price overrides from %s", *pricesPath)
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

// defaultDataDir is ~/.agenthub/data, so the hub finds the same database no
// matter which directory it is started from.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./data"
	}
	return filepath.Join(home, ".agenthub", "data")
}

// warnLegacyData points at a ./data database left over from when the default
// was relative to the working directory, so an upgrade does not silently
// start from an empty hub.
func warnLegacyData(dataDir string) {
	if fileExists(filepath.Join(dataDir, "agenthub.db")) || !fileExists(filepath.Join("data", "agenthub.db")) {
		return
	}
	if abs, err := filepath.Abs("data"); err == nil {
		if want, err := filepath.Abs(dataDir); err == nil && abs == want {
			return
		}
	}
	log.Printf("note: found an older database in ./data, but the default data directory is now %s", dataDir)
	log.Printf("      to keep using it, move it (stop the hub first) or start with --data ./data")
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
