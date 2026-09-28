// Package user 负责用户实体的增删改查与初始管理员种子。
package user

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/crypto"
	"github.com/huoguoji/shellcove/internal/db"
)

// 角色常量。
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User 对外暴露的用户信息（绝不包含密码哈希与 TOTP 密钥）。
type User struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	Remark             string `json:"remark"`
	TOTPEnabled        bool   `json:"totp_enabled"`
	MustChangePassword bool   `json:"must_change_password"`
	Disabled           bool   `json:"disabled"`
	MaxSessions        int    `json:"max_sessions"`
	LastLoginAt        string `json:"last_login_at"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// IsAdmin 是否为管理员。
func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// Store 用户数据访问层。
type Store struct {
	conn *sql.DB
}

// NewStore 构造用户 Store。
func NewStore(conn *sql.DB) *Store { return &Store{conn: conn} }

const userColumns = `id, username, role, COALESCE(remark, ''), totp_enabled, must_change_password, disabled, max_sessions,
	COALESCE(last_login_at, ''), created_at, updated_at`

func scanUser(sc interface {
	Scan(dest ...any) error
}) (*User, error) {
	var u User
	err := sc.Scan(&u.ID, &u.Username, &u.Role, &u.Remark, &u.TOTPEnabled, &u.MustChangePassword,
		&u.Disabled, &u.MaxSessions, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByID 按 ID 查询用户。
func (s *Store) GetByID(id string) (*User, error) {
	row := s.conn.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if err == sql.ErrNoRows {
		return nil, apperr.ErrNotFound.WithMessage("用户不存在")
	}
	return u, err
}

// GetByUsername 按用户名查询用户。
func (s *Store) GetByUsername(username string) (*User, error) {
	row := s.conn.QueryRow(`SELECT `+userColumns+` FROM users WHERE username = ?`, username)
	u, err := scanUser(row)
	if err == sql.ErrNoRows {
		return nil, apperr.ErrNotFound.WithMessage("用户名或密码错误")
	}
	return u, err
}

// List 返回全部用户。
func (s *Store) List() ([]*User, error) {
	rows, err := s.conn.Query(`SELECT ` + userColumns + ` FROM users ORDER BY role, username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*User, 0, 8)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// PasswordHash 读取密码哈希（仅认证流程使用）。
func (s *Store) PasswordHash(id string) (string, error) {
	var hash string
	err := s.conn.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, id).Scan(&hash)
	return hash, err
}

// Create 创建用户。
func (s *Store) Create(username, password, role, remark string, maxSessions int, mustChange bool) (*User, error) {
	username = normalizeUsername(username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if !crypto.ValidatePanelPassword(password) {
		return nil, apperr.ErrWeakPassword.WithMessage(
			fmt.Sprintf("密码至少 %d 位，且必须同时包含字母和数字", crypto.MinPanelPasswordLen))
	}
	if role != RoleAdmin && role != RoleUser {
		return nil, apperr.ErrBadRequest.WithMessage("角色不合法")
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, err
	}

	now := db.NowStr()
	u := &User{
		ID:                 uuid.NewString(),
		Username:           username,
		Role:               role,
		Remark:             remark,
		MustChangePassword: mustChange,
		MaxSessions:        maxSessions,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	_, err = s.conn.Exec(
		`INSERT INTO users (id, username, password_hash, role, remark, must_change_password, max_sessions, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, hash, u.Role, u.Remark, u.MustChangePassword, u.MaxSessions, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperr.ErrConflict.WithMessage("用户名已存在")
		}
		return nil, err
	}
	return u, nil
}

// UpdateProfile 更新备注与并发会话上限。
func (s *Store) UpdateProfile(id, remark string, maxSessions int) error {
	_, err := s.conn.Exec(`UPDATE users SET remark = ?, max_sessions = ?, updated_at = ? WHERE id = ?`,
		remark, maxSessions, db.NowStr(), id)
	return err
}

// SetPassword 更新密码哈希与强制改密标记。
func (s *Store) SetPassword(id, password string, mustChange bool) error {
	if !crypto.ValidatePanelPassword(password) {
		return apperr.ErrWeakPassword.WithMessage(
			fmt.Sprintf("密码至少 %d 位，且必须同时包含字母和数字", crypto.MinPanelPasswordLen))
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.conn.Exec(`UPDATE users SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ?`,
		hash, mustChange, db.NowStr(), id)
	return err
}

// SetPasswordHash 直接写入哈希（用于导入恢复）。
func (s *Store) SetPasswordHash(id, hash string, mustChange bool) error {
	_, err := s.conn.Exec(`UPDATE users SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ?`,
		hash, mustChange, db.NowStr(), id)
	return err
}

// SetDisabled 启用/禁用账号。
func (s *Store) SetDisabled(id string, disabled bool) error {
	_, err := s.conn.Exec(`UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?`, disabled, db.NowStr(), id)
	return err
}

// SetRole 调整角色。
func (s *Store) SetRole(id, role string) error {
	_, err := s.conn.Exec(`UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, role, db.NowStr(), id)
	return err
}

// Delete 删除用户，同时清理其权限与恢复码。
func (s *Store) Delete(id string) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`DELETE FROM permissions WHERE user_id = ?`,
		`DELETE FROM recovery_codes WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		if _, err := tx.Exec(stmt, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TouchLogin 记录最近登录时间。
func (s *Store) TouchLogin(id string) error {
	_, err := s.conn.Exec(`UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`,
		db.NowStr(), db.NowStr(), id)
	return err
}

// CountAdmins 返回启用状态的管理员数量。
func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.conn.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ? AND disabled = 0`, RoleAdmin).Scan(&n)
	return n, err
}

// TOTPSecret 返回加密的 TOTP 密钥与启用状态。
func (s *Store) TOTPSecret(id string) (string, bool, error) {
	var enc sql.NullString
	var enabled bool
	err := s.conn.QueryRow(`SELECT totp_secret_enc, totp_enabled FROM users WHERE id = ?`, id).Scan(&enc, &enabled)
	if err != nil {
		return "", false, err
	}
	return db.NullStr(enc), enabled, nil
}

// SetTOTP 写入加密的 TOTP 密钥并设置启用状态。
func (s *Store) SetTOTP(id, encSecret string, enabled bool) error {
	_, err := s.conn.Exec(`UPDATE users SET totp_secret_enc = ?, totp_enabled = ?, updated_at = ? WHERE id = ?`,
		encSecret, enabled, db.NowStr(), id)
	return err
}

// ClearTOTP 关闭 2FA 并清除密钥。
func (s *Store) ClearTOTP(id string) error {
	_, err := s.conn.Exec(`UPDATE users SET totp_secret_enc = NULL, totp_enabled = 0, updated_at = ? WHERE id = ?`,
		db.NowStr(), id)
	return err
}

// ValidateUsername 校验用户名格式。
func ValidateUsername(name string) error {
	if len(name) < 3 || len(name) > 32 {
		return apperr.ErrBadRequest.WithMessage("用户名长度需为 3-32 个字符")
	}
	for _, r := range name {
		if !(r == '_' || r == '-' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return apperr.ErrBadRequest.WithMessage("用户名只能包含字母、数字、下划线、短横线和点")
		}
	}
	return nil
}

func normalizeUsername(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "UNIQUE CONSTRAINT FAILED") || strings.Contains(msg, "CONSTRAINT FAILED")
}

// EnsureInitialAdmin 首次启动时创建管理员账号。
// 密码优先取 ADMIN_PASSWORD 环境变量，否则随机生成并返回给调用方打印一次。
func (s *Store) EnsureInitialAdmin(adminPasswordEnv string) (generated string, created bool, err error) {
	var count int
	if err := s.conn.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return "", false, err
	}
	if count > 0 {
		return "", false, nil
	}

	password := adminPasswordEnv
	if password == "" {
		password, err = crypto.RandomHex(9)
		if err != nil {
			return "", false, err
		}
		generated = password
	}
	// 生成密码可能不含数字/字母组合，直接写库以绕过强度校验。
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return "", false, err
	}
	now := db.NowStr()
	_, err = s.conn.Exec(
		`INSERT INTO users (id, username, password_hash, role, remark, must_change_password, max_sessions, created_at, updated_at)
		 VALUES (?, 'admin', ?, ?, '初始管理员', 1, 0, ?, ?)`,
		uuid.NewString(), hash, RoleAdmin, now, now)
	if err != nil {
		return "", false, err
	}
	return generated, true, nil
}
