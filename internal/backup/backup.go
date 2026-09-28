// Package backup 实现备份导出与导入（双层加密 + 版本校验 + 事务回滚）。
//
// 双层加密：
//  1. 字段级——凭证在数据库与导出 JSON 中始终是主密钥加密的密文；
//  2. 文件级——整个 JSON 再用备份密码派生的密钥加密。
package backup

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/config"
	"github.com/huoguoji/shellcove/internal/crypto"
)

// 冲突处理策略。
const (
	StrategyOverwrite = "overwrite"
	StrategyMerge     = "merge"
	StrategySkip      = "skip"
)

// EncryptionMeta 文件级加密参数。
type EncryptionMeta struct {
	Alg        string `json:"alg"`
	KDF        string `json:"kdf"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	IV         string `json:"iv"`
	Tag        string `json:"tag"`
}

// Manifest 备份清单（明文部分）。
type Manifest struct {
	Format               string          `json:"format"`
	Version              string          `json:"version"`
	AppVersion           string          `json:"app_version"`
	ExportedAt           string          `json:"exported_at"`
	Checksum             string          `json:"checksum"`
	MasterKeyFingerprint string          `json:"master_key_fingerprint"`
	Counts               map[string]int  `json:"counts"`
	IncludesAudit        bool            `json:"includes_audit"`
	Encryption           *EncryptionMeta `json:"encryption,omitempty"`
}

// Envelope 备份文件结构。
type Envelope struct {
	Manifest Manifest `json:"manifest"`
	Payload  string   `json:"payload"`
}

// Table 可导出的数据表。
type Table struct {
	Name    string
	Columns []string
	// Key 用于冲突处理时判重。
	Key string
	// Audit 为 true 时仅在勾选「包含审计数据」时导出。
	Audit bool
}

// exportTables 导出范围定义（按外键依赖顺序排列）。
var exportTables = []Table{
	{Name: "folders", Key: "id", Columns: []string{"id", "name", "parent_id", "created_at", "updated_at"}},
	{Name: "ssh_infos", Key: "id", Columns: []string{
		"id", "name", "remark", "host", "port", "username", "auth_type",
		"password_enc", "private_key_enc", "passphrase_enc", "folder_id", "key_id",
		"monitor_enabled", "proxy_type", "proxy_host", "proxy_port", "jump_ssh_id",
		"deleted_at", "created_at", "updated_at"}},
	{Name: "command_groups", Key: "id", Columns: []string{"id", "name", "sort_order", "created_at", "updated_at"}},
	{Name: "commands", Key: "id", Columns: []string{
		"id", "group_id", "name", "content", "remark", "sort_order", "created_at", "updated_at"}},
	{Name: "permissions", Key: "id", Columns: []string{
		"id", "user_id", "resource_type", "resource_id", "can_sftp", "can_monitor", "created_at"}},
	{Name: "users", Key: "username", Columns: []string{
		"id", "username", "password_hash", "role", "totp_secret_enc", "totp_enabled",
		"must_change_password", "disabled", "max_sessions", "remark", "last_login_at",
		"created_at", "updated_at"}},
	{Name: "recovery_codes", Key: "id", Columns: []string{"id", "user_id", "code_hash", "used_at", "created_at"}},
	{Name: "settings", Key: "key", Columns: []string{"key", "value", "updated_at"}},
	{Name: "audit_logs", Key: "id", Audit: true, Columns: []string{
		"id", "user_id", "username", "action", "target_type", "target_id", "target_name",
		"detail", "ip", "user_agent", "success", "created_at"}},
	{Name: "sessions", Key: "id", Audit: true, Columns: []string{
		"id", "user_id", "username", "ssh_id", "ssh_name", "title", "client_ip", "status",
		"recording_path", "recording_size", "bytes_in", "bytes_out", "close_reason",
		"started_at", "last_active_at", "ended_at"}},
}

// Service 备份服务。
type Service struct {
	conn   *sql.DB
	cipher *crypto.Cipher
	cfg    *config.Config
	// reEncrypt 由 main 注入，避免包循环依赖（内部调用 sshinfo.Store.ReEncryptAll）。
	reEncrypt func(oldKey []byte) error
}

// NewService 构造备份服务。
func NewService(conn *sql.DB, cipher *crypto.Cipher, cfg *config.Config) *Service {
	return &Service{conn: conn, cipher: cipher, cfg: cfg}
}

// SetReEncryptor 注入凭证重加密函数。
func (s *Service) SetReEncryptor(fn func(oldKey []byte) error) { s.reEncrypt = fn }

// PreviewResult 导出预览。
type PreviewResult struct {
	AppVersion           string         `json:"app_version"`
	BackupFormatVersion  string         `json:"backup_format_version"`
	MasterKeyFingerprint string         `json:"master_key_fingerprint"`
	Counts               map[string]int `json:"counts"`
	Total                int            `json:"total"`
	NothingToExport      bool           `json:"nothing_to_export"`
}

// Preview 统计可导出的数据量。
func (s *Service) Preview(includeAudit bool) (*PreviewResult, error) {
	counts, total, err := s.counts(includeAudit)
	if err != nil {
		return nil, err
	}
	meaningful := total - counts["audit_logs"] - counts["sessions"]
	return &PreviewResult{
		AppVersion:           s.cfg.AppVersion,
		BackupFormatVersion:  config.BackupFormatVersion,
		MasterKeyFingerprint: s.cipher.Fingerprint(),
		Counts:               counts,
		Total:                total,
		NothingToExport:      meaningful <= 0,
	}, nil
}

func (s *Service) counts(includeAudit bool) (map[string]int, int, error) {
	counts := map[string]int{}
	total := 0
	for _, t := range exportTables {
		if t.Audit && !includeAudit {
			continue
		}
		var n int
		if err := s.conn.QueryRow(`SELECT COUNT(*) FROM ` + t.Name).Scan(&n); err != nil {
			return nil, 0, err
		}
		counts[t.Name] = n
		total += n
	}
	return counts, total, nil
}

// Export 生成备份文件内容并写入 w。
func (s *Service) Export(w io.Writer, backupPassword string, includeAudit bool) (*Manifest, error) {
	if !crypto.ValidateBackupPassword(backupPassword) {
		return nil, apperr.ErrWeakBackupPassword
	}

	counts, total, err := s.counts(includeAudit)
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, apperr.ErrNothingToExport
	}

	payload, err := s.buildPayload(includeAudit)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, apperr.ErrEncryptionFailed
	}

	manifest := Manifest{
		Format:               config.BackupFormat,
		Version:              config.BackupFormatVersion,
		AppVersion:           s.cfg.AppVersion,
		ExportedAt:           time.Now().UTC().Format(time.RFC3339),
		Checksum:             crypto.SHA256Hex(raw),
		MasterKeyFingerprint: s.cipher.Fingerprint(),
		Counts:               counts,
		IncludesAudit:        includeAudit,
	}

	salt, err := crypto.RandomBytes(crypto.SaltSize)
	if err != nil {
		return nil, apperr.ErrEncryptionFailed
	}
	// PBKDF2-SHA256(备份密码, salt, 600000) 派生文件级密钥。
	key := crypto.DeriveKey(backupPassword, salt, crypto.BackupIterations, crypto.KeySize)
	fileCipher, err := crypto.New(key)
	if err != nil {
		return nil, apperr.ErrEncryptionFailed
	}
	sealed, err := fileCipher.EncryptBytes(raw)
	if err != nil {
		return nil, apperr.ErrEncryptionFailed
	}
	// EncryptBytes 输出 iv + ciphertext + tag，按规格拆分到 manifest 与 payload。
	const ivLen, tagLen = 12, 16
	if len(sealed) <= ivLen+tagLen {
		return nil, apperr.ErrEncryptionFailed
	}
	iv := sealed[:ivLen]
	tag := sealed[len(sealed)-tagLen:]
	body := sealed[ivLen : len(sealed)-tagLen]

	manifest.Encryption = &EncryptionMeta{
		Alg:        "AES-256-GCM",
		KDF:        "PBKDF2-SHA256",
		Iterations: crypto.BackupIterations,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		IV:         base64.StdEncoding.EncodeToString(iv),
		Tag:        base64.StdEncoding.EncodeToString(tag),
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(Envelope{
		Manifest: manifest,
		Payload:  base64.StdEncoding.EncodeToString(body),
	}); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// Payload 备份数据体。
type Payload struct {
	Format               string            `json:"format"`
	Version              string            `json:"version"`
	ExportedAt           string            `json:"exported_at"`
	MasterKeyFingerprint string            `json:"master_key_fingerprint"`
	Tables               []TableData       `json:"tables"`
	TableChecksums       map[string]string `json:"table_checksums"`
}

// TableData 单表数据。
type TableData struct {
	Name string           `json:"name"`
	Rows []map[string]any `json:"rows"`
}

func (s *Service) buildPayload(includeAudit bool) (*Payload, error) {
	payload := &Payload{
		Format:               config.BackupFormat,
		Version:              config.BackupFormatVersion,
		ExportedAt:           time.Now().UTC().Format(time.RFC3339),
		MasterKeyFingerprint: s.cipher.Fingerprint(),
		TableChecksums:       map[string]string{},
	}
	for _, t := range exportTables {
		if t.Audit && !includeAudit {
			continue
		}
		rows, err := s.readTable(t)
		if err != nil {
			return nil, err
		}
		payload.Tables = append(payload.Tables, TableData{Name: t.Name, Rows: rows})

		tableJSON, err := json.Marshal(rows)
		if err != nil {
			return nil, err
		}
		payload.TableChecksums[t.Name] = crypto.SHA256Hex(tableJSON)
	}
	return payload, nil
}

func (s *Service) readTable(t Table) ([]map[string]any, error) {
	rows, err := s.conn.Query(`SELECT ` + strings.Join(t.Columns, ", ") + ` FROM ` + t.Name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]map[string]any, 0, 16)
	for rows.Next() {
		values := make([]any, len(t.Columns))
		ptrs := make([]any, len(t.Columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		record := make(map[string]any, len(t.Columns))
		for i, col := range t.Columns {
			record[col] = normalizeForJSON(values[i])
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// normalizeForJSON 将驱动返回的字节切片转为字符串，保证 JSON 可读。
func normalizeForJSON(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// InspectResult 预检结果。
type InspectResult struct {
	Format                string         `json:"format"`
	Version               string         `json:"version"`
	AppVersion            string         `json:"app_version"`
	ExportedAt            string         `json:"exported_at"`
	Encrypted             bool           `json:"encrypted"`
	Alg                   string         `json:"alg"`
	KDF                   string         `json:"kdf"`
	Iterations            int            `json:"iterations"`
	Counts                map[string]int `json:"counts"`
	NeedsBackupPassword   bool           `json:"needs_backup_password"`
	NeedsMasterKey        bool           `json:"needs_master_key"`
	MasterKeyFingerprint  string         `json:"master_key_fingerprint"`
	CurrentKeyFingerprint string         `json:"current_key_fingerprint"`
	Compatible            bool           `json:"compatible"`
	Message               string         `json:"message"`
}

// Inspect 读取备份文件的明文清单并校验版本兼容性。
func (s *Service) Inspect(data []byte) (*InspectResult, error) {
	envelope, err := decodeEnvelope(data)
	if err != nil {
		return nil, err
	}
	if err := s.checkCompatibility(envelope.Manifest); err != nil {
		return nil, err
	}

	res := &InspectResult{
		Format:                envelope.Manifest.Format,
		Version:               envelope.Manifest.Version,
		AppVersion:            envelope.Manifest.AppVersion,
		ExportedAt:            envelope.Manifest.ExportedAt,
		Counts:                envelope.Manifest.Counts,
		MasterKeyFingerprint:  envelope.Manifest.MasterKeyFingerprint,
		CurrentKeyFingerprint: s.cipher.Fingerprint(),
		Compatible:            true,
	}
	if envelope.Manifest.Encryption != nil {
		res.Encrypted = true
		res.NeedsBackupPassword = true
		res.Alg = envelope.Manifest.Encryption.Alg
		res.KDF = envelope.Manifest.Encryption.KDF
		res.Iterations = envelope.Manifest.Encryption.Iterations
	}
	res.NeedsMasterKey = envelope.Manifest.MasterKeyFingerprint != "" &&
		envelope.Manifest.MasterKeyFingerprint != s.cipher.Fingerprint()

	switch {
	case res.NeedsMasterKey && res.NeedsBackupPassword:
		res.Message = "备份使用了不同的主密钥，导入时需要同时提供备份密码与原主密钥"
	case res.NeedsMasterKey:
		res.Message = "备份使用了不同的主密钥，导入时需要提供原主密钥"
	case res.NeedsBackupPassword:
		res.Message = "需要提供导出时设置的备份密码"
	default:
		res.Message = "备份文件未加密，可直接导入"
	}
	return res, nil
}

func decodeEnvelope(data []byte) (*Envelope, error) {
	if len(data) == 0 {
		return nil, apperr.ErrInvalidFileFormat
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, apperr.ErrInvalidFileFormat
	}
	if envelope.Manifest.Format != config.BackupFormat || envelope.Payload == "" {
		return nil, apperr.ErrInvalidFileFormat
	}
	return &envelope, nil
}

func (s *Service) checkCompatibility(m Manifest) error {
	if m.Version != config.BackupFormatVersion {
		return apperr.ErrUnsupportedVersion.WithMessage(
			fmt.Sprintf("备份格式版本 %s 与当前程序（%s）不兼容", m.Version, config.BackupFormatVersion))
	}
	if compareVersions(m.AppVersion, s.cfg.AppVersion) > 0 {
		return apperr.ErrUnsupportedVersion.WithMessage(
			fmt.Sprintf("备份由更高版本程序（%s）生成，当前程序为 %s", m.AppVersion, s.cfg.AppVersion))
	}
	return nil
}

// compareVersions 比较形如 1.2.3 的版本号，a > b 返回 1。
func compareVersions(a, b string) int {
	parse := func(v string) []int {
		parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(v, "v")), ".")
		out := make([]int, 0, len(parts))
		for _, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil {
				n = 0
			}
			out = append(out, n)
		}
		return out
	}
	av, bv := parse(a), parse(b)
	for i := 0; i < len(av) || i < len(bv); i++ {
		var x, y int
		if i < len(av) {
			x = av[i]
		}
		if i < len(bv) {
			y = bv[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}
