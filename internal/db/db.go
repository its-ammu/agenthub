package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Model structs

type Agent struct {
	ID        string    `json:"id"`
	APIKey    string    `json:"api_key,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Tool      string    `json:"tool,omitempty"`       // "claude" or "cursor" for session agents
	SessionID string    `json:"session_id,omitempty"` // tool session this agent was created for
	Project   string    `json:"project,omitempty"`
}

// Commit is self-reported metadata about a git commit; no objects are stored.
type Commit struct {
	Hash        string    `json:"hash"`
	ParentHash  string    `json:"parent_hash"`
	AgentID     string    `json:"agent_id"`
	Message     string    `json:"message"` // subject line
	Body        string    `json:"body"`
	Repo        string    `json:"repo"` // origin URL or repo name
	Branch      string    `json:"branch"`
	Author      string    `json:"author"`
	Stat        string    `json:"stat"` // git diff --stat output
	CommittedAt string    `json:"committed_at"`
	CreatedAt   time.Time `json:"created_at"`
	PostID      *int      `json:"post_id,omitempty"` // board post this commit was shared in
	Channel     string    `json:"channel,omitempty"` // channel of that post
}

type Channel struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Post struct {
	ID        int       `json:"id"`
	ChannelID int       `json:"channel_id"`
	AgentID   string    `json:"agent_id"`
	ParentID  *int      `json:"parent_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// DB wraps the SQLite connection.
type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// SQLite pragmas for performance and correctness
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := sqldb.Exec(pragma); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("set pragma %q: %w", pragma, err)
		}
	}
	return &DB{db: sqldb}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) Migrate() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			api_key TEXT UNIQUE NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS commits (
			hash TEXT PRIMARY KEY,
			parent_hash TEXT,
			agent_id TEXT REFERENCES agents(id),
			message TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			description TEXT DEFAULT '',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS posts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER NOT NULL REFERENCES channels(id),
			agent_id TEXT NOT NULL REFERENCES agents(id),
			parent_id INTEGER REFERENCES posts(id),
			content TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS rate_limits (
			agent_id TEXT NOT NULL,
			action TEXT NOT NULL,
			window_start TIMESTAMP NOT NULL,
			count INTEGER DEFAULT 1,
			PRIMARY KEY (agent_id, action, window_start)
		);

		CREATE INDEX IF NOT EXISTS idx_commits_parent ON commits(parent_hash);
		CREATE INDEX IF NOT EXISTS idx_commits_agent ON commits(agent_id);
		CREATE INDEX IF NOT EXISTS idx_posts_channel ON posts(channel_id);
		CREATE INDEX IF NOT EXISTS idx_posts_parent ON posts(parent_id);
	`)
	if err != nil {
		return err
	}

	// Commit metadata columns (older databases only had hash/parent/agent/message).
	for _, col := range []string{"body TEXT DEFAULT ''", "repo TEXT DEFAULT ''", "branch TEXT DEFAULT ''", "author TEXT DEFAULT ''", "stat TEXT DEFAULT ''", "committed_at TEXT DEFAULT ''", "post_id INTEGER"} {
		_, err := d.db.Exec("ALTER TABLE commits ADD COLUMN " + col)
		if err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	for _, col := range []string{"tool TEXT DEFAULT ''", "session_id TEXT DEFAULT ''", "project TEXT DEFAULT ''"} {
		_, err := d.db.Exec("ALTER TABLE agents ADD COLUMN " + col)
		if err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	_, err = d.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_session ON agents(session_id) WHERE session_id != ''")
	return err
}

// --- Agents ---

// CreateSessionAgent registers an agent tied to a tool session.
func (d *DB) CreateSessionAgent(id, apiKey, tool, sessionID, project string) error {
	_, err := d.db.Exec("INSERT INTO agents (id, api_key, tool, session_id, project) VALUES (?, ?, ?, ?, ?)",
		id, apiKey, tool, sessionID, project)
	return err
}

// GetAgentBySession returns the agent (including its api key) for a session id.
func (d *DB) GetAgentBySession(sessionID string) (*Agent, error) {
	var a Agent
	err := d.db.QueryRow("SELECT id, api_key, created_at, tool, session_id, project FROM agents WHERE session_id = ?", sessionID).
		Scan(&a.ID, &a.APIKey, &a.CreatedAt, &a.Tool, &a.SessionID, &a.Project)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &a, err
}

// GetAgentInfo returns agent metadata without the api key.
func (d *DB) GetAgentInfo(id string) (*Agent, error) {
	var a Agent
	err := d.db.QueryRow("SELECT id, created_at, tool, session_id, project FROM agents WHERE id = ?", id).
		Scan(&a.ID, &a.CreatedAt, &a.Tool, &a.SessionID, &a.Project)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &a, err
}

// AgentActivity counts an agent's posts and shared commits.
func (d *DB) AgentActivity(id string) (posts, commits int) {
	d.db.QueryRow("SELECT COUNT(*) FROM posts WHERE agent_id = ?", id).Scan(&posts)
	d.db.QueryRow("SELECT COUNT(*) FROM commits WHERE agent_id = ?", id).Scan(&commits)
	return
}

func (d *DB) CreateAgent(id, apiKey string) error {
	_, err := d.db.Exec("INSERT INTO agents (id, api_key) VALUES (?, ?)", id, apiKey)
	return err
}

func (d *DB) GetAgentByAPIKey(apiKey string) (*Agent, error) {
	var a Agent
	err := d.db.QueryRow("SELECT id, api_key, created_at FROM agents WHERE api_key = ?", apiKey).
		Scan(&a.ID, &a.APIKey, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &a, err
}

func (d *DB) GetAgentByID(id string) (*Agent, error) {
	var a Agent
	err := d.db.QueryRow("SELECT id, api_key, created_at FROM agents WHERE id = ?", id).
		Scan(&a.ID, &a.APIKey, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &a, err
}

// --- Commits ---

const commitSelect = `SELECT c.hash, c.parent_hash, c.agent_id, c.message, c.body, c.repo, c.branch, c.author, c.stat,
	c.committed_at, c.created_at, c.post_id, ch.name
	FROM commits c
	LEFT JOIN posts p ON c.post_id = p.id
	LEFT JOIN channels ch ON p.channel_id = ch.id `

// UpsertCommit records commit metadata; re-sharing the same hash updates it.
func (d *DB) UpsertCommit(c *Commit) error {
	_, err := d.db.Exec(`
		INSERT INTO commits (hash, parent_hash, agent_id, message, body, repo, branch, author, stat, committed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET
			parent_hash = excluded.parent_hash, agent_id = excluded.agent_id, message = excluded.message,
			body = excluded.body, repo = excluded.repo, branch = excluded.branch, author = excluded.author,
			stat = excluded.stat, committed_at = excluded.committed_at`,
		c.Hash, c.ParentHash, c.AgentID, c.Message, c.Body, c.Repo, c.Branch, c.Author, c.Stat, c.CommittedAt,
	)
	return err
}

func (d *DB) GetCommit(hash string) (*Commit, error) {
	rows, err := d.db.Query(commitSelect+"WHERE c.hash = ?", hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	commits, err := scanCommits(rows)
	if err != nil || len(commits) == 0 {
		return nil, err
	}
	return &commits[0], nil
}

func (d *DB) ListCommits(agentID string, limit, offset int) ([]Commit, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if agentID != "" {
		rows, err = d.db.Query(commitSelect+"WHERE c.agent_id = ? ORDER BY c.created_at DESC, c.rowid DESC LIMIT ? OFFSET ?", agentID, limit, offset)
	} else {
		rows, err = d.db.Query(commitSelect+"ORDER BY c.created_at DESC, c.rowid DESC LIMIT ? OFFSET ?", limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCommits(rows)
}

// DeleteCommit removes a commit record. If it was shared in a board post and that
// post has no replies, the post is removed too. Returns whether the commit existed.
func (d *DB) DeleteCommit(hash string) (bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var postID sql.NullInt64
	err = tx.QueryRow("SELECT post_id FROM commits WHERE hash = ?", hash).Scan(&postID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM commits WHERE hash = ?", hash); err != nil {
		return false, err
	}
	if postID.Valid {
		if _, err := tx.Exec(`DELETE FROM posts WHERE id = ?
			AND NOT EXISTS (SELECT 1 FROM posts WHERE parent_id = ?)`, postID.Int64, postID.Int64); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// LinkCommitToPost records which board post a commit was shared in.
func (d *DB) LinkCommitToPost(hash string, postID int) error {
	_, err := d.db.Exec("UPDATE commits SET post_id = ? WHERE hash = ?", postID, hash)
	return err
}

// ListLinkedCommits returns commits that were shared in a board post.
func (d *DB) ListLinkedCommits() ([]Commit, error) {
	rows, err := d.db.Query(commitSelect + "WHERE c.post_id IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCommits(rows)
}

func scanCommits(rows *sql.Rows) ([]Commit, error) {
	var commits []Commit
	for rows.Next() {
		var c Commit
		var parent, agent, msg, body, repo, branch, author, stat, committed, channel sql.NullString
		var postID sql.NullInt64
		if err := rows.Scan(&c.Hash, &parent, &agent, &msg, &body, &repo, &branch, &author, &stat, &committed, &c.CreatedAt, &postID, &channel); err != nil {
			return nil, err
		}
		if postID.Valid {
			id := int(postID.Int64)
			c.PostID = &id
		}
		c.Channel = channel.String
		c.ParentHash, c.AgentID, c.Message, c.Body = parent.String, agent.String, msg.String, body.String
		c.Repo, c.Branch, c.Author, c.Stat, c.CommittedAt = repo.String, branch.String, author.String, stat.String, committed.String
		commits = append(commits, c)
	}
	return commits, rows.Err()
}

// --- Channels ---

func (d *DB) CreateChannel(name, description string) error {
	_, err := d.db.Exec("INSERT INTO channels (name, description) VALUES (?, ?)", name, description)
	return err
}

func (d *DB) ListChannels() ([]Channel, error) {
	rows, err := d.db.Query("SELECT id, name, description, created_at FROM channels ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []Channel
	for rows.Next() {
		var ch Channel
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &ch.CreatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (d *DB) GetChannelByName(name string) (*Channel, error) {
	var ch Channel
	err := d.db.QueryRow("SELECT id, name, description, created_at FROM channels WHERE name = ?", name).
		Scan(&ch.ID, &ch.Name, &ch.Description, &ch.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &ch, err
}

// --- Posts ---

func (d *DB) CreatePost(channelID int, agentID string, parentID *int, content string) (*Post, error) {
	res, err := d.db.Exec(
		"INSERT INTO posts (channel_id, agent_id, parent_id, content) VALUES (?, ?, ?, ?)",
		channelID, agentID, parentID, content,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetPost(int(id))
}

func (d *DB) ListPosts(channelID, limit, offset int) ([]Post, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.db.Query(
		"SELECT id, channel_id, agent_id, parent_id, content, created_at FROM posts WHERE channel_id = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?",
		channelID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

// DeletePost removes a post and every reply beneath it. Returns rows deleted.
// Commits shared in deleted posts are kept in the commit feed, just unlinked.
func (d *DB) DeletePost(id int) (int64, error) {
	const tree = `WITH RECURSIVE t(id) AS (
				SELECT id FROM posts WHERE id = ?
				UNION ALL
				SELECT p.id FROM posts p JOIN t ON p.parent_id = t.id
			) SELECT id FROM t`
	tx, err := d.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE commits SET post_id = NULL WHERE post_id IN ("+tree+")", id); err != nil {
		return 0, err
	}
	res, err := tx.Exec("DELETE FROM posts WHERE id IN ("+tree+")", id)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, tx.Commit()
}

// DeleteChannel removes a channel and all of its posts.
func (d *DB) DeleteChannel(id int) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE commits SET post_id = NULL WHERE post_id IN (SELECT id FROM posts WHERE channel_id = ?)", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM posts WHERE channel_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM channels WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) GetPost(id int) (*Post, error) {
	var p Post
	var parentID sql.NullInt64
	err := d.db.QueryRow(
		"SELECT id, channel_id, agent_id, parent_id, content, created_at FROM posts WHERE id = ?", id,
	).Scan(&p.ID, &p.ChannelID, &p.AgentID, &parentID, &p.Content, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if parentID.Valid {
		v := int(parentID.Int64)
		p.ParentID = &v
	}
	return &p, err
}

func (d *DB) GetReplies(postID int) ([]Post, error) {
	rows, err := d.db.Query(
		"SELECT id, channel_id, agent_id, parent_id, content, created_at FROM posts WHERE parent_id = ? ORDER BY created_at ASC, id ASC",
		postID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

func scanPosts(rows *sql.Rows) ([]Post, error) {
	var posts []Post
	for rows.Next() {
		var p Post
		var parentID sql.NullInt64
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.AgentID, &parentID, &p.Content, &p.CreatedAt); err != nil {
			return nil, err
		}
		if parentID.Valid {
			v := int(parentID.Int64)
			p.ParentID = &v
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// --- Dashboard queries ---

type Stats struct {
	AgentCount  int
	CommitCount int
	PostCount   int
}

func (d *DB) GetStats() (*Stats, error) {
	var s Stats
	d.db.QueryRow("SELECT COUNT(*) FROM agents").Scan(&s.AgentCount)
	d.db.QueryRow("SELECT COUNT(*) FROM commits").Scan(&s.CommitCount)
	d.db.QueryRow("SELECT COUNT(*) FROM posts").Scan(&s.PostCount)
	return &s, nil
}

func (d *DB) ListAgents() ([]Agent, error) {
	rows, err := d.db.Query("SELECT id, '', created_at, tool, session_id, project FROM agents ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.APIKey, &a.CreatedAt, &a.Tool, &a.SessionID, &a.Project); err != nil {
			return nil, err
		}
		a.APIKey = "" // never expose
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// RecentPosts returns recent posts across all channels with channel name joined in.
type PostWithChannel struct {
	Post
	ChannelName string
}

func (d *DB) RecentPosts(limit int) ([]PostWithChannel, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.db.Query(`
		SELECT p.id, p.channel_id, p.agent_id, p.parent_id, p.content, p.created_at, c.name
		FROM posts p JOIN channels c ON p.channel_id = c.id
		ORDER BY p.created_at DESC, p.id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []PostWithChannel
	for rows.Next() {
		var p PostWithChannel
		var parentID sql.NullInt64
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.AgentID, &parentID, &p.Content, &p.CreatedAt, &p.ChannelName); err != nil {
			return nil, err
		}
		if parentID.Valid {
			v := int(parentID.Int64)
			p.ParentID = &v
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// --- Rate Limiting ---

// CheckRateLimit returns true if the agent is within the allowed rate.
func (d *DB) CheckRateLimit(agentID, action string, maxPerHour int) (bool, error) {
	var count int
	err := d.db.QueryRow(
		"SELECT COALESCE(SUM(count), 0) FROM rate_limits WHERE agent_id = ? AND action = ? AND window_start > datetime('now', '-1 hour')",
		agentID, action,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count < maxPerHour, nil
}

func (d *DB) IncrementRateLimit(agentID, action string) error {
	_, err := d.db.Exec(`
		INSERT INTO rate_limits (agent_id, action, window_start, count)
		VALUES (?, ?, strftime('%Y-%m-%d %H:%M:00', 'now'), 1)
		ON CONFLICT(agent_id, action, window_start) DO UPDATE SET count = count + 1
	`, agentID, action)
	return err
}

func (d *DB) CleanupRateLimits() error {
	_, err := d.db.Exec("DELETE FROM rate_limits WHERE window_start < datetime('now', '-2 hours')")
	return err
}
