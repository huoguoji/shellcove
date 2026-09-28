package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/auth"
	"github.com/huoguoji/shellcove/internal/user"
)

func (s *Server) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.protected(s.handleLogout))
	mux.HandleFunc("GET /api/auth/me", s.protected(s.handleMe))
	mux.HandleFunc("POST /api/auth/verify", s.protected(s.handleVerify))
	mux.HandleFunc("POST /api/auth/change-password", s.protected(s.handleChangePassword))
	mux.HandleFunc("POST /api/auth/2fa/setup", s.protected(s.handleTOTPSetup))
	mux.HandleFunc("POST /api/auth/2fa/enable", s.protected(s.handleTOTPEnable))
	mux.HandleFunc("POST /api/auth/2fa/verify", s.protected(s.handleTOTPEnable))
	mux.HandleFunc("POST /api/auth/2fa/disable", s.protected(s.handleTOTPDisable))
	mux.HandleFunc("POST /api/auth/2fa/recovery", s.handleRecoveryLogin)
	mux.HandleFunc("GET /api/auth/2fa/recovery", s.protected(s.handleRecoveryStatus))
	mux.HandleFunc("POST /api/auth/2fa/recovery/regenerate", s.protected(s.handleRecoveryRegenerate))
}

type loginRequest struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	TOTPCode     string `json:"totp_code"`
	RecoveryCode string `json:"recovery_code"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || (req.Password == "" && req.RecoveryCode == "") {
		writeError(w, apperr.ErrBadRequest.WithMessage("请输入用户名与密码"))
		return
	}

	res, err := s.Auth.Authenticate(auth.LoginInput{
		Username:     req.Username,
		Password:     req.Password,
		TOTPCode:     req.TOTPCode,
		RecoveryCode: req.RecoveryCode,
		IP:           clientIP(r),
		UserAgent:    r.UserAgent(),
	})
	if err != nil {
		// 密码正确但缺少动态码：引导前端进入 2FA 步骤，而非报错。
		if apperr.CodeOf(err) == apperr.CodeTOTPRequired {
			writeJSON(w, http.StatusOK, map[string]any{"need_totp": true, "must_setup_totp": false})
			return
		}
		s.logAs(r, nil, audit.ActionLoginFailed, "用户名 "+req.Username+" 登录失败", false)
		writeError(w, err)
		return
	}
	if res.NeedTOTP {
		// 密码正确但缺少或未完成 2FA，引导前端进入验证码步骤。
		writeJSON(w, http.StatusOK, map[string]any{
			"need_totp":       true,
			"must_setup_totp": res.MustSetupTOTP,
		})
		return
	}

	s.setTokenCookie(w, res.Token, res.ExpiresAt)
	s.logAs(r, res.User, audit.ActionLogin, "登录成功", true)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":                res.Token,
		"expires_at":           res.ExpiresAt.UTC().Format(time.RFC3339),
		"user":                 res.User,
		"must_change_password": res.User.MustChangePassword,
	})
}

func (s *Server) handleRecoveryLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username     string `json:"username"`
		RecoveryCode string `json:"recovery_code"`
		Password     string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.RecoveryCode) == "" || req.Password == "" {
		writeError(w, apperr.ErrBadRequest.WithMessage("请输入用户名、登录密码与恢复码"))
		return
	}
	res, err := s.Auth.Authenticate(auth.LoginInput{
		Username:     strings.TrimSpace(req.Username),
		Password:     req.Password,
		RecoveryCode: strings.TrimSpace(req.RecoveryCode),
		IP:           clientIP(r),
		UserAgent:    r.UserAgent(),
	})
	if err != nil {
		s.logAs(r, nil, audit.ActionLoginFailed, "使用恢复码登录失败", false)
		writeError(w, err)
		return
	}
	if res.NeedTOTP {
		writeJSON(w, http.StatusOK, map[string]any{"need_totp": true})
		return
	}
	s.setTokenCookie(w, res.Token, res.ExpiresAt)
	s.logAs(r, res.User, audit.ActionRecoveryUsed, "使用恢复码登录成功", true)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      res.Token,
		"expires_at": res.ExpiresAt.UTC().Format(time.RFC3339),
		"user":       res.User,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	if claims != nil {
		_ = s.Auth.Revoke(claims)
	}
	s.clearTokenCookie(w)
	s.log(r, audit.ActionLogout, "user", "", "", "退出登录", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	resolution, err := s.Perms.Resolve(u)
	if err != nil {
		writeError(w, err)
		return
	}
	codes, _ := s.Auth.RecoveryCodes(u.ID)

	writeJSON(w, http.StatusOK, map[string]any{
		"user":             u,
		"permissions":      resolution.Filter(),
		"all_resources":    resolution.All,
		"forced_totp":      s.Auth.ForcedTOTP(),
		"recovery_codes":   codes,
		"must_change_pwd":  u.MustChangePassword,
		"reveal_threshold": s.Cfg.RevealWarnThreshold,
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.Auth.VerifySecondFactor(u, req.OldPassword, ""); err != nil {
		s.log(r, audit.ActionLoginFailed, "user", u.ID, u.Username, "修改密码时旧密码校验失败", false)
		writeError(w, err)
		return
	}

	updated, token, expires, err := s.Auth.ChangePassword(u, req.OldPassword, req.NewPassword)
	if err != nil {
		writeError(w, err)
		return
	}
	s.setTokenCookie(w, token, expires)
	s.log(r, audit.ActionPasswordChange, "user", u.ID, u.Username, "修改登录密码", true)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":       updated,
		"token":      token,
		"expires_at": expires.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	setup, err := s.Auth.SetupTOTP(u)
	if err != nil {
		writeError(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, setup)
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	codes, err := s.Auth.EnableTOTP(u, strings.TrimSpace(req.Code))
	if err != nil {
		s.log(r, audit.ActionTOTPFailed, "user", u.ID, u.Username, "启用两步验证失败", false)
		writeError(w, err)
		return
	}
	noStore(w)
	s.log(r, audit.ActionTOTPEnable, "user", u.ID, u.Username, "启用两步验证", true)
	writeJSON(w, http.StatusOK, map[string]any{
		"recovery_codes": codes,
		"message":        "两步验证已启用，请立即保存恢复码",
	})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.Auth.DisableTOTP(u, req.Password, strings.TrimSpace(req.Code)); err != nil {
		s.log(r, audit.ActionTOTPFailed, "user", u.ID, u.Username, "关闭两步验证失败", false)
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionTOTPDisable, "user", u.ID, u.Username, "关闭两步验证", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRecoveryStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	status, err := s.Auth.RecoveryCodes(u.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleRecoveryRegenerate(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	codes, err := s.Auth.RegenerateRecoveryCodes(u, req.Password)
	if err != nil {
		writeError(w, err)
		return
	}
	noStore(w)
	s.log(r, audit.ActionTOTPEnable, "user", u.ID, u.Username, "重新生成恢复码", true)
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// handleVerify 校验登录密码 + 2FA，成功后签发一次性令牌。
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
		Purpose  string `json:"purpose"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.requireSecondFactor(r, u, req.Password, req.Code, ""); err != nil {
		writeError(w, err)
		return
	}

	purpose := strings.TrimSpace(req.Purpose)
	if purpose == "" {
		purpose = auth.PurposeReveal
	}
	ttl := s.Cfg.VerifyTokenTTL
	switch purpose {
	case auth.PurposeExport:
		ttl = s.Cfg.ExportTokenTTL
	case auth.PurposeImport:
		ttl = s.Cfg.ImportTokenTTL
	}
	token, expiresAt, err := s.Auth.Tokens.Issue(u.ID, purpose, ttl)
	if err != nil {
		writeError(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"purpose":    purpose,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

// requireSecondFactor 统一处理二次验证：优先使用一次性令牌，否则校验密码 + 动态码。
func (s *Server) requireSecondFactor(r *http.Request, u *user.User, password, code, token string) (bool, error) {
	if strings.TrimSpace(token) != "" {
		userID, err := s.Auth.Tokens.Consume(strings.TrimSpace(token), purposeOf(r))
		if err != nil {
			return false, apperr.ErrExportTokenInvalid
		}
		if userID != u.ID {
			return false, apperr.ErrExportTokenInvalid
		}
		return true, nil
	}
	if err := s.Auth.VerifySecondFactor(u, password, strings.TrimSpace(code)); err != nil {
		s.log(r, audit.ActionLoginFailed, "user", u.ID, u.Username, "二次验证失败", false)
		return false, apperr.ErrAuthFailed
	}
	return false, nil
}

// purposeOf 依据请求路径推断一次性令牌用途。
func purposeOf(r *http.Request) string {
	switch {
	case strings.Contains(r.URL.Path, "/backup/export"):
		return auth.PurposeExport
	case strings.Contains(r.URL.Path, "/backup/import"):
		return auth.PurposeImport
	default:
		return auth.PurposeReveal
	}
}
