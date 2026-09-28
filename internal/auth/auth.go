// Package auth 负责面板登录、JWT 会话、TOTP 两步验证与恢复码。
package auth

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"errors"
	"image/png"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/config"
	"github.com/huoguoji/shellcove/internal/crypto"
	"github.com/huoguoji/shellcove/internal/db"
	"github.com/huoguoji/shellcove/internal/user"
)

// TokenTTL 面板会话有效期。
const TokenTTL = 12 * time.Hour

// Issuer JWT 签发方标识。
const Issuer = "shellcove"

// RecoveryCodeCount 一次性恢复码数量。
const RecoveryCodeCount = 10

// Claims 面板会话声明。
type Claims struct {
	UserID             string `json:"uid"`
	Username           string `json:"usr"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"mcp"`
	jwt.RegisteredClaims
}

// Service 认证服务。
type Service struct {
	conn   *sql.DB
	cipher *crypto.Cipher
	cfg    *config.Config
	users  *user.Store

	// Tokens 保存二次验证后签发的一次性令牌。
	Tokens *TokenStore
	// RevealCounter 统计查看凭证的调用频率。
	RevealCounter *RateCounter
}

// New 构造认证服务。
func New(conn *sql.DB, cipher *crypto.Cipher, cfg *config.Config, users *user.Store) *Service {
	return &Service{
		conn:          conn,
		cipher:        cipher,
		cfg:           cfg,
		users:         users,
		Tokens:        NewTokenStore(),
		RevealCounter: NewRateCounter(cfg.RevealWarnWindow, cfg.RevealWarnThreshold),
	}
}

// LoginInput 登录参数。
type LoginInput struct {
	Username     string
	Password     string
	TOTPCode     string
	RecoveryCode string
	IP           string
	UserAgent    string
}

// LoginResult 登录结果。
type LoginResult struct {
	User *user.User

	// NeedTOTP 为 true 表示密码正确但需要补充动态验证码。
	NeedTOTP bool
	// MustSetupTOTP 为 true 表示管理员已强制 2FA，用户需立即绑定。
	MustSetupTOTP bool

	Token     string
	ExpiresAt time.Time
}

// IssueToken 签发 JWT。
func (s *Service) IssueToken(u *user.User) (string, time.Time, error) {
	now := time.Now()
	expires := now.Add(TokenTTL)
	claims := &Claims{
		UserID:             u.ID,
		Username:           u.Username,
		Role:               u.Role,
		MustChangePassword: u.MustChangePassword,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   u.ID,
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.cfg.JWTSecret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expires, nil
}

// ParseToken 校验并解析 JWT。
func (s *Service) ParseToken(raw string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("非预期的签名算法")
		}
		return s.cfg.JWTSecret, nil
	}, jwt.WithIssuer(Issuer), jwt.WithExpirationRequired())
	if err != nil {
		return nil, apperr.ErrUnauthorized
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, apperr.ErrUnauthorized
	}
	if db.IsBlacklisted(s.conn, claims.ID) {
		return nil, apperr.ErrUnauthorized
	}
	return claims, nil
}

// Revoke 将令牌加入黑名单。
func (s *Service) Revoke(claims *Claims) error {
	if claims == nil {
		return nil
	}
	exp := time.Now().Add(TokenTTL)
	if claims.ExpiresAt != nil {
		exp = claims.ExpiresAt.Time
	}
	_ = db.CleanupBlacklist(s.conn)
	return db.Blacklist(s.conn, claims.ID, exp)
}

// ForcedTOTP 返回是否已强制全员启用 2FA。
func (s *Service) ForcedTOTP() bool {
	if s.cfg.ForceTOTPForAll {
		return true
	}
	return db.SettingGetBool(s.conn, "force_totp_all", false)
}

// Authenticate 执行登录流程：密码 → 2FA → 签发会话。
func (s *Service) Authenticate(in LoginInput) (*LoginResult, error) {
	u, err := s.users.GetByUsername(strings.TrimSpace(in.Username))
	if err != nil {
		// 用户名不存在与密码错误返回同一提示，避免账号枚举。
		return nil, apperr.ErrAuthFailed.WithMessage("用户名或密码错误")
	}
	if u.Disabled {
		return nil, apperr.ErrAccountDisabled
	}
	hash, err := s.users.PasswordHash(u.ID)
	if err != nil {
		return nil, err
	}
	if !crypto.CheckPassword(hash, in.Password) {
		return nil, apperr.ErrAuthFailed.WithMessage("用户名或密码错误")
	}

	if u.TOTPEnabled {
		switch {
		case strings.TrimSpace(in.RecoveryCode) != "":
			if err := s.consumeRecoveryCode(u.ID, in.RecoveryCode); err != nil {
				return nil, err
			}
		case strings.TrimSpace(in.TOTPCode) == "":
			return nil, apperr.ErrTOTPRequired
		default:
			if err := s.checkTOTP(u.ID, in.TOTPCode); err != nil {
				return nil, err
			}
		}
	}

	result := &LoginResult{User: u}
	if !u.TOTPEnabled && s.ForcedTOTP() {
		result.MustSetupTOTP = true
	}

	token, expires, err := s.IssueToken(u)
	if err != nil {
		return nil, err
	}
	result.Token = token
	result.ExpiresAt = expires

	if err := s.users.TouchLogin(u.ID); err != nil {
		return nil, err
	}
	return result, nil
}

// ChangePassword 修改密码并重新签发会话（清除强制改密标记）。
func (s *Service) ChangePassword(u *user.User, oldPassword, newPassword string) (*user.User, string, time.Time, error) {
	hash, err := s.users.PasswordHash(u.ID)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	if !crypto.CheckPassword(hash, oldPassword) {
		return nil, "", time.Time{}, apperr.ErrAuthFailed.WithMessage("原密码错误")
	}
	if oldPassword == newPassword {
		return nil, "", time.Time{}, apperr.ErrWeakPassword.WithMessage("新密码不能与原密码相同")
	}
	if err := s.users.SetPassword(u.ID, newPassword, false); err != nil {
		return nil, "", time.Time{}, err
	}
	updated, err := s.users.GetByID(u.ID)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	token, expires, err := s.IssueToken(updated)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	return updated, token, expires, nil
}

// TOTPSetup 2FA 绑定信息。
type TOTPSetup struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
	QRCodePNG  string `json:"qr_code_png"` // data:image/png;base64,...
}

// SetupTOTP 生成 TOTP 密钥与二维码，此时尚未启用。
func (s *Service) SetupTOTP(u *user.User) (*TOTPSetup, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "ShellCove",
		AccountName: u.Username,
		Period:      30,
		SecretSize:  20,
	})
	if err != nil {
		return nil, err
	}
	enc, err := s.cipher.EncryptString(key.Secret())
	if err != nil {
		return nil, err
	}
	if err := s.users.SetTOTP(u.ID, enc, false); err != nil {
		return nil, err
	}

	img, err := key.Image(256, 256)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}

	return &TOTPSetup{
		Secret:     key.Secret(),
		OTPAuthURL: key.URL(),
		QRCodePNG:  "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
	}, nil
}

// EnableTOTP 校验动态验证码并启用 2FA，返回一次性恢复码（仅此一次可见）。
func (s *Service) EnableTOTP(u *user.User, code string) ([]string, error) {
	enc, _, err := s.users.TOTPSecret(u.ID)
	if err != nil {
		return nil, err
	}
	if enc == "" {
		return nil, apperr.ErrBadRequest.WithMessage("请先调用 2FA 绑定接口生成密钥")
	}
	if err := s.checkTOTP(u.ID, code); err != nil {
		return nil, err
	}
	if err := s.users.SetTOTP(u.ID, enc, true); err != nil {
		return nil, err
	}
	return s.regenerateRecoveryCodes(u.ID)
}

// DisableTOTP 关闭 2FA（需密码 + 动态验证码）。
func (s *Service) DisableTOTP(u *user.User, password, code string) error {
	if err := s.VerifySecondFactor(u, password, code); err != nil {
		return err
	}
	if err := s.users.ClearTOTP(u.ID); err != nil {
		return err
	}
	_, err := s.conn.Exec(`DELETE FROM recovery_codes WHERE user_id = ?`, u.ID)
	return err
}

// VerifySecondFactor 二次验证：登录密码 + （已启用时）动态验证码 / 恢复码。
func (s *Service) VerifySecondFactor(u *user.User, password, code string) error {
	hash, err := s.users.PasswordHash(u.ID)
	if err != nil {
		return err
	}
	if !crypto.CheckPassword(hash, password) {
		return apperr.ErrAuthFailed.WithMessage("密码错误")
	}
	if !u.TOTPEnabled {
		return nil
	}
	if strings.TrimSpace(code) == "" {
		return apperr.ErrTOTPRequired
	}
	if err := s.checkTOTP(u.ID, code); err == nil {
		return nil
	}
	// 允许使用一次性恢复码完成二次验证。
	return s.consumeRecoveryCode(u.ID, code)
}

// checkTOTP 校验动态验证码。
func (s *Service) checkTOTP(userID, code string) error {
	enc, _, err := s.users.TOTPSecret(userID)
	if err != nil {
		return err
	}
	if enc == "" {
		return apperr.ErrTOTPInvalid.WithMessage("尚未绑定两步验证")
	}
	secret, err := s.cipher.DecryptString(enc)
	if err != nil {
		return err
	}
	code = normalizeCode(code)
	if code == "" {
		return apperr.ErrTOTPRequired
	}
	valid, err := totp.ValidateCustom(code, secret, time.Now().UTC(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil || !valid {
		return apperr.ErrTOTPInvalid
	}
	return nil
}

// RecoveryCodeStatus 恢复码使用情况。
type RecoveryCodeStatus struct {
	Total     int `json:"total"`
	Remaining int `json:"remaining"`
}

// RecoveryCodes 统计恢复码。
func (s *Service) RecoveryCodes(userID string) (*RecoveryCodeStatus, error) {
	var total, remaining int
	if err := s.conn.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ?`, userID).Scan(&total); err != nil {
		return nil, err
	}
	if err := s.conn.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&remaining); err != nil {
		return nil, err
	}
	return &RecoveryCodeStatus{Total: total, Remaining: remaining}, nil
}

// RegenerateRecoveryCodes 重新生成恢复码（需密码二次确认）。
func (s *Service) RegenerateRecoveryCodes(u *user.User, password string) ([]string, error) {
	hash, err := s.users.PasswordHash(u.ID)
	if err != nil {
		return nil, err
	}
	if !crypto.CheckPassword(hash, password) {
		return nil, apperr.ErrAuthFailed.WithMessage("密码错误")
	}
	if !u.TOTPEnabled {
		return nil, apperr.ErrBadRequest.WithMessage("尚未启用两步验证")
	}
	return s.regenerateRecoveryCodes(u.ID)
}

// regenerateRecoveryCodes 生成新的恢复码集合，旧码全部作废。
func (s *Service) regenerateRecoveryCodes(userID string) ([]string, error) {
	tx, err := s.conn.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}

	codes := make([]string, 0, RecoveryCodeCount)
	now := db.NowStr()
	for i := 0; i < RecoveryCodeCount; i++ {
		raw, err := crypto.RandomHex(4)
		if err != nil {
			return nil, err
		}
		raw = strings.ToUpper(raw)
		display := raw[:4] + "-" + raw[4:]
		codes = append(codes, display)
		if _, err := tx.Exec(
			`INSERT INTO recovery_codes (id, user_id, code_hash, created_at) VALUES (?, ?, ?, ?)`,
			uuid.NewString(), userID, crypto.SHA256Hex([]byte(normalizeRecoveryCode(display))), now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}

// consumeRecoveryCode 校验并消费一个恢复码。
func (s *Service) consumeRecoveryCode(userID, code string) error {
	normalized := normalizeRecoveryCode(code)
	if normalized == "" {
		return apperr.ErrTOTPInvalid.WithMessage("恢复码错误")
	}
	target := crypto.SHA256Hex([]byte(normalized))

	rows, err := s.conn.Query(
		`SELECT id, code_hash FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID)
	if err != nil {
		return err
	}
	type candidate struct{ id, hash string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.hash); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range candidates {
		if constantTimeEqual(c.hash, target) {
			res, err := s.conn.Exec(`UPDATE recovery_codes SET used_at = ? WHERE id = ? AND used_at IS NULL`, db.NowStr(), c.id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				break
			}
			return nil
		}
	}
	return apperr.ErrTOTPInvalid.WithMessage("恢复码错误或已使用")
}

// normalizeRecoveryCode 去除分隔符并转为大写。
func normalizeRecoveryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}

// RequirePasswordChange 判断是否必须强制修改密码。
func RequirePasswordChange(u *user.User) error {
	if u.MustChangePassword {
		return apperr.ErrPasswordMustChg
	}
	return nil
}

// FormatSecretHint 生成敏感字段的提示文本（前端展示用，不含明文）。
func FormatSecretHint(hasValue bool) string {
	if hasValue {
		return "已配置（留空表示不修改）"
	}
	return "未配置"
}
