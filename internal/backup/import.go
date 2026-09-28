package backup

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/config"
	"github.com/huoguoji/shellcove/internal/crypto"
)

// ImportRequest 导入请求。
type ImportRequest struct {
	Data           []byte
	BackupPassword string
	MasterKeyHex   string
	Strategy       string
	IncludeAudit   bool
	// SkipPreImportBackup 为 true 时跳过导入前快照（仅用于测试）。
	SkipPreImportBackup bool
}

// ImportResult 导入结果。
type ImportResult struct {
	Strategy      string         `json:"strategy"`
	Imported      map[string]int `json:"imported"`
	Skipped       map[string]int `json:"skipped"`
	ReEncrypted   bool           `json:"re_encrypted"`
	PreImportPath string         `json:"pre_import_backup"`
}

// Import 执行导入：解密 → 校验 → 预备份 → 事务写入 → 失败回滚。
func (s *Service) Import(req ImportRequest) (*ImportResult, error) {
	envelope, err := decodeEnvelope(req.Data)
	if err != nil {
		return nil, err
	}
	if err := s.checkCompatibility(envelope.Manifest); err != nil {
		return nil, err
	}

	strategy := strings.ToLower(strings.TrimSpace(req.Strategy))
	switch strategy {
	case StrategyOverwrite, StrategyMerge, StrategySkip:
	case "":
		strategy = StrategyMerge
	default:
		return nil, apperr.ErrBadRequest.WithMessage("冲突处理策略不合法")
	}

	rawPayload, err := s.decryptPayload(envelope, req.BackupPassword)
	if err != nil {
		return nil, err
	}
	if envelope.Manifest.Checksum != "" && crypto.SHA256Hex(rawPayload) != envelope.Manifest.Checksum {
		return nil, apperr.ErrChecksumMismatch
	}

	var payload Payload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return nil, apperr.ErrInvalidFileFormat
	}
	if payload.Version != "" && payload.Version != config.BackupFormatVersion {
		return nil, apperr.ErrUnsupportedVersion
	}
	if err := verifyTableChecksums(payload); err != nil {
		return nil, err
	}

	// 主密钥不一致时要求提供原主密钥用于重加密。
	needReEncrypt := payload.MasterKeyFingerprint != "" && payload.MasterKeyFingerprint != s.cipher.Fingerprint()
	var oldKey []byte
	if needReEncrypt {
		if strings.TrimSpace(req.MasterKeyHex) == "" {
			return nil, apperr.ErrWrongMasterKey.WithMessage("备份使用了不同的主密钥，请提供原主密钥")
		}
		oldKey, err = crypto.ParseKey(req.MasterKeyHex)
		if err != nil {
			return nil, apperr.ErrWrongMasterKey
		}
		if crypto.SHA256Hex(oldKey)[:16] != payload.MasterKeyFingerprint {
			return nil, apperr.ErrWrongMasterKey
		}
		if s.reEncrypt == nil {
			return nil, apperr.ErrInternal
		}
	}

	result := &ImportResult{
		Strategy: strategy,
		Imported: map[string]int{},
		Skipped:  map[string]int{},
	}

	if !req.SkipPreImportBackup {
		path, err := s.SavePreImportBackup()
		if err != nil {
			return nil, err
		}
		result.PreImportPath = path
	}

	// 事务内写入，任何一步失败都整体回滚，不留半成品。
	tx, err := s.conn.Begin()
	if err != nil {
		return nil, wrapDBErr(err)
	}
	if err := applyPayload(tx, payload, strategy, req.IncludeAudit, result); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return nil, wrapDBErr(err)
	}

	// 导入成功后按需用原主密钥重加密凭证，使其匹配当前主密钥。
	if needReEncrypt {
		if err := s.reEncrypt(oldKey); err != nil {
			return nil, apperr.ErrDecryptionFailed.WithMessage("凭证重加密失败：" + err.Error())
		}
		result.ReEncrypted = true
	}
	return result, nil
}

func (s *Service) decryptPayload(envelope *Envelope, backupPassword string) ([]byte, error) {
	body, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return nil, apperr.ErrInvalidFileFormat
	}
	if envelope.Manifest.Encryption == nil {
		return body, nil
	}
	if strings.TrimSpace(backupPassword) == "" {
		return nil, apperr.ErrWrongBackupPassword
	}

	meta := envelope.Manifest.Encryption
	salt, err := base64.StdEncoding.DecodeString(meta.Salt)
	if err != nil {
		return nil, apperr.ErrInvalidFileFormat
	}
	iv, err := base64.StdEncoding.DecodeString(meta.IV)
	if err != nil {
		return nil, apperr.ErrInvalidFileFormat
	}
	tag, err := base64.StdEncoding.DecodeString(meta.Tag)
	if err != nil {
		return nil, apperr.ErrInvalidFileFormat
	}

	iterations := meta.Iterations
	if iterations <= 0 {
		iterations = crypto.BackupIterations
	}
	fileCipher, err := crypto.New(crypto.DeriveKey(backupPassword, salt, iterations, crypto.KeySize))
	if err != nil {
		return nil, apperr.ErrDecryptionFailed
	}

	sealed := make([]byte, 0, len(iv)+len(body)+len(tag))
	sealed = append(sealed, iv...)
	sealed = append(sealed, body...)
	sealed = append(sealed, tag...)

	plain, err := fileCipher.DecryptBytes(sealed)
	if err != nil {
		// 密码类错误统一提示「密码错误」，不区分细节。
		return nil, apperr.ErrWrongBackupPassword
	}
	return plain, nil
}

func verifyTableChecksums(payload Payload) error {
	for _, t := range payload.Tables {
		want, ok := payload.TableChecksums[t.Name]
		if !ok || want == "" {
			continue
		}
		raw, err := json.Marshal(t.Rows)
		if err != nil {
			return apperr.ErrInvalidFileFormat
		}
		if crypto.SHA256Hex(raw) != want {
			return apperr.ErrChecksumMismatch.WithMessage("数据表 " + t.Name + " 校验失败")
		}
	}
	return nil
}

func applyPayload(tx *sql.Tx, payload Payload, strategy string, includeAudit bool, result *ImportResult) error {
	for _, data := range payload.Tables {
		table, ok := findTable(data.Name)
		if !ok {
			continue
		}
		if table.Audit && !includeAudit {
			continue
		}
		for _, row := range data.Rows {
			inserted, err := upsertRow(tx, table, row, strategy)
			if err != nil {
				return err
			}
			if inserted {
				result.Imported[table.Name]++
			} else {
				result.Skipped[table.Name]++
			}
		}
	}
	return nil
}

func findTable(name string) (Table, bool) {
	for _, t := range exportTables {
		if t.Name == name {
			return t, true
		}
	}
	return Table{}, false
}

func upsertRow(tx *sql.Tx, table Table, row map[string]any, strategy string) (bool, error) {
	keyValue := normalizeValue(row[table.Key])
	if keyValue == nil {
		// 缺少关键字段的行直接跳过，避免写入脏数据。
		return false, nil
	}

	columns := make([]string, 0, len(table.Columns))
	values := make([]any, 0, len(table.Columns))
	placeholders := make([]string, 0, len(table.Columns))
	for _, col := range table.Columns {
		if _, ok := row[col]; !ok {
			continue
		}
		columns = append(columns, col)
		values = append(values, normalizeValue(row[col]))
		placeholders = append(placeholders, "?")
	}
	if len(columns) == 0 {
		return false, nil
	}

	exists, err := rowExists(tx, table.Name, table.Key, keyValue)
	if err != nil {
		return false, wrapDBErr(err)
	}
	switch strategy {
	case StrategySkip:
		if exists {
			return false, nil
		}
	case StrategyMerge:
		if exists {
			assignments := make([]string, 0, len(columns))
			updateValues := make([]any, 0, len(columns))
			for i, col := range columns {
				if col == table.Key {
					continue
				}
				assignments = append(assignments, col+" = ?")
				updateValues = append(updateValues, values[i])
			}
			if len(assignments) == 0 {
				return false, nil
			}
			updateValues = append(updateValues, keyValue)
			_, err := tx.Exec(
				`UPDATE `+table.Name+` SET `+strings.Join(assignments, ", ")+` WHERE `+table.Key+` = ?`,
				updateValues...)
			if err != nil {
				return false, wrapDBErr(err)
			}
			return true, nil
		}
	case StrategyOverwrite:
		if exists {
			if _, err := tx.Exec(`DELETE FROM `+table.Name+` WHERE `+table.Key+` = ?`, keyValue); err != nil {
				return false, wrapDBErr(err)
			}
		}
	}

	_, err = tx.Exec(
		`INSERT INTO `+table.Name+` (`+strings.Join(columns, ", ")+`) VALUES (`+strings.Join(placeholders, ", ")+`)`,
		values...)
	if err != nil {
		return false, wrapDBErr(err)
	}
	return true, nil
}

func rowExists(tx *sql.Tx, table, key string, value any) (bool, error) {
	var one int
	err := tx.QueryRow(`SELECT 1 FROM `+table+` WHERE `+key+` = ? LIMIT 1`, value).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// normalizeValue 将 JSON 反序列化得到的 float64 还原为整型，避免 INTEGER 列被写成 REAL。
func normalizeValue(v any) any {
	switch value := v.(type) {
	case float64:
		if value == float64(int64(value)) {
			return int64(value)
		}
		return value
	case bool:
		if value {
			return int64(1)
		}
		return int64(0)
	case string, int64, int, nil:
		return value
	default:
		return value
	}
}

// SavePreImportBackup 在导入前把现有数据快照写入 data/backups 目录。
func (s *Service) SavePreImportBackup() (string, error) {
	payload, err := s.buildPayload(false)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	manifest := Manifest{
		Format:               config.BackupFormat,
		Version:              config.BackupFormatVersion,
		AppVersion:           s.cfg.AppVersion,
		ExportedAt:           time.Now().UTC().Format(time.RFC3339),
		Checksum:             crypto.SHA256Hex(raw),
		MasterKeyFingerprint: s.cipher.Fingerprint(),
	}
	body, err := json.MarshalIndent(Envelope{
		Manifest: manifest,
		Payload:  base64.StdEncoding.EncodeToString(raw),
	}, "", "  ")
	if err != nil {
		return "", err
	}

	name := "pre-import-" + time.Now().UTC().Format("20060102-150405") + ".json"
	path := filepath.Join(s.cfg.BackupsDir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", wrapDBErr(err)
	}
	return path, nil
}

func wrapDBErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ENOSPC) || strings.Contains(strings.ToLower(err.Error()), "disk is full") ||
		strings.Contains(strings.ToLower(err.Error()), "no space left") {
		return apperr.ErrDiskFull
	}
	return apperr.ErrVersionMigration.WithMessage(fmt.Sprintf("%s", err.Error()))
}
