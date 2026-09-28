// Package config 负责从环境变量加载运行配置。
package config

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/crypto"
)

// AppVersion 当前程序版本，写入备份 manifest 用于版本兼容性判断。
const AppVersion = "0.3.2"

// BackupFormat 备份文件格式标识。
const BackupFormat = "shellcove-backup"

// BackupFormatVersion 当前备份格式版本。
const BackupFormatVersion = "1.0"

// Config 运行期配置。
type Config struct {
	AppVersion string

	Port    int
	DataDir string
	TZ      string

	DBPath        string
	RecordingsDir string
	UploadsDir    string
	BackupsDir    string

	MasterKey []byte
	JWTSecret []byte

	IPWhitelist  []string
	SecureCookie bool

	// 回收站保留天数，0 表示不自动清理。
	TrashRetentionDays int
	// 清理任务执行周期。
	TrashCleanupInterval time.Duration
	// 终端会话空闲保活与后端连接保留时长。
	SessionIdleTimeout time.Duration
	// 二次验证令牌与导出/导入令牌有效期。
	VerifyTokenTTL time.Duration
	ExportTokenTTL time.Duration
	ImportTokenTTL time.Duration
	// 单次会话录像文件大小上限（MB），0 表示不限制。
	RecordingMaxMB int
	// 查看凭证短时间多次调用的告警阈值与窗口。
	RevealWarnThreshold int
	RevealWarnWindow    time.Duration
	// SFTP 单文件与分片上限。
	UploadChunkSize  int64
	UploadMaxFileMB  int64
	// 是否允许用户自行开启 2FA（管理员可在设置页强制全员 2FA）。
	ForceTOTPForAll bool
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		return "./data"
	}
	return "/app/data"
}

// Load 读取环境变量并校验必填项。
func Load() (*Config, error) {
	dataDir := env("DATA_DIR", defaultDataDir())
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}

	masterKey, err := crypto.LoadMasterKey(os.Getenv("ENCRYPTION_KEY"), strings.TrimSpace(os.Getenv("ENCRYPTION_KEY_FILE")))
	if err != nil {
		return nil, apperr.New(apperr.CodeInternal,
			"主密钥读取失败：请通过 ENCRYPTION_KEY（32 字节 hex）或 ENCRYPTION_KEY_FILE 注入密钥", 500)
	}

	cfg := &Config{
		AppVersion: AppVersion,
		Port:       envInt("PORT", 8080),
		DataDir:    absData,
		TZ:         env("TZ", "UTC"),

		DBPath:        filepath.Join(absData, "shellcove.db"),
		RecordingsDir: filepath.Join(absData, "recordings"),
		UploadsDir:    filepath.Join(absData, "uploads"),
		BackupsDir:    filepath.Join(absData, "backups"),

		MasterKey: masterKey,
		JWTSecret: deriveJWTSecret(masterKey),

		IPWhitelist:  splitList(os.Getenv("IP_WHITELIST")),
		SecureCookie: envBool("SECURE_COOKIE", true),

		TrashRetentionDays: envInt("TRASH_RETENTION_DAYS", 30),
		TrashCleanupInterval: time.Duration(envInt("TRASH_CLEANUP_MINUTES", 60)) * time.Minute,

		SessionIdleTimeout: time.Duration(envInt("SESSION_IDLE_MINUTES", 30)) * time.Minute,
		VerifyTokenTTL:     5 * time.Minute,
		ExportTokenTTL:     5 * time.Minute,
		ImportTokenTTL:     10 * time.Minute,
		RecordingMaxMB:     envInt("RECORDING_MAX_MB", 64),

		RevealWarnThreshold: envInt("REVEAL_WARN_THRESHOLD", 5),
		RevealWarnWindow:    time.Duration(envInt("REVEAL_WARN_WINDOW_MINUTES", 10)) * time.Minute,

		UploadChunkSize: int64(envInt("UPLOAD_CHUNK_KB", 2048)) * 1024,
		UploadMaxFileMB: int64(envInt("UPLOAD_MAX_FILE_MB", 4096)),
	}

	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, apperr.New(apperr.CodeInternal, "PORT 配置不合法", 500)
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}
	if cfg.TZ != "" {
		if loc, err := time.LoadLocation(cfg.TZ); err == nil {
			time.Local = loc
		}
	}
	return cfg, nil
}

// EnsureDirs 创建运行时数据目录。
func (c *Config) EnsureDirs() error {
	for _, dir := range []string{c.DataDir, c.RecordingsDir, c.UploadsDir, c.BackupsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return apperr.New(apperr.CodeInternal, "创建数据目录失败: "+dir, 500)
		}
	}
	return nil
}

// deriveJWTSecret 由主密钥派生 JWT 签名密钥，避免额外的必填环境变量。
func deriveJWTSecret(master []byte) []byte {
	h := sha256.New()
	h.Write(master)
	h.Write([]byte("|shellcove-jwt-v1"))
	return h.Sum(nil)
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
