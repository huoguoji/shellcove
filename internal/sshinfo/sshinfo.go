// Package sshinfo 管理 SSH 信息的增删改查、凭证加密存储与解密读取。
//
// 安全约定：
//   - 列表中只返回 has_* 标记与提示文案，绝不返回明文；
//   - 编辑时敏感字段留空表示保留原密文；
//   - 明文只在 ssh 连接与「二次验证后查看凭证」场景内存中短暂出现。
package sshinfo

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/crypto"
	"github.com/huoguoji/shellcove/internal/db"
	"github.com/huoguoji/shellcove/internal/folder"
)

// 认证方式。
const (
	AuthPassword            = "password"
	AuthPublicKey           = "publickey"
	AuthKeyboardInteractive = "keyboard-interactive"
)

// 代理类型。
const (
	ProxyNone   = ""
	ProxySocks5 = "socks5"
	ProxyHTTP   = "http"
)

// Info SSH 信息（对外结构，永不含明文凭证）。
type Info struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Remark   string `json:"remark"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	AuthType string `json:"auth_type"`

	FolderID       *string `json:"folder_id"`
	KeyID          int     `json:"key_id"`
	MonitorEnabled bool    `json:"monitor_enabled"`

	ProxyType string `json:"proxy_type"`
	ProxyHost string `json:"proxy_host"`
	ProxyPort int    `json:"proxy_port"`

	JumpSSHID *string `json:"jump_ssh_id"`

	DeletedAt *string `json:"deleted_at"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`

	// 下列字段仅用于前端展示，不含任何明文。
	HasPassword    bool   `json:"has_password"`
	HasPrivateKey  bool   `json:"has_private_key"`
	HasPassphrase  bool   `json:"has_passphrase"`
	PasswordHint   string `json:"password_hint"`
	PrivateKeyHint string `json:"private_key_hint"`
	PassphraseHint string `json:"passphrase_hint"`

	FolderName string `json:"folder_name"`
	JumpName   string `json:"jump_name"`
}

// Input 新建/编辑入参。
type Input struct {
	Name           string  `json:"name"`
	Remark         string  `json:"remark"`
	Host           string  `json:"host"`
	Port           int     `json:"port"`
	Username       string  `json:"username"`
	AuthType       string  `json:"auth_type"`
	Password       string  `json:"password"`
	PrivateKey     string  `json:"private_key"`
	Passphrase     string  `json:"passphrase"`
	FolderID       *string `json:"folder_id"`
	MonitorEnabled bool    `json:"monitor_enabled"`
	ProxyType      string  `json:"proxy_type"`
	ProxyHost      string  `json:"proxy_host"`
	ProxyPort      int     `json:"proxy_port"`
	JumpSSHID      *string `json:"jump_ssh_id"`

	// 显式清空敏感字段（留空默认为「不修改」）。
	ClearPassword   bool `json:"clear_password"`
	ClearPrivateKey bool `json:"clear_private_key"`
	ClearPassphrase bool `json:"clear_passphrase"`
}

// ListFilter 列表查询条件。
type ListFilter struct {
	Search         string
	FolderID       string
	Recursive      bool
	IncludeTrashed bool
	OnlyTrashed    bool
}

// Credentials 解密后的连接凭证（仅内存态）。
type Credentials struct {
	ID       string
	Name     string
	Host     string
	Port     int
	Username string
	AuthType string

	Password   string
	PrivateKey string
	Passphrase string

	ProxyType string
	ProxyHost string
	ProxyPort int

	MonitorEnabled bool

	// Jump 跳板机（若配置）。
	Jump *Credentials
}

// Secret 明文凭证集合，用于「查看凭证」接口。
type Secret struct {
	Password    string `json:"password,omitempty"`
	PrivateKey  string `json:"private_key,omitempty"`
	Passphrase  string `json:"passphrase,omitempty"`
	AuthType    string `json:"auth_type"`
	Username    string `json:"username"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	HasPassword bool   `json:"has_password"`
	HasKey      bool   `json:"has_private_key"`
}

// Store SSH 信息数据访问层。
type Store struct {
	conn     *sql.DB
	cipher   *crypto.Cipher
	folders  *folder.Store
}

// NewStore 构造 Store。
func NewStore(conn *sql.DB, cipher *crypto.Cipher) *Store {
	return &Store{conn: conn, cipher: cipher, folders: folder.NewStore(conn)}
}

const infoColumns = `
	i.id, i.name, COALESCE(i.remark, ''), i.host, i.port, i.username, i.auth_type,
	i.password_enc, i.private_key_enc, i.passphrase_enc, i.folder_id, i.key_id, i.monitor_enabled,
	COALESCE(i.proxy_type, ''), COALESCE(i.proxy_host, ''), COALESCE(i.proxy_port, 0),
	i.jump_ssh_id, i.deleted_at, i.created_at, i.updated_at,
	COALESCE(f.name, ''), COALESCE(j.name, '')`

func scanInfo(sc interface{ Scan(dest ...any) error }) (*Info, error) {
	info := &Info{}
	var (
		passwordEnc, keyEnc, passEnc sql.NullString
		folderID, jumpID, deletedAt   sql.NullString
		folderName, jumpName          sql.NullString
	)
	err := sc.Scan(
		&info.ID, &info.Name, &info.Remark, &info.Host, &info.Port, &info.Username, &info.AuthType,
		&passwordEnc, &keyEnc, &passEnc, &folderID, &info.KeyID, &info.MonitorEnabled,
		&info.ProxyType, &info.ProxyHost, &info.ProxyPort,
		&jumpID, &deletedAt, &info.CreatedAt, &info.UpdatedAt,
		&folderName, &jumpName,
	)
	if err != nil {
		return nil, err
	}
	info.FolderID = db.NullStrPtr(folderID)
	info.JumpSSHID = db.NullStrPtr(jumpID)
	info.DeletedAt = db.NullStrPtr(deletedAt)
	info.FolderName = db.NullStr(folderName)
	info.JumpName = db.NullStr(jumpName)

	info.HasPassword = passwordEnc.Valid && passwordEnc.String != ""
	info.HasPrivateKey = keyEnc.Valid && keyEnc.String != ""
	info.HasPassphrase = passEnc.Valid && passEnc.String != ""
	info.PasswordHint = secretHint(info.HasPassword)
	info.PrivateKeyHint = secretHint(info.HasPrivateKey)
	info.PassphraseHint = secretHint(info.HasPassphrase)
	return info, nil
}

func secretHint(has bool) string {
	if has {
		return "已配置（留空表示不修改）"
	}
	return "未配置"
}

// List 按条件查询。
func (s *Store) List(filter ListFilter) ([]*Info, error) {
	var where []string
	var args []any

	switch {
	case filter.OnlyTrashed:
		where = append(where, `i.deleted_at IS NOT NULL`)
	case filter.IncludeTrashed:
	default:
		where = append(where, `i.deleted_at IS NULL`)
	}

	if term := strings.TrimSpace(filter.Search); term != "" {
		like := "%" + term + "%"
		where = append(where, `(i.name LIKE ? OR i.host LIKE ? OR COALESCE(i.remark, '') LIKE ? OR i.username LIKE ?)`)
		args = append(args, like, like, like, like)
	}

	if fid := strings.TrimSpace(filter.FolderID); fid != "" {
		if filter.Recursive {
			ids, err := s.folders.SubtreeIDs(fid)
			if err != nil {
				return nil, err
			}
			ph := make([]string, 0, len(ids))
			for _, id := range ids {
				ph = append(ph, "?")
				args = append(args, id)
			}
			where = append(where, `i.folder_id IN (`+strings.Join(ph, ",")+`)`)
		} else {
			where = append(where, `i.folder_id = ?`)
			args = append(args, fid)
		}
	}

	query := `SELECT ` + infoColumns + `
		FROM ssh_infos i
		LEFT JOIN folders f ON f.id = i.folder_id
		LEFT JOIN ssh_infos j ON j.id = i.jump_ssh_id`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY i.name`

	rows, err := s.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Info, 0, 16)
	for rows.Next() {
		info, err := scanInfo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

// Get 按 ID 查询详情。
func (s *Store) Get(id string) (*Info, error) {
	row := s.conn.QueryRow(`SELECT `+infoColumns+`
		FROM ssh_infos i
		LEFT JOIN folders f ON f.id = i.folder_id
		LEFT JOIN ssh_infos j ON j.id = i.jump_ssh_id
		WHERE i.id = ?`, id)
	info, err := scanInfo(row)
	if err == sql.ErrNoRows {
		return nil, apperr.ErrNotFound.WithMessage("SSH 信息不存在")
	}
	return info, err
}

// Create 新建 SSH 信息。
func (s *Store) Create(in *Input) (*Info, error) {
	if err := s.validate(in, ""); err != nil {
		return nil, err
	}
	if in.AuthType == AuthPassword && strings.TrimSpace(in.Password) == "" {
		return nil, apperr.ErrBadRequest.WithMessage("密码认证必须填写密码")
	}
	if in.AuthType == AuthPublicKey && strings.TrimSpace(in.PrivateKey) == "" {
		return nil, apperr.ErrBadRequest.WithMessage("公钥认证必须填写私钥")
	}

	passwordEnc, err := s.cipher.EncryptString(in.Password)
	if err != nil {
		return nil, apperr.ErrEncryptionFailed
	}
	keyEnc, err := s.cipher.EncryptString(in.PrivateKey)
	if err != nil {
		return nil, apperr.ErrEncryptionFailed
	}
	passEnc, err := s.cipher.EncryptString(in.Passphrase)
	if err != nil {
		return nil, apperr.ErrEncryptionFailed
	}

	now := db.NowStr()
	id := uuid.NewString()
	_, err = s.conn.Exec(
		`INSERT INTO ssh_infos (id, name, remark, host, port, username, auth_type,
			password_enc, private_key_enc, passphrase_enc, folder_id, key_id, monitor_enabled,
			proxy_type, proxy_host, proxy_port, jump_ssh_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, strings.TrimSpace(in.Name), strings.TrimSpace(in.Remark), strings.TrimSpace(in.Host),
		in.Port, strings.TrimSpace(in.Username), in.AuthType,
		nullStr(passwordEnc), nullStr(keyEnc), nullStr(passEnc),
		nullable(in.FolderID), s.cipher.KeyID(), in.MonitorEnabled,
		nullableStr(in.ProxyType), strings.TrimSpace(in.ProxyHost), nullableInt(in.ProxyPort),
		nullable(in.JumpSSHID), now, now)
	if err != nil {
		return nil, err
	}
	return s.Get(id)
}

// Update 编辑；敏感字段留空表示保留原密文。
func (s *Store) Update(id string, in *Input) (*Info, error) {
	current, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if err := s.validate(in, id); err != nil {
		return nil, err
	}

	updates := []string{
		`name = ?`, `remark = ?`, `host = ?`, `port = ?`, `username = ?`, `auth_type = ?`,
		`folder_id = ?`, `monitor_enabled = ?`, `proxy_type = ?`, `proxy_host = ?`, `proxy_port = ?`,
		`jump_ssh_id = ?`, `updated_at = ?`,
	}
	args := []any{
		strings.TrimSpace(in.Name), strings.TrimSpace(in.Remark), strings.TrimSpace(in.Host),
		in.Port, strings.TrimSpace(in.Username), in.AuthType,
		nullable(in.FolderID), in.MonitorEnabled,
		nullableStr(in.ProxyType), strings.TrimSpace(in.ProxyHost), nullableInt(in.ProxyPort),
		nullable(in.JumpSSHID), db.NowStr(),
	}

	// 敏感字段：留空 = 不修改；有值 = 加密覆盖；Clear* = 清空。
	if in.ClearPassword {
		updates = append(updates, `password_enc = NULL`)
	} else if strings.TrimSpace(in.Password) != "" {
		enc, err := s.cipher.EncryptString(in.Password)
		if err != nil {
			return nil, apperr.ErrEncryptionFailed
		}
		updates = append(updates, `password_enc = ?`)
		args = append(args, enc)
	}

	if in.ClearPrivateKey {
		updates = append(updates, `private_key_enc = NULL`)
	} else if strings.TrimSpace(in.PrivateKey) != "" {
		enc, err := s.cipher.EncryptString(in.PrivateKey)
		if err != nil {
			return nil, apperr.ErrEncryptionFailed
		}
		updates = append(updates, `private_key_enc = ?`)
		args = append(args, enc)
	}

	if in.ClearPassphrase {
		updates = append(updates, `passphrase_enc = NULL`)
	} else if strings.TrimSpace(in.Passphrase) != "" {
		enc, err := s.cipher.EncryptString(in.Passphrase)
		if err != nil {
			return nil, apperr.ErrEncryptionFailed
		}
		updates = append(updates, `passphrase_enc = ?`)
		args = append(args, enc)
	}

	// 交换机密认证方式后必须已有对应凭证，避免产生无法连接的记录。
	hasPassword := current.HasPassword
	if in.ClearPassword {
		hasPassword = false
	} else if strings.TrimSpace(in.Password) != "" {
		hasPassword = true
	}
	hasKey := current.HasPrivateKey
	if in.ClearPrivateKey {
		hasKey = false
	} else if strings.TrimSpace(in.PrivateKey) != "" {
		hasKey = true
	}
	switch in.AuthType {
	case AuthPassword, AuthKeyboardInteractive:
		if !hasPassword {
			return nil, apperr.ErrBadRequest.WithMessage("当前认证方式需要密码，请填写密码")
		}
	case AuthPublicKey:
		if !hasKey {
			return nil, apperr.ErrBadRequest.WithMessage("公钥认证需要私钥，请填写私钥")
		}
	}

	args = append(args, id)
	if _, err := s.conn.Exec(`UPDATE ssh_infos SET `+strings.Join(updates, ", ")+` WHERE id = ?`, args...); err != nil {
		return nil, err
	}
	return s.Get(id)
}

func (s *Store) validate(in *Input, selfID string) error {
	if strings.TrimSpace(in.Name) == "" {
		return apperr.ErrBadRequest.WithMessage("名称不能为空")
	}
	if len([]rune(in.Name)) > 128 {
		return apperr.ErrBadRequest.WithMessage("名称过长")
	}
	if strings.TrimSpace(in.Host) == "" {
		return apperr.ErrBadRequest.WithMessage("主机地址不能为空")
	}
	if strings.TrimSpace(in.Username) == "" {
		return apperr.ErrBadRequest.WithMessage("用户名不能为空")
	}
	if in.Port <= 0 || in.Port > 65535 {
		return apperr.ErrBadRequest.WithMessage("端口需在 1-65535 之间")
	}
	switch in.AuthType {
	case AuthPassword, AuthPublicKey, AuthKeyboardInteractive:
	default:
		return apperr.ErrBadRequest.WithMessage("认证方式不合法")
	}
	switch in.ProxyType {
	case ProxyNone, ProxySocks5, ProxyHTTP:
	default:
		return apperr.ErrBadRequest.WithMessage("代理类型不合法")
	}
	if in.ProxyType != ProxyNone {
		if strings.TrimSpace(in.ProxyHost) == "" || in.ProxyPort <= 0 || in.ProxyPort > 65535 {
			return apperr.ErrBadRequest.WithMessage("启用代理时必须填写代理地址与端口")
		}
	}

	if in.FolderID != nil && *in.FolderID != "" {
		if _, err := s.folders.Get(*in.FolderID); err != nil {
			return apperr.ErrBadRequest.WithMessage("所属文件夹不存在")
		}
	} else {
		in.FolderID = nil
	}

	if in.JumpSSHID != nil && *in.JumpSSHID != "" {
		if selfID != "" && *in.JumpSSHID == selfID {
			return apperr.ErrBadRequest.WithMessage("跳板机不能是自己")
		}
		if _, err := s.Get(*in.JumpSSHID); err != nil {
			return apperr.ErrBadRequest.WithMessage("跳板机信息不存在")
		}
		if err := s.assertNoJumpLoop(selfID, *in.JumpSSHID); err != nil {
			return err
		}
	} else {
		in.JumpSSHID = nil
	}
	return nil
}

// assertNoJumpLoop 检查跳板机链路是否形成环。
func (s *Store) assertNoJumpLoop(selfID, jumpID string) error {
	seen := map[string]bool{}
	cur := jumpID
	for cur != "" {
		if selfID != "" && cur == selfID {
			return apperr.ErrBadRequest.WithMessage("跳板机链路存在循环引用")
		}
		if seen[cur] {
			return apperr.ErrBadRequest.WithMessage("跳板机链路存在循环引用")
		}
		seen[cur] = true

		var next sql.NullString
		if err := s.conn.QueryRow(`SELECT jump_ssh_id FROM ssh_infos WHERE id = ?`, cur).Scan(&next); err != nil {
			return nil
		}
		cur = db.NullStr(next)
	}
	return nil
}

// SoftDelete 移入回收站。
func (s *Store) SoftDelete(id string) error {
	res, err := s.conn.Exec(`UPDATE ssh_infos SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		db.NowStr(), db.NowStr(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.ErrNotFound.WithMessage("SSH 信息不存在或已在回收站中")
	}
	return nil
}

// Restore 从回收站恢复。
func (s *Store) Restore(id string) error {
	res, err := s.conn.Exec(`UPDATE ssh_infos SET deleted_at = NULL, updated_at = ? WHERE id = ? AND deleted_at IS NOT NULL`,
		db.NowStr(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.ErrNotFound.WithMessage("回收站中不存在该记录")
	}
	return nil
}

// Purge 彻底删除，同时解除其他记录对它的跳板机引用。
func (s *Store) Purge(id string) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE ssh_infos SET jump_ssh_id = NULL WHERE jump_ssh_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM permissions WHERE resource_type = 'ssh' AND resource_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM ssh_infos WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.ErrNotFound
	}
	return tx.Commit()
}

// EmptyTrash 清空回收站。
func (s *Store) EmptyTrash() (int, error) {
	rows, err := s.conn.Query(`SELECT id FROM ssh_infos WHERE deleted_at IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	removed := 0
	for _, id := range ids {
		if err := s.Purge(id); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// CleanupExpired 清理超过保留天数的回收站记录。
func (s *Store) CleanupExpired(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	threshold := db.TimeStr(nowAddDays(-retentionDays))
	rows, err := s.conn.Query(`SELECT id FROM ssh_infos WHERE deleted_at IS NOT NULL AND deleted_at < ?`, threshold)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	removed := 0
	for _, id := range ids {
		if err := s.Purge(id); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// CountAll 统计非回收站记录数。
func (s *Store) CountAll() (int, error) {
	var n int
	err := s.conn.QueryRow(`SELECT COUNT(*) FROM ssh_infos WHERE deleted_at IS NULL`).Scan(&n)
	return n, err
}

// CountTrashed 统计回收站记录数。
func (s *Store) CountTrashed() (int, error) {
	var n int
	err := s.conn.QueryRow(`SELECT COUNT(*) FROM ssh_infos WHERE deleted_at IS NOT NULL`).Scan(&n)
	return n, err
}

// DisplayLabel 返回用于审计日志的展示名。
func (i *Info) DisplayLabel() string {
	if i == nil {
		return ""
	}
	return fmt.Sprintf("%s (%s@%s:%d)", i.Name, i.Username, i.Host, i.Port)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullable(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func nullableStr(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

func nullableInt(n int) any {
	if n <= 0 {
		return nil
	}
	return n
}
