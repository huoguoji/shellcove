// Package api 提供 HTTP 路由、中间件与全部 REST / WebSocket 接口。
package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/auth"
	"github.com/huoguoji/shellcove/internal/backup"
	"github.com/huoguoji/shellcove/internal/command"
	"github.com/huoguoji/shellcove/internal/config"
	"github.com/huoguoji/shellcove/internal/crypto"
	"github.com/huoguoji/shellcove/internal/db"
	"github.com/huoguoji/shellcove/internal/folder"
	"github.com/huoguoji/shellcove/internal/permission"
	"github.com/huoguoji/shellcove/internal/sftp"
	"github.com/huoguoji/shellcove/internal/ssh"
	"github.com/huoguoji/shellcove/internal/sshinfo"
	"github.com/huoguoji/shellcove/internal/trash"
	"github.com/huoguoji/shellcove/internal/user"
)

// TokenCookie 保存 JWT 的 Cookie 名称。
const TokenCookie = "shellcove_token"

// maxBodyBytes 单次请求体上限（备份导入需要较大空间）。
const maxBodyBytes = 256 << 20

// Deps API 层依赖集合，由 main 装配。
type Deps struct {
	Cfg      *config.Config
	Conn     *sql.DB
	Cipher   *crypto.Cipher
	Auth     *auth.Service
	Users    *user.Store
	Perms    *permission.Service
	Folders  *folder.Store
	Infos    *sshinfo.Store
	Trash    *trash.Service
	Commands *command.Store
	Manager  *ssh.Manager
	SSHPool  *ssh.Pool
	SFTP     *sftp.Service
	Audit    *audit.Logger
	Backup   *backup.Service
	Static   fs.FS
}

// Server HTTP 服务。
type Server struct {
	Deps
	upgrader websocket.Upgrader
	pending  *pendingImports
}

// New 构造 Server 并注册路由。
func New(deps Deps) *Server {
	s := &Server{Deps: deps, pending: newPendingImports(30 * time.Minute)}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
		CheckOrigin:     func(r *http.Request) bool { return sameOrigin(r) },
	}
	return s
}

// Handler 返回带中间件的根处理器。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerAuthRoutes(mux)
	s.registerResourceRoutes(mux)
	s.registerUserRoutes(mux)
	s.registerCommandRoutes(mux)
	s.registerFileRoutes(mux)
	s.registerTerminalRoutes(mux)
	s.registerAuditRoutes(mux)
	s.registerBackupRoutes(mux)
	s.registerSettingsRoutes(mux)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"version": s.Cfg.AppVersion,
			"time":    db.NowStr(),
		})
	})
	s.registerStatic(mux)

	return s.recoverer(s.ipWhitelist(mux))
}

// ---------------------------------------------------------------- 中间件

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeError(w, apperr.ErrInternal.WithMessage("服务内部错误"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ipWhitelist(next http.Handler) http.Handler {
	allow := s.Cfg.IPWhitelist
	if len(allow) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !ipAllowed(ip, allow) {
			writeError(w, apperr.ErrForbidden.WithMessage("当前 IP 不在白名单内"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ipAllowed(ip string, allow []string) bool {
	for _, item := range allow {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == ip {
			return true
		}
		if strings.HasSuffix(item, "*") && strings.HasPrefix(ip, strings.TrimSuffix(item, "*")) {
			return true
		}
		if _, cidr, err := net.ParseCIDR(item); err == nil {
			if parsed := net.ParseIP(ip); parsed != nil && cidr.Contains(parsed) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------- 认证会话

type ctxKey string

const claimsKey ctxKey = "claims"

func withClaimsCtx(ctx context.Context, claims *auth.Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

func claimsFromCtx(ctx context.Context) *auth.Claims {
	if v, ok := ctx.Value(claimsKey).(*auth.Claims); ok {
		return v
	}
	return nil
}

// protected 包装需要登录的处理器，并拦截「必须修改密码」状态。
func (s *Server) protected(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, err := s.authenticate(r)
		if err != nil {
			writeError(w, err)
			return
		}
		if claims.MustChangePassword && !strings.HasPrefix(r.URL.Path, "/api/auth/") {
			writeError(w, apperr.ErrPasswordMustChg)
			return
		}
		fn(w, withClaims(r, claims))
	}
}

// admin 包装仅管理员可访问的处理器。
func (s *Server) admin(fn http.HandlerFunc) http.HandlerFunc {
	return s.protected(func(w http.ResponseWriter, r *http.Request) {
		if claims := claimsFrom(r); claims == nil || claims.Role != user.RoleAdmin {
			writeError(w, apperr.ErrNoPermission.WithMessage("仅管理员可执行该操作"))
			return
		}
		fn(w, r)
	})
}

func (s *Server) authenticate(r *http.Request) (*auth.Claims, error) {
	raw := ""
	if c, err := r.Cookie(TokenCookie); err == nil {
		raw = c.Value
	}
	if raw == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			raw = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	if raw == "" {
		return nil, apperr.ErrUnauthorized
	}
	claims, err := s.Auth.ParseToken(raw)
	if err != nil {
		return nil, apperr.ErrUnauthorized
	}
	if claims.ID != "" && db.IsBlacklisted(s.Conn, claims.ID) {
		return nil, apperr.ErrUnauthorized.WithMessage("登录状态已失效，请重新登录")
	}
	return claims, nil
}

func withClaims(r *http.Request, claims *auth.Claims) *http.Request {
	return r.WithContext(withClaimsCtx(r.Context(), claims))
}

func claimsFrom(r *http.Request) *auth.Claims { return claimsFromCtx(r.Context()) }

// currentUser 读取当前登录用户的完整信息。
func (s *Server) currentUser(r *http.Request) (*user.User, error) {
	claims := claimsFrom(r)
	if claims == nil {
		return nil, apperr.ErrUnauthorized
	}
	u, err := s.Users.GetByID(claims.UserID)
	if err != nil || u == nil {
		return nil, apperr.ErrUnauthorized
	}
	if u.Disabled {
		return nil, apperr.ErrAccountDisabled
	}
	return u, nil
}

// ---------------------------------------------------------------- 通用工具

// writeJSON 输出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// writeError 将业务错误映射为统一 JSON 错误响应。
func writeError(w http.ResponseWriter, err error) {
	if err == nil {
		err = apperr.ErrInternal
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = apperr.ErrNotFound
	}
	status := apperr.StatusOf(err)
	if status == http.StatusOK {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]any{
		"code":    apperr.CodeOf(err),
		"message": apperr.MessageOf(err),
	})
}

// noStore 关闭响应缓存（凭证类接口必须调用）。
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

// decodeJSON 读取并解析请求体。
func decodeJSON(w http.ResponseWriter, r *http.Request, out any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		return apperr.ErrBadRequest.WithMessage("读取请求体失败")
	}
	if len(body) == 0 {
		return apperr.ErrBadRequest.WithMessage("请求体不能为空")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return apperr.ErrBadRequest.WithMessage("请求参数不合法")
	}
	return nil
}

// decodeJSONOptional 宽松解析请求体：请求体为空时保留结构体默认值。
func decodeJSONOptional(r *http.Request, out any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return apperr.ErrBadRequest.WithMessage("读取请求体失败")
	}
	if strings.TrimSpace(string(body)) == "" {
		return nil
	}
	data := strings.TrimSpace(string(body))
	if strings.HasPrefix(data, "{") || strings.HasPrefix(data, "[") {
		return json.Unmarshal([]byte(data), out)
	}
	return nil
}

func queryInt(r *http.Request, key string, def int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func queryBool(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func clientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); v != "" {
		if idx := strings.Index(v, ","); idx > 0 {
			return strings.TrimSpace(v[:idx])
		}
		return v
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	host := r.Host
	trimmed := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	return strings.EqualFold(trimmed, host)
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// log 记录审计日志（自动补齐 IP / User-Agent）。
func (s *Server) log(r *http.Request, action, targetType, targetID, targetName, detail string, success bool) {
	e := audit.Entry{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		TargetName: targetName,
		Detail:     detail,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Success:    success,
	}
	if claims := claimsFrom(r); claims != nil {
		e.UserID = claims.UserID
		e.Username = claims.Username
	}
	s.Audit.Log(e)
}

// logAs 记录以指定用户为主体的审计日志（登录失败等场景）。
func (s *Server) logAs(r *http.Request, u *user.User, action, detail string, success bool) {
	e := audit.Entry{
		Action:    action,
		Detail:    detail,
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
		Success:   success,
	}
	if u != nil {
		e.UserID = u.ID
		e.Username = u.Username
	}
	s.Audit.Log(e)
}

// setTokenCookie 写入登录凭证 Cookie。
func (s *Server) setTokenCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     TokenCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.Cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
	})
}

func (s *Server) clearTokenCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     TokenCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.Cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// ---------------------------------------------------------------- 静态资源

func (s *Server) registerStatic(mux *http.ServeMux) {
	if s.Static == nil {
		return
	}
	fileServer := http.FileServer(http.FS(s.Static))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, apperr.ErrNotFound)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(s.Static, path); err != nil {
			// 前端为单页应用，未知路径回退到 index.html。
			serveIndex(w, r, s.Static)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, static fs.FS) {
	page, err := fs.ReadFile(static, "index.html")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"message": "前端资源未构建，请在 web/ 目录执行 npm run build",
		})
		return
	}
	// 这里必须直接写出内容：若改由 http.FileServer 处理，它会因为路径以
	// "index.html" 结尾而回 301 到 "./"，使 /commands 这类前端路由刷新后跳回首页。
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page)
}
