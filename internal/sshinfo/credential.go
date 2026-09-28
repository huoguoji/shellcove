package sshinfo

import (
	"database/sql"
	"time"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/crypto"
	"github.com/huoguoji/shellcove/internal/db"
)

func nowAddDays(days int) time.Time {
	return time.Now().UTC().AddDate(0, 0, days)
}

// maxJumpDepth 跳板机最大嵌套层数，防止异常数据导致无限解析。
const maxJumpDepth = 4

// Credentials 解密凭证供 SSH 连接使用（明文仅存在于内存）。
func (s *Store) Credentials(id string) (*Credentials, error) {
	return s.credentialsWithDepth(id, 0)
}

func (s *Store) credentialsWithDepth(id string, depth int) (*Credentials, error) {
	if depth > maxJumpDepth {
		return nil, apperr.ErrBadRequest.WithMessage("跳板机嵌套层数超出限制")
	}

	var (
		c                            = &Credentials{}
		passwordEnc, keyEnc, passEnc sql.NullString
		jumpID                       sql.NullString
	)
	err := s.conn.QueryRow(
		`SELECT id, name, host, port, username, auth_type, password_enc, private_key_enc, passphrase_enc,
		        COALESCE(proxy_type, ''), COALESCE(proxy_host, ''), COALESCE(proxy_port, 0), jump_ssh_id, monitor_enabled
		 FROM ssh_infos WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&c.ID, &c.Name, &c.Host, &c.Port, &c.Username, &c.AuthType,
			&passwordEnc, &keyEnc, &passEnc, &c.ProxyType, &c.ProxyHost, &c.ProxyPort, &jumpID, &c.MonitorEnabled)
	if err == sql.ErrNoRows {
		return nil, apperr.ErrNotFound.WithMessage("SSH 信息不存在或在回收站中")
	}
	if err != nil {
		return nil, err
	}

	if c.Password, err = s.cipher.DecryptString(db.NullStr(passwordEnc)); err != nil {
		return nil, apperr.ErrDecryptionFailed.WithMessage("凭据解密失败：主密钥可能已更换")
	}
	if c.PrivateKey, err = s.cipher.DecryptString(db.NullStr(keyEnc)); err != nil {
		return nil, apperr.ErrDecryptionFailed.WithMessage("私钥解密失败：主密钥可能已更换")
	}
	if c.Passphrase, err = s.cipher.DecryptString(db.NullStr(passEnc)); err != nil {
		return nil, apperr.ErrDecryptionFailed.WithMessage("私钥口令解密失败：主密钥可能已更换")
	}

	if next := db.NullStr(jumpID); next != "" {
		jump, err := s.credentialsWithDepth(next, depth+1)
		if err != nil {
			return nil, err
		}
		c.Jump = jump
	}
	return c, nil
}

// Reveal 返回明文凭证，仅供「二次验证后查看凭证」接口调用。
func (s *Store) Reveal(id string) (*Secret, error) {
	creds, err := s.Credentials(id)
	if err != nil {
		return nil, err
	}
	return &Secret{
		Password:    creds.Password,
		PrivateKey:  creds.PrivateKey,
		Passphrase:  creds.Passphrase,
		AuthType:    creds.AuthType,
		Username:    creds.Username,
		Host:        creds.Host,
		Port:        creds.Port,
		HasPassword: creds.Password != "",
		HasKey:      creds.PrivateKey != "",
	}, nil
}

// ReEncryptAll 使用原主密钥解密全部凭证并用当前主密钥重新加密。
// 用于导入备份后主密钥不一致的场景；任一记录解密失败即整体回滚。
func (s *Store) ReEncryptAll(oldKey []byte) error {
	oldCipher, err := crypto.New(oldKey)
	if err != nil {
		return apperr.ErrWrongMasterKey
	}
	if string(oldKey) == string(s.cipher.KeyBytes()) {
		return nil
	}

	type row struct {
		id          string
		pw, key, pp sql.NullString
	}
	rows, err := s.conn.Query(`SELECT id, password_enc, private_key_enc, passphrase_enc FROM ssh_infos`)
	if err != nil {
		return err
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.pw, &r.key, &r.pp); err != nil {
			rows.Close()
			return err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// 先全部解密校验，避免解到一半才发现密钥错误。
	type plainRow struct{ id, pw, key, pp string }
	plains := make([]plainRow, 0, len(list))
	for _, r := range list {
		pw, err := decryptWith(oldCipher, db.NullStr(r.pw))
		if err != nil {
			return apperr.ErrWrongMasterKey
		}
		key, err := decryptWith(oldCipher, db.NullStr(r.key))
		if err != nil {
			return apperr.ErrWrongMasterKey
		}
		pp, err := decryptWith(oldCipher, db.NullStr(r.pp))
		if err != nil {
			return apperr.ErrWrongMasterKey
		}
		plains = append(plains, plainRow{id: r.id, pw: pw, key: key, pp: pp})
	}

	tx, err := s.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, p := range plains {
		pw, err := s.cipher.EncryptString(p.pw)
		if err != nil {
			return apperr.ErrEncryptionFailed
		}
		key, err := s.cipher.EncryptString(p.key)
		if err != nil {
			return apperr.ErrEncryptionFailed
		}
		pp, err := s.cipher.EncryptString(p.pp)
		if err != nil {
			return apperr.ErrEncryptionFailed
		}
		if _, err := tx.Exec(
			`UPDATE ssh_infos SET password_enc = ?, private_key_enc = ?, passphrase_enc = ?, key_id = ?, updated_at = ?
			 WHERE id = ?`,
			nullStr(pw), nullStr(key), nullStr(pp), s.cipher.KeyID(), db.NowStr(), p.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func decryptWith(c *crypto.Cipher, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return c.DecryptString(value)
}
