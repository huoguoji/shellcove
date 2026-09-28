// Package crypto 提供 AES-256-GCM 字段级加密与 PBKDF2-SHA256 密钥派生。
//
// 存储格式：base64(iv + ciphertext + authTag)，每条记录独立随机 IV。
// 主密钥通过环境变量 ENCRYPTION_KEY 或 ENCRYPTION_KEY_FILE 注入，绝不入库。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	// KeySize AES-256 主密钥长度（字节）。
	KeySize = 32
	// BackupIterations 备份文件密钥派生迭代次数。
	BackupIterations = 600000
	// SaltSize 文件级加密盐长度。
	SaltSize = 16
)

// ErrInvalidKey 主密钥格式不合法。
var ErrInvalidKey = errors.New("ENCRYPTION_KEY 必须是 32 字节（64 位 hex / 32 字符原文 / base64）")

// Cipher 封装字段级加解密器，线程安全（cipher.AEAD 可并发使用）。
type Cipher struct {
	aead  cipher.AEAD
	key   []byte
	keyID int
}

// New 基于主密钥构造 Cipher。
func New(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	dup := make([]byte, len(key))
	copy(dup, key)
	return &Cipher{aead: aead, key: dup, keyID: 1}, nil
}

// KeyID 返回当前密钥版本号（为密钥轮换预留，初期固定为 1）。
func (c *Cipher) KeyID() int { return c.keyID }

// KeyBytes 返回主密钥副本（用于比对导入备份的原主密钥是否一致）。
func (c *Cipher) KeyBytes() []byte {
	dup := make([]byte, len(c.key))
	copy(dup, c.key)
	return dup
}

// Fingerprint 返回主密钥指纹（不泄露密钥本身，用于界面对比确认）。
func (c *Cipher) Fingerprint() string {
	return SHA256Hex(c.key)[:16]
}

// ParseKey 解析主密钥字符串，支持 64 位 hex、标准 base64、32 字节原文。
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrInvalidKey
	}
	if len(s) == KeySize*2 {
		if b, err := hex.DecodeString(s); err == nil && len(b) == KeySize {
			return b, nil
		}
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == KeySize {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil && len(b) == KeySize {
		return b, nil
	}
	if len(s) == KeySize {
		return []byte(s), nil
	}
	return nil, ErrInvalidKey
}

// LoadMasterKey 按优先级读取密钥文件，其次读环境变量值。
func LoadMasterKey(envValue, keyFile string) ([]byte, error) {
	if keyFile != "" {
		raw, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("读取密钥文件失败: %w", err)
		}
		return ParseKey(string(raw))
	}
	return ParseKey(envValue)
}

// GenerateKey 生成随机主密钥，返回 hex 字符串。
func GenerateKey() (string, error) {
	buf := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// EncryptString 加密明文字符串，返回 base64(iv+ct+tag)。
func (c *Cipher) EncryptString(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	out, err := c.EncryptBytes([]byte(plain))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

// DecryptString 解密 base64(iv+ct+tag)。
func (c *Cipher) DecryptString(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("密文 base64 解码失败: %w", err)
	}
	out, err := c.DecryptBytes(raw)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// EncryptBytes 加密二进制内容，返回 iv+ct+tag。
func (c *Cipher) EncryptBytes(plain []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	// Seal 会把 authTag 追加在密文尾部，正好符合 iv+ct+tag 布局。
	return c.aead.Seal(nonce, nonce, plain, nil), nil
}

// DecryptBytes 解密 iv+ct+tag。
func (c *Cipher) DecryptBytes(data []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(data) <= ns {
		return nil, errors.New("密文长度不足")
	}
	plain, err := c.aead.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return nil, errors.New("解密失败：密钥不匹配或数据已损坏")
	}
	return plain, nil
}

// MaskSecret 生成不回显的占位提示。
func MaskSecret(plain string) string {
	if plain == "" {
		return ""
	}
	return strings.Repeat("*", 8)
}

// DeriveKey PBKDF2-SHA256 派生密钥，用于备份文件级加密。
func DeriveKey(password string, salt []byte, iterations, keyLen int) []byte {
	if iterations <= 0 {
		iterations = BackupIterations
	}
	if keyLen <= 0 {
		keyLen = KeySize
	}
	return pbkdf2.Key([]byte(password), salt, iterations, keyLen, sha256.New)
}

// RandomBytes 生成 n 字节随机数据。
func RandomBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// RandomToken 生成 base64url 随机令牌，用于 export_token / import_token。
func RandomToken(n int) (string, error) {
	buf, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// RandomHex 生成 n 字节随机数据的 hex 表示，用于生成初始管理员密码、恢复码。
func RandomHex(n int) (string, error) {
	buf, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// SHA256Hex 计算内容摘要。
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
