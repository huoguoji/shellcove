// Package db 负责 SQLite 初始化、表结构迁移与基础数据种子。
package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Open 打开 SQLite 连接并设置 PRAGMA（WAL、外键、忙等待）。
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", filepath.ToSlash(path))
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 单文件写入，限制连接数避免 database is locked。
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(time.Hour)
	if err := conn.Ping(); err != nil {
		return nil, err
	}
	return conn, nil
}

// Migrate 建表，幂等。
func Migrate(conn *sql.DB) error {
	for _, stmt := range schema {
		if _, err := conn.Exec(stmt); err != nil {
			return fmt.Errorf("执行建表语句失败: %w", err)
		}
	}
	return nil
}

// schema 全部表结构。时间统一由应用写入 RFC3339（UTC）字符串。
var schema = []string{
	`CREATE TABLE IF NOT EXISTS folders (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		parent_id   TEXT,
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_folders_parent ON folders(parent_id)`,

	`CREATE TABLE IF NOT EXISTS ssh_infos (
		id              TEXT PRIMARY KEY,
		name            TEXT NOT NULL,
		remark          TEXT,
		host            TEXT NOT NULL,
		port            INTEGER DEFAULT 22,
		username        TEXT NOT NULL,
		auth_type       TEXT NOT NULL,
		password_enc    TEXT,
		private_key_enc TEXT,
		passphrase_enc  TEXT,
		folder_id       TEXT,
		key_id          INTEGER DEFAULT 1,
		monitor_enabled INTEGER DEFAULT 0,
		proxy_type      TEXT,
		proxy_host      TEXT,
		proxy_port      INTEGER,
		jump_ssh_id     TEXT,
		deleted_at      TEXT,
		created_at      TEXT NOT NULL,
		updated_at      TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_ssh_folder ON ssh_infos(folder_id)`,
	`CREATE INDEX IF NOT EXISTS idx_ssh_deleted ON ssh_infos(deleted_at)`,

	`CREATE TABLE IF NOT EXISTS users (
		id                   TEXT PRIMARY KEY,
		username             TEXT NOT NULL UNIQUE,
		password_hash        TEXT NOT NULL,
		role                 TEXT NOT NULL DEFAULT 'user',
		totp_secret_enc      TEXT,
		totp_enabled         INTEGER NOT NULL DEFAULT 0,
		must_change_password INTEGER NOT NULL DEFAULT 0,
		disabled             INTEGER NOT NULL DEFAULT 0,
		max_sessions         INTEGER NOT NULL DEFAULT 0,
		remark               TEXT,
		last_login_at        TEXT,
		created_at           TEXT NOT NULL,
		updated_at           TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_users_name ON users(username)`,

	`CREATE TABLE IF NOT EXISTS recovery_codes (
		id         TEXT PRIMARY KEY,
		user_id    TEXT NOT NULL,
		code_hash  TEXT NOT NULL,
		used_at    TEXT,
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_recovery_user ON recovery_codes(user_id)`,

	`CREATE TABLE IF NOT EXISTS permissions (
		id            TEXT PRIMARY KEY,
		user_id       TEXT NOT NULL,
		resource_type TEXT NOT NULL,
		resource_id   TEXT,
		can_sftp      INTEGER NOT NULL DEFAULT 1,
		can_monitor   INTEGER NOT NULL DEFAULT 1,
		created_at    TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_perm_user ON permissions(user_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_perm_unique ON permissions(user_id, resource_type, IFNULL(resource_id, ''))`,

	`CREATE TABLE IF NOT EXISTS command_groups (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,

	`CREATE TABLE IF NOT EXISTS commands (
		id         TEXT PRIMARY KEY,
		group_id   TEXT,
		name       TEXT NOT NULL,
		content    TEXT NOT NULL,
		remark     TEXT,
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_commands_group ON commands(group_id)`,

	`CREATE TABLE IF NOT EXISTS audit_logs (
		id          TEXT PRIMARY KEY,
		user_id     TEXT,
		username    TEXT,
		action      TEXT NOT NULL,
		target_type TEXT,
		target_id   TEXT,
		target_name TEXT,
		detail      TEXT,
		ip          TEXT,
		user_agent  TEXT,
		success     INTEGER NOT NULL DEFAULT 1,
		created_at  TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_logs(action)`,

	`CREATE TABLE IF NOT EXISTS sessions (
		id             TEXT PRIMARY KEY,
		user_id        TEXT NOT NULL,
		username       TEXT,
		ssh_id         TEXT,
		ssh_name       TEXT,
		title          TEXT,
		client_ip      TEXT,
		status         TEXT NOT NULL,
		recording_path TEXT,
		recording_size INTEGER NOT NULL DEFAULT 0,
		bytes_in       INTEGER NOT NULL DEFAULT 0,
		bytes_out      INTEGER NOT NULL DEFAULT 0,
		close_reason   TEXT,
		started_at     TEXT NOT NULL,
		last_active_at TEXT NOT NULL,
		ended_at       TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status)`,

	`CREATE TABLE IF NOT EXISTS session_commands (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		user_id    TEXT,
		command    TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_sesscmd_session ON session_commands(session_id)`,

	`CREATE TABLE IF NOT EXISTS upload_progress (
		id            TEXT PRIMARY KEY,
		user_id       TEXT NOT NULL,
		ssh_id        TEXT NOT NULL,
		remote_path   TEXT NOT NULL,
		filename      TEXT NOT NULL,
		total_size    INTEGER NOT NULL DEFAULT 0,
		received_size INTEGER NOT NULL DEFAULT 0,
		chunk_size    INTEGER NOT NULL DEFAULT 0,
		file_hash     TEXT,
		received_hash TEXT,
		temp_path     TEXT NOT NULL,
		status        TEXT NOT NULL DEFAULT 'uploading',
		created_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_upload_user ON upload_progress(user_id, ssh_id)`,

	`CREATE TABLE IF NOT EXISTS settings (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,

	`CREATE TABLE IF NOT EXISTS token_blacklist (
		jti        TEXT PRIMARY KEY,
		expires_at TEXT NOT NULL
	)`,
}
