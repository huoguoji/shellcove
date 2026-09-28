package api

import (
	"net/http"
	"strings"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/permission"
	"github.com/huoguoji/shellcove/internal/sshinfo"
	"github.com/huoguoji/shellcove/internal/user"
)

func (s *Server) registerUserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/users", s.admin(s.handleUserList))
	mux.HandleFunc("POST /api/users", s.admin(s.handleUserCreate))
	mux.HandleFunc("PUT /api/users/{id}", s.admin(s.handleUserUpdate))
	mux.HandleFunc("DELETE /api/users/{id}", s.admin(s.handleUserDelete))
	mux.HandleFunc("POST /api/users/{id}/password", s.admin(s.handleUserResetPassword))
	mux.HandleFunc("POST /api/users/{id}/role", s.admin(s.handleUserRole))
	mux.HandleFunc("POST /api/users/{id}/status", s.admin(s.handleUserStatus))
	mux.HandleFunc("GET /api/users/{id}/permissions", s.admin(s.handlePermissionGet))
	mux.HandleFunc("PUT /api/users/{id}/permissions", s.admin(s.handlePermissionUpdate))

	// 活动会话
	mux.HandleFunc("GET /api/sessions", s.protected(s.handleSessionList))
	mux.HandleFunc("POST /api/sessions/{id}/close", s.protected(s.handleSessionClose))
}

func (s *Server) handleUserList(w http.ResponseWriter, r *http.Request) {
	items, err := s.Users.List()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username           string `json:"username"`
		Password           string `json:"password"`
		Role               string `json:"role"`
		Remark             string `json:"remark"`
		MaxSessions        int    `json:"max_sessions"`
		MustChangePassword bool   `json:"must_change_password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = user.RoleUser
	}
	if role != user.RoleAdmin && role != user.RoleUser {
		writeError(w, apperr.ErrBadRequest.WithMessage("角色不合法"))
		return
	}
	u, err := s.Users.Create(strings.TrimSpace(req.Username), req.Password, role, req.Remark,
		req.MaxSessions, req.MustChangePassword)
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionUserCreate, "user", u.ID, u.Username, "新建用户，角色 "+role, true)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Remark      string `json:"remark"`
		MaxSessions int    `json:"max_sessions"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.Users.UpdateProfile(id, req.Remark, req.MaxSessions); err != nil {
		writeError(w, err)
		return
	}
	target, err := s.Users.GetByID(id)
	if err != nil {
		writeError(w, err)
		return
	}
	if req.MaxSessions > 0 && s.Manager.CountByUser(id) > req.MaxSessions {
		s.Manager.CloseByUser(id, "管理员下调了并发会话上限")
	}
	s.log(r, audit.ActionUserUpdate, "user", id, target.Username, "更新用户资料", true)
	writeJSON(w, http.StatusOK, target)
}

func (s *Server) handleUserResetPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Password           string `json:"password"`
		MustChangePassword bool   `json:"must_change_password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	target, err := s.Users.GetByID(id)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.Users.SetPassword(id, req.Password, req.MustChangePassword); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionUserUpdate, "user", id, target.Username, "重置登录密码", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUserRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	role := strings.TrimSpace(req.Role)
	if role != user.RoleAdmin && role != user.RoleUser {
		writeError(w, apperr.ErrBadRequest.WithMessage("角色不合法"))
		return
	}
	target, err := s.Users.GetByID(id)
	if err != nil {
		writeError(w, err)
		return
	}
	if target.IsAdmin() && role != user.RoleAdmin {
		if err := s.ensureAnotherAdmin(id); err != nil {
			writeError(w, err)
			return
		}
	}
	if err := s.Users.SetRole(id, role); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionUserUpdate, "user", id, target.Username, "调整角色为 "+role, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUserStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Disabled bool `json:"disabled"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	claims := claimsFrom(r)
	if req.Disabled && claims != nil && claims.UserID == id {
		writeError(w, apperr.ErrBadRequest.WithMessage("不能禁用当前登录账号"))
		return
	}
	target, err := s.Users.GetByID(id)
	if err != nil {
		writeError(w, err)
		return
	}
	if req.Disabled && target.IsAdmin() {
		if err := s.ensureAnotherAdmin(id); err != nil {
			writeError(w, err)
			return
		}
	}
	if err := s.Users.SetDisabled(id, req.Disabled); err != nil {
		writeError(w, err)
		return
	}
	action := audit.ActionUserEnable
	detail := "启用账号"
	if req.Disabled {
		action = audit.ActionUserDisable
		detail = "禁用账号"
		// 禁用后立即断开该用户的全部会话。
		s.Manager.CloseByUser(id, "账号已被禁用")
	}
	s.log(r, action, "user", id, target.Username, detail, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if claims := claimsFrom(r); claims != nil && claims.UserID == id {
		writeError(w, apperr.ErrBadRequest.WithMessage("不能删除当前登录账号"))
		return
	}
	target, err := s.Users.GetByID(id)
	if err != nil {
		writeError(w, err)
		return
	}
	if target.IsAdmin() {
		if err := s.ensureAnotherAdmin(id); err != nil {
			writeError(w, err)
			return
		}
	}
	if err := s.Users.Delete(id); err != nil {
		writeError(w, err)
		return
	}
	s.Manager.CloseByUser(id, "账号已被删除")
	s.log(r, audit.ActionUserDelete, "user", id, target.Username, "删除用户", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) ensureAnotherAdmin(excludeID string) error {
	admins, err := s.Users.CountAdmins()
	if err != nil {
		return err
	}
	if admins <= 1 {
		return apperr.ErrBadRequest.WithMessage("系统至少需要保留一名管理员")
	}
	return nil
}

// ------------------------------------------------------------------ 授权

func (s *Server) handlePermissionGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.Users.GetByID(id); err != nil {
		writeError(w, err)
		return
	}
	grants, err := s.Perms.List(id)
	if err != nil {
		writeError(w, err)
		return
	}
	tree, err := s.Folders.Tree()
	if err != nil {
		writeError(w, err)
		return
	}
	infos, err := s.Infos.List(sshinfo.ListFilter{})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"grants":  grants,
		"folders": tree,
		"ssh":     infos,
	})
}

func (s *Server) handlePermissionUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.Users.GetByID(id); err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Grants []*permission.Grant `json:"grants"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	grants := permission.Sanitize(req.Grants)
	if err := s.Perms.Replace(id, grants); err != nil {
		writeError(w, err)
		return
	}
	// 权限收紧后关闭该用户已无权访问的会话。
	s.closeUnauthorizedSessions(id)
	target, _ := s.Users.GetByID(id)
	name := id
	if target != nil {
		name = target.Username
	}
	s.log(r, audit.ActionPermissionUpdate, "user", id, name, "更新资源授权", true)
	writeJSON(w, http.StatusOK, map[string]any{"grants": grants})
}

func (s *Server) closeUnauthorizedSessions(userID string) {
	u, err := s.Users.GetByID(userID)
	if err != nil || u == nil {
		return
	}
	resolution, err := s.Perms.Resolve(u)
	if err != nil {
		return
	}
	for _, info := range s.Manager.List(userID) {
		if !resolution.Allows(info.SSHID) {
			if session, ok := s.Manager.Get(info.ID); ok {
				session.Close("资源权限已被回收")
			}
		}
	}
}

// ------------------------------------------------------------------ 活动会话

func (s *Server) handleSessionList(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	userID := ""
	if !u.IsAdmin() {
		userID = u.ID
	}
	items := s.Manager.List(userID)
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleSessionClose(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("id")
	session, ok := s.Manager.Get(id)
	if !ok {
		writeError(w, apperr.ErrNotFound.WithMessage("会话不存在或已结束"))
		return
	}
	if !u.IsAdmin() && session.UserID != u.ID {
		writeError(w, apperr.ErrNoPermission)
		return
	}
	session.Close("用户主动断开")
	if u.IsAdmin() && session.UserID != u.ID {
		s.log(r, audit.ActionSessionKill, "session", id, session.SSHName, "管理员强制断开会话", true)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
