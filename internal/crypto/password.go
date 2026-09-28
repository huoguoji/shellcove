package crypto

import (
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// MinPanelPasswordLen 面板账号密码最小长度。
const MinPanelPasswordLen = 8

// MinBackupPasswordLen 备份密码最小长度。
const MinBackupPasswordLen = 12

// HashPassword 使用 bcrypt 生成密码哈希。
func HashPassword(password string) (string, error) {
	buf, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// CheckPassword 校验密码是否匹配哈希。
func CheckPassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ValidatePanelPassword 校验面板密码强度：>=8 位且同时包含字母与数字。
func ValidatePanelPassword(password string) bool {
	if len([]rune(password)) < MinPanelPasswordLen {
		return false
	}
	var hasLetter, hasDigit bool
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}

// ValidateBackupPassword 校验备份密码强度：>=12 位。
func ValidateBackupPassword(password string) bool {
	return len([]rune(strings.TrimSpace(password))) >= MinBackupPasswordLen
}
