package db

import (
	"database/sql"
	"strings"
	"time"
)

// TimeFormat 数据库中统一使用的时间格式（UTC）。
const TimeFormat = time.RFC3339

// NowStr 返回当前 UTC 时间的 RFC3339 字符串。
func NowStr() string { return time.Now().UTC().Format(TimeFormat) }

// TimeStr 将时间格式化为存储格式。
func TimeStr(t time.Time) string { return t.UTC().Format(TimeFormat) }

// ParseTime 宽容解析数据库中读取的时间字符串，失败返回零值。
func ParseTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// NullStr 返回 sql.NullString 的字符串值。
func NullStr(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

// NullStrPtr 将 sql.NullString 转为 *string，空值返回 nil。
func NullStrPtr(v sql.NullString) *string {
	if !v.Valid || v.String == "" {
		return nil
	}
	s := v.String
	return &s
}

// SettingGet 读取配置项，不存在时返回默认值。
func SettingGet(conn *sql.DB, key, def string) string {
	var v string
	err := conn.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return def
	}
	return v
}

// SettingSet 写入或更新配置项。
func SettingSet(conn *sql.DB, key, value string) error {
	_, err := conn.Exec(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, NowStr())
	return err
}

// SettingGetBool 读取布尔配置项。
func SettingGetBool(conn *sql.DB, key string, def bool) bool {
	switch strings.ToLower(SettingGet(conn, key, "")) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// SettingGetInt 读取整型配置项。
func SettingGetInt(conn *sql.DB, key string, def int) int {
	raw := SettingGet(conn, key, "")
	if raw == "" {
		return def
	}
	var n int
	if _, err := fmtSscan(raw, &n); err != nil {
		return def
	}
	return n
}

func fmtSscan(s string, out *int) (int, error) {
	n := 0
	started := false
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			if !started {
				continue
			}
			break
		}
		started = true
		n = n*10 + int(r-'0')
	}
	if !started {
		return 0, errNotNumber
	}
	*out = n
	return 1, nil
}

var errNotNumber = sql.ErrNoRows

// CleanupBlacklist 清理过期的 token 黑名单记录。
func CleanupBlacklist(conn *sql.DB) error {
	_, err := conn.Exec(`DELETE FROM token_blacklist WHERE expires_at < ?`, NowStr())
	return err
}

// Blacklist 将 JWT 的 jti 加入黑名单（登出时使用）。
func Blacklist(conn *sql.DB, jti string, expiresAt time.Time) error {
	_, err := conn.Exec(
		`INSERT INTO token_blacklist (jti, expires_at) VALUES (?, ?) ON CONFLICT(jti) DO NOTHING`,
		jti, TimeStr(expiresAt))
	return err
}

// IsBlacklisted 判断 jti 是否已被吊销。
func IsBlacklisted(conn *sql.DB, jti string) bool {
	var one int
	err := conn.QueryRow(`SELECT 1 FROM token_blacklist WHERE jti = ?`, jti).Scan(&one)
	return err == nil
}
