package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store 是持久化存储接口。
type Store interface {
	Init(ctx context.Context) error
	Close() error

	// 会话管理
	GetOrCreateSession(ctx context.Context, id string) (*Session, error)
	LockSession(ctx context.Context, id string) error
	UnlockSession(ctx context.Context, id string) error
	UnlockStaleSessions(ctx context.Context) error
	DeleteSession(ctx context.Context, id string) error
	ListSessions(ctx context.Context) ([]*Session, error)

	// 消息管理
	AddMessage(ctx context.Context, sessionID, role, content string) error
	AddComplexMessage(ctx context.Context, sessionID, role, content, msgType string) error
	GetMessages(ctx context.Context, sessionID string) ([]*Message, error)
	PruneLastMessage(ctx context.Context, sessionID string) error
	TruncateMessages(ctx context.Context, sessionID string, keep int) error
	DeleteAllMessages(ctx context.Context, sessionID string) error
	DeleteMessagesByIDs(ctx context.Context, ids []int64) error

	// 动态配置管理 - 只保留 agent_settings（记录当前使用的 provider/model）
	SetAgentModel(ctx context.Context, agentID, model, providerID string) error
	GetAgentSetting(ctx context.Context, agentID string) (*AgentSetting, error)

	CreateAgent(ctx context.Context, id, name, workspace, channels, model, providerID string) error
	ListDataAgents(ctx context.Context) ([]*AgentBaseInfo, error)

	// Cron 定时任务管理
	SaveCronJob(ctx context.Context, job *CronJob) error
	DeleteCronJob(ctx context.Context, id string) error
	ListCronJobs(ctx context.Context, agentID string) ([]*CronJob, error)
	ListAllCronJobs(ctx context.Context) ([]*CronJob, error)
	SetCronJobEnabled(ctx context.Context, id string, enabled bool) error
}

type AgentSetting struct {
	AgentID    string
	Model      string
	ProviderID string // 指向 .env 中配置的 provider 名称
}

type AgentBaseInfo struct {
	ID         string
	Name       string
	Workspace  string
	Channels   string // 逗号分隔
	Model      string
	ProviderID string
}

type Session struct {
	ID        string
	Locked    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Message struct {
	ID        int64
	SessionID string
	Role      string
	Content   string
	Type      string // "text" 或 "json"
	CreatedAt time.Time
}

// CronJob 表示一个定时任务。
type CronJob struct {
	ID        string // 唯一 ID（短名，如 "daily-summary"）
	AgentID   string // 执行任务的代理 ID
	Name      string // 人可读名称
	CronExpr  string // Cron 表达式，如 "0 9 * * 1"(每周一上卸9点)
	Prompt    string // 发送给代理的提示词
	ChannelID string // 响应发到哪个渠道 chat（可空表示仅记录日志）
	Enabled   bool   // 是否启用
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	// 通过 DSN 启用 WAL 模式和外键
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 sqlite 失败: %w", err)
	}
	// 设置单个写入者以避免 WAL 争用
	db.SetMaxOpenConns(1)
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Init(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		id         TEXT PRIMARY KEY,
		locked     INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS messages (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		role       TEXT NOT NULL CHECK(role IN ('user','assistant','tool','system')),
		content    TEXT NOT NULL,
		type       TEXT NOT NULL DEFAULT 'text',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);

	CREATE TABLE IF NOT EXISTS agent_settings (
		agent_id    TEXT PRIMARY KEY,
		model       TEXT NOT NULL,
		provider_id TEXT NOT NULL,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS agents (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		workspace   TEXT NOT NULL,
		channels    TEXT NOT NULL, -- 逗号分隔
		model       TEXT NOT NULL,
		provider_id TEXT NOT NULL,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS cron_jobs (
		id         TEXT PRIMARY KEY,
		agent_id   TEXT NOT NULL,
		name       TEXT NOT NULL,
		cron_expr  TEXT NOT NULL,
		prompt     TEXT NOT NULL,
		channel_id TEXT NOT NULL DEFAULT '',
		enabled    INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}

	// 迁移：为已存在的老表添加新列（IF NOT EXISTS 由 SQLite 的错误忽略来实现）
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN type TEXT NOT NULL DEFAULT 'text'`)

	return nil
}

func (s *SQLiteStore) SetAgentModel(ctx context.Context, agentID, model, providerID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_settings(agent_id, model, provider_id, updated_at)
		 VALUES(?,?,?, CURRENT_TIMESTAMP)
		 ON CONFLICT(agent_id) DO UPDATE SET model=excluded.model, provider_id=excluded.provider_id, updated_at=CURRENT_TIMESTAMP`,
		agentID, model, providerID)
	return err
}

func (s *SQLiteStore) GetAgentSetting(ctx context.Context, agentID string) (*AgentSetting, error) {
	row := s.db.QueryRowContext(ctx, `SELECT agent_id, model, provider_id FROM agent_settings WHERE agent_id=?`, agentID)
	a := &AgentSetting{}
	if err := row.Scan(&a.AgentID, &a.Model, &a.ProviderID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // 未设置
		}
		return nil, err
	}
	return a, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) GetOrCreateSession(ctx context.Context, id string) (*Session, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO sessions(id) VALUES(?)`, id)
	if err != nil {
		return nil, fmt.Errorf("插入或忽略会话失败: %w", err)
	}
	return s.fetchSession(ctx, id)
}

func (s *SQLiteStore) fetchSession(ctx context.Context, id string) (*Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, locked, created_at, updated_at FROM sessions WHERE id=?`, id)
	sess := &Session{}
	var locked int
	err := row.Scan(&sess.ID, &locked, &sess.CreatedAt, &sess.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("获取会话失败: %w", err)
	}
	sess.Locked = locked == 1
	return sess, nil
}

func (s *SQLiteStore) LockSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET locked=1, updated_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

func (s *SQLiteStore) UnlockSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET locked=0, updated_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

// UnlockStaleSessions 解锁被残留的锁定会话（例如在进程崩溃后）。
func (s *SQLiteStore) UnlockStaleSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET locked=0 WHERE locked=1`)
	return err
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	return err
}

func (s *SQLiteStore) ListSessions(ctx context.Context) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, locked, created_at, updated_at FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		sess := &Session{}
		var locked int
		if err := rows.Scan(&sess.ID, &locked, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
			return nil, err
		}
		sess.Locked = locked == 1
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

func (s *SQLiteStore) AddMessage(ctx context.Context, sessionID, role, content string) error {
	return s.AddComplexMessage(ctx, sessionID, role, content, "text")
}

func (s *SQLiteStore) AddComplexMessage(ctx context.Context, sessionID, role, content, msgType string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages(session_id, role, content, type) VALUES(?,?,?,?)`,
		sessionID, role, content, msgType)
	return err
}

func (s *SQLiteStore) GetMessages(ctx context.Context, sessionID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, role, content, type, created_at FROM messages WHERE session_id=? ORDER BY created_at ASC`,
		sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		m := &Message{}
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Type, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// PruneLastMessage 移除会话中的最后一条消息（用于卡死循环的自我修复）。
func (s *SQLiteStore) PruneLastMessage(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM messages WHERE id = (
			SELECT id FROM messages WHERE session_id=? ORDER BY id DESC LIMIT 1
		)`, sessionID)
	return err
}

// TruncateMessages 在会话中仅保留最后 `keep` 条消息。
func (s *SQLiteStore) TruncateMessages(ctx context.Context, sessionID string, keep int) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM messages WHERE session_id=? AND id NOT IN (
			SELECT id FROM messages WHERE session_id=? ORDER BY id DESC LIMIT ?
		)`, sessionID, sessionID, keep)
	return err
}

// DeleteAllMessages 物理删除会话的所有消息。
func (s *SQLiteStore) DeleteAllMessages(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE session_id=?`, sessionID)
	return err
}

// DeleteMessagesByIDs 根据 ID 列表删除消息（用于清理孤立工具消息）
func (s *SQLiteStore) DeleteMessagesByIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	// 构建占位符
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`DELETE FROM messages WHERE id IN (%s)`, strings.Join(placeholders, ","))
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteStore) CreateAgent(ctx context.Context, id, name, workspace, channels, model, providerID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agents(id, name, workspace, channels, model, provider_id, updated_at)
		 VALUES(?,?,?,?,?,?, CURRENT_TIMESTAMP)
		 ON CONFLICT(id) DO UPDATE SET name=excluded.name, workspace=excluded.workspace, channels=excluded.channels, model=excluded.model, provider_id=excluded.provider_id, updated_at=CURRENT_TIMESTAMP`,
		id, name, workspace, channels, model, providerID)
	return err
}

func (s *SQLiteStore) ListDataAgents(ctx context.Context) ([]*AgentBaseInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, workspace, channels, model, provider_id FROM agents ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []*AgentBaseInfo
	for rows.Next() {
		a := &AgentBaseInfo{}
		if err := rows.Scan(&a.ID, &a.Name, &a.Workspace, &a.Channels, &a.Model, &a.ProviderID); err != nil {
			return nil, err
		}
		res = append(res, a)
	}
	return res, nil
}

// --- Cron 定时任务 ---

func (s *SQLiteStore) SaveCronJob(ctx context.Context, job *CronJob) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cron_jobs(id, agent_id, name, cron_expr, prompt, channel_id, enabled, updated_at)
		 VALUES(?,?,?,?,?,?,?, CURRENT_TIMESTAMP)
		 ON CONFLICT(id) DO UPDATE SET
		   agent_id=excluded.agent_id,
		   name=excluded.name,
		   cron_expr=excluded.cron_expr,
		   prompt=excluded.prompt,
		   channel_id=excluded.channel_id,
		   enabled=excluded.enabled,
		   updated_at=CURRENT_TIMESTAMP`,
		job.ID, job.AgentID, job.Name, job.CronExpr, job.Prompt, job.ChannelID, boolToInt(job.Enabled))
	return err
}

func (s *SQLiteStore) DeleteCronJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id=?`, id)
	return err
}

func (s *SQLiteStore) ListCronJobs(ctx context.Context, agentID string) ([]*CronJob, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, name, cron_expr, prompt, channel_id, enabled, created_at, updated_at
		 FROM cron_jobs WHERE agent_id=? ORDER BY id ASC`, agentID)
	if err != nil {
		return nil, err
	}
	return scanCronJobs(rows)
}

func (s *SQLiteStore) ListAllCronJobs(ctx context.Context) ([]*CronJob, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, name, cron_expr, prompt, channel_id, enabled, created_at, updated_at
		 FROM cron_jobs ORDER BY agent_id, id ASC`)
	if err != nil {
		return nil, err
	}
	return scanCronJobs(rows)
}

func (s *SQLiteStore) SetCronJobEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE cron_jobs SET enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		boolToInt(enabled), id)
	return err
}

func scanCronJobs(rows *sql.Rows) ([]*CronJob, error) {
	defer rows.Close()
	var res []*CronJob
	for rows.Next() {
		j := &CronJob{}
		var enabled int
		if err := rows.Scan(&j.ID, &j.AgentID, &j.Name, &j.CronExpr, &j.Prompt, &j.ChannelID,
			&enabled, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.Enabled = enabled == 1
		res = append(res, j)
	}
	return res, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
