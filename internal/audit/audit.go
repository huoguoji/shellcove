// Package audit 负责操作审计日志、终端会话记录与会话录像（asciinema v2 格式）。
package audit

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/huoguoji/shellcove/internal/db"
)

// 审计动作常量。
const (
	ActionLogin            = "login"
	ActionLoginFailed      = "login_failed"
	ActionLogout           = "logout"
	ActionPasswordChange   = "password_change"
	ActionTOTPEnable       = "totp_enable"
	ActionTOTPDisable      = "totp_disable"
	ActionTOTPFailed       = "totp_failed"
	ActionRecoveryUsed     = "recovery_code_used"
	ActionSSHCreate        = "ssh_create"
	ActionSSHUpdate        = "ssh_update"
	ActionSSHDelete        = "ssh_delete"
	ActionSSHRestore       = "ssh_restore"
	ActionSSHPurge         = "ssh_purge"
	ActionSSHReveal        = "ssh_reveal"
	ActionSSHRevealAlert   = "ssh_reveal_alert"
	ActionSSHConnect       = "ssh_connect"
	ActionSSHDisconnect    = "ssh_disconnect"
	ActionFolderCreate     = "folder_create"
	ActionFolderUpdate     = "folder_update"
	ActionFolderDelete     = "folder_delete"
	ActionUserCreate       = "user_create"
	ActionUserUpdate       = "user_update"
	ActionUserDelete       = "user_delete"
	ActionUserDisable      = "user_disable"
	ActionUserEnable       = "user_enable"
	ActionPermissionUpdate = "permission_update"
	ActionCommandCreate    = "command_create"
	ActionCommandUpdate    = "command_update"
	ActionCommandDelete    = "command_delete"
	ActionCommandSend      = "command_send"
	ActionSFTPUpload       = "sftp_upload"
	ActionSFTPDownload     = "sftp_download"
	ActionSFTPDelete       = "sftp_delete"
	ActionSFTPMkdir        = "sftp_mkdir"
	ActionSFTPRename       = "sftp_rename"
	ActionSFTPChmod        = "sftp_chmod"
	ActionBackupExport     = "backup_export"
	ActionBackupExportPrev = "backup_export_preview"
	ActionBackupImport     = "backup_import"
	ActionBackupInspect    = "backup_import_inspect"
	ActionSettingsUpdate   = "settings_update"
	ActionTrashEmpty       = "trash_empty"
	ActionSessionKill      = "session_kill"
	ActionAuditClear       = "audit_clear"
	ActionMonitorRead      = "monitor_read"
)

// Entry 一条审计记录。
type Entry struct {
	UserID     string
	Username   string
	Action     string
	TargetType string
	TargetID   string
	TargetName string
	Detail     string
	IP         string
	UserAgent  string
	Success    bool
}

// Record 审计日志读取结构。
type Record struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	Username   string `json:"username"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	TargetName string `json:"target_name"`
	Detail     string `json:"detail"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	Success    bool   `json:"success"`
	CreatedAt  string `json:"created_at"`
}

// Session 终端会话记录。
type Session struct {
	ID            string `json:"id"`
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	SSHID         string `json:"ssh_id"`
	SSHName       string `json:"ssh_name"`
	Title         string `json:"title"`
	ClientIP      string `json:"client_ip"`
	Status        string `json:"status"`
	RecordingPath string `json:"-"`
	HasRecording  bool   `json:"has_recording"`
	RecordingSize int64  `json:"recording_size"`
	BytesIn       int64  `json:"bytes_in"`
	BytesOut      int64  `json:"bytes_out"`
	CloseReason   string `json:"close_reason"`
	StartedAt     string `json:"started_at"`
	LastActiveAt  string `json:"last_active_at"`
	EndedAt       string `json:"ended_at"`
}

// SessionCommand 会话中捕获到的命令。
type SessionCommand struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	Command   string `json:"command"`
	CreatedAt string `json:"created_at"`
}

// Logger 审计服务。
type Logger struct {
	conn           *sql.DB
	recordingsDir  string
	maxRecordingMB int
}

// NewLogger 构造审计服务。
func NewLogger(conn *sql.DB, recordingsDir string, maxRecordingMB int) *Logger {
	return &Logger{conn: conn, recordingsDir: recordingsDir, maxRecordingMB: maxRecordingMB}
}

// Log 写入一条审计日志。
func (l *Logger) Log(e Entry) {
	_, err := l.conn.Exec(
		`INSERT INTO audit_logs (id, user_id, username, action, target_type, target_id, target_name, detail, ip, user_agent, success, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), e.UserID, e.Username, e.Action, e.TargetType, e.TargetID, e.TargetName,
		e.Detail, e.IP, e.UserAgent, e.Success, db.NowStr())
	if err != nil {
		// 审计写入失败不应阻断主流程，但必须留痕到标准错误。
		fmt.Fprintf(os.Stderr, "[audit] 写入审计日志失败: %v\n", err)
	}
}

// Logf 便捷方法：带格式化详情。
func (l *Logger) Logf(e Entry, format string, args ...any) {
	e.Detail = fmt.Sprintf(format, args...)
	l.Log(e)
}

// QueryFilter 审计查询条件。
type QueryFilter struct {
	UserID string
	Action string
	Search string
	From   string
	To     string
	Limit  int
	Offset int
}

// List 分页查询审计日志，返回记录与总数。
func (l *Logger) List(f QueryFilter) ([]*Record, int, error) {
	var where []string
	var args []any

	if f.UserID != "" {
		where = append(where, `user_id = ?`)
		args = append(args, f.UserID)
	}
	if f.Action != "" {
		where = append(where, `action = ?`)
		args = append(args, f.Action)
	}
	if f.From != "" {
		where = append(where, `created_at >= ?`)
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, `created_at <= ?`)
		args = append(args, f.To)
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		like := "%" + term + "%"
		where = append(where, `(username LIKE ? OR target_name LIKE ? OR detail LIKE ? OR ip LIKE ?)`)
		args = append(args, like, like, like, like)
	}

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := l.conn.QueryRow(`SELECT COUNT(*) FROM audit_logs`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	query := `SELECT id, COALESCE(user_id, ''), COALESCE(username, ''), action,
		COALESCE(target_type, ''), COALESCE(target_id, ''), COALESCE(target_name, ''),
		COALESCE(detail, ''), COALESCE(ip, ''), COALESCE(user_agent, ''), success, created_at
		FROM audit_logs` + clause + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, maxInt(f.Offset, 0))

	rows, err := l.conn.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]*Record, 0, limit)
	for rows.Next() {
		r := &Record{}
		if err := rows.Scan(&r.ID, &r.UserID, &r.Username, &r.Action, &r.TargetType, &r.TargetID,
			&r.TargetName, &r.Detail, &r.IP, &r.UserAgent, &r.Success, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// Clear 清理审计日志，before 为空时清空全部，返回删除条数。
func (l *Logger) Clear(before string) (int64, error) {
	var res sql.Result
	var err error
	if strings.TrimSpace(before) == "" {
		res, err = l.conn.Exec(`DELETE FROM audit_logs`)
	} else {
		res, err = l.conn.Exec(`DELETE FROM audit_logs WHERE created_at < ?`, before)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Count 统计审计日志条数。
func (l *Logger) Count() (int, error) {
	var n int
	err := l.conn.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&n)
	return n, err
}

// CreateSession 创建会话记录。
func (l *Logger) CreateSession(s *Session) error {
	now := db.NowStr()
	if s.StartedAt == "" {
		s.StartedAt = now
	}
	s.LastActiveAt = now
	_, err := l.conn.Exec(
		`INSERT INTO sessions (id, user_id, username, ssh_id, ssh_name, title, client_ip, status,
			recording_path, recording_size, bytes_in, bytes_out, started_at, last_active_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?, 0, 0, 0, ?, ?)`,
		s.ID, s.UserID, s.Username, s.SSHID, s.SSHName, s.Title, s.ClientIP, s.RecordingPath, s.StartedAt, s.LastActiveAt)
	return err
}

// EndSession 结束会话。
func (l *Logger) EndSession(id, status, reason string, bytesIn, bytesOut, recordingSize int64) error {
	_, err := l.conn.Exec(
		`UPDATE sessions SET status = ?, close_reason = ?, bytes_in = ?, bytes_out = ?, recording_size = ?,
			ended_at = ?, last_active_at = ? WHERE id = ?`,
		status, reason, bytesIn, bytesOut, recordingSize, db.NowStr(), db.NowStr(), id)
	return err
}

// TouchSession 刷新会话活跃时间。
func (l *Logger) TouchSession(id string) {
	_, _ = l.conn.Exec(`UPDATE sessions SET last_active_at = ? WHERE id = ?`, db.NowStr(), id)
}

// ActiveSessionCount 统计某用户当前活跃会话数。
func (l *Logger) ActiveSessionCount(userID string) (int, error) {
	var n int
	err := l.conn.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ? AND status = 'active'`, userID).Scan(&n)
	return n, err
}

// SessionFilter 会话查询条件。
type SessionFilter struct {
	UserID string
	SSHID  string
	Search string
	Status string
	Limit  int
	Offset int
}

// ListSessions 分页查询会话。
func (l *Logger) ListSessions(f SessionFilter) ([]*Session, int, error) {
	var where []string
	var args []any

	if f.UserID != "" {
		where = append(where, `user_id = ?`)
		args = append(args, f.UserID)
	}
	if f.SSHID != "" {
		where = append(where, `ssh_id = ?`)
		args = append(args, f.SSHID)
	}
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, f.Status)
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		like := "%" + term + "%"
		where = append(where, `(username LIKE ? OR ssh_name LIKE ? OR title LIKE ?)`)
		args = append(args, like, like, like)
	}

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := l.conn.QueryRow(`SELECT COUNT(*) FROM sessions`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT id, user_id, COALESCE(username, ''), COALESCE(ssh_id, ''), COALESCE(ssh_name, ''),
		COALESCE(title, ''), COALESCE(client_ip, ''), status, COALESCE(recording_path, ''), recording_size,
		bytes_in, bytes_out, COALESCE(close_reason, ''), started_at, last_active_at, COALESCE(ended_at, '')
		FROM sessions` + clause + ` ORDER BY started_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, maxInt(f.Offset, 0))

	rows, err := l.conn.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]*Session, 0, limit)
	for rows.Next() {
		s := &Session{}
		if err := rows.Scan(&s.ID, &s.UserID, &s.Username, &s.SSHID, &s.SSHName, &s.Title, &s.ClientIP,
			&s.Status, &s.RecordingPath, &s.RecordingSize, &s.BytesIn, &s.BytesOut, &s.CloseReason,
			&s.StartedAt, &s.LastActiveAt, &s.EndedAt); err != nil {
			return nil, 0, err
		}
		s.HasRecording = s.RecordingPath != "" && s.RecordingSize > 0
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// GetSession 查询单个会话。
func (l *Logger) GetSession(id string) (*Session, error) {
	row := l.conn.QueryRow(`SELECT id, user_id, COALESCE(username, ''), COALESCE(ssh_id, ''), COALESCE(ssh_name, ''),
		COALESCE(title, ''), COALESCE(client_ip, ''), status, COALESCE(recording_path, ''), recording_size,
		bytes_in, bytes_out, COALESCE(close_reason, ''), started_at, last_active_at, COALESCE(ended_at, '')
		FROM sessions WHERE id = ?`, id)
	s := &Session{}
	err := row.Scan(&s.ID, &s.UserID, &s.Username, &s.SSHID, &s.SSHName, &s.Title, &s.ClientIP,
		&s.Status, &s.RecordingPath, &s.RecordingSize, &s.BytesIn, &s.BytesOut, &s.CloseReason,
		&s.StartedAt, &s.LastActiveAt, &s.EndedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.HasRecording = s.RecordingPath != "" && s.RecordingSize > 0
	return s, nil
}

// MarkStaleSessions 将重启前遗留的 active 会话标记为已中断。
func (l *Logger) MarkStaleSessions() error {
	_, err := l.conn.Exec(
		`UPDATE sessions SET status = 'closed', close_reason = '服务重启', ended_at = ? WHERE status = 'active'`,
		db.NowStr())
	return err
}

// AddCommand 记录会话中执行的命令。
func (l *Logger) AddCommand(sessionID, userID, cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	if _, err := l.conn.Exec(
		`INSERT INTO session_commands (id, session_id, user_id, command, created_at) VALUES (?, ?, ?, ?, ?)`,
		uuid.NewString(), sessionID, userID, cmd, db.NowStr()); err != nil {
		fmt.Fprintf(os.Stderr, "[audit] 记录会话命令失败: %v\n", err)
	}
}

// ListCommands 返回会话内命令列表。
func (l *Logger) ListCommands(sessionID string) ([]*SessionCommand, error) {
	rows, err := l.conn.Query(
		`SELECT id, session_id, COALESCE(user_id, ''), command, created_at FROM session_commands
		 WHERE session_id = ? ORDER BY created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*SessionCommand, 0, 16)
	for rows.Next() {
		c := &SessionCommand{}
		if err := rows.Scan(&c.ID, &c.SessionID, &c.UserID, &c.Command, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecordingFile 返回会话录像文件路径。
func (l *Logger) RecordingFile(sessionID string) string {
	return filepath.Join(l.recordingsDir, sessionID+".cast")
}

// ReadRecording 读取录像文件内容。
func (l *Logger) ReadRecording(sessionID string) ([]byte, error) {
	return os.ReadFile(l.RecordingFile(sessionID))
}

// recordingHeader asciinema v2 头部。
type recordingHeader struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp"`
	Env       map[string]string `json:"env"`
}

// Recorder 会话录像器，输出 asciinema v2 格式，可直接用 asciinema play 回放。
type Recorder struct {
	mu      sync.Mutex
	file    *os.File
	start   time.Time
	size    int64
	limit   int64
	dropped bool
	path    string
}

// NewRecorder 创建录像文件并写入头部。
func (l *Logger) NewRecorder(sessionID string, cols, rows int) (*Recorder, error) {
	if err := os.MkdirAll(l.recordingsDir, 0o700); err != nil {
		return nil, err
	}
	path := l.RecordingFile(sessionID)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}

	header, err := json.Marshal(recordingHeader{
		Version:   2,
		Width:     cols,
		Height:    rows,
		Timestamp: time.Now().Unix(),
		Env:       map[string]string{"TERM": "xterm-256color", "SHELL": "/bin/bash"},
	})
	if err != nil {
		f.Close()
		return nil, err
	}
	line := append(header, '\n')
	if _, err := f.Write(line); err != nil {
		f.Close()
		return nil, err
	}

	limit := int64(0)
	if l.maxRecordingMB > 0 {
		limit = int64(l.maxRecordingMB) * 1024 * 1024
	}
	return &Recorder{
		file:  f,
		start: time.Now(),
		size:  int64(len(line)),
		limit: limit,
		path:  path,
	}, nil
}

// Write 记录一条事件，"o" 表示终端输出，"i" 表示用户输入。
func (r *Recorder) Write(kind string, data []byte) {
	if r == nil || len(data) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return
	}
	if r.limit > 0 && r.size >= r.limit {
		if !r.dropped {
			r.dropped = true
			fmt.Fprintf(os.Stderr, "[audit] 会话录像超过大小限制，已停止记录: %s\n", r.path)
		}
		return
	}

	event, err := json.Marshal([]any{time.Since(r.start).Seconds(), kind, string(data)})
	if err != nil {
		return
	}
	line := append(event, '\n')
	if _, err := r.file.Write(line); err != nil {
		return
	}
	r.size += int64(len(line))
}

// Close 关闭录像文件，返回文件大小。
func (r *Recorder) Close() int64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return 0
	}
	size := r.size
	_ = r.file.Sync()
	_ = r.file.Close()
	r.file = nil
	return size
}

// Path 录像文件路径。
func (r *Recorder) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

// DeleteRecording 删除录像文件。
func (l *Logger) DeleteRecording(sessionID string) error {
	err := os.Remove(l.RecordingFile(sessionID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
