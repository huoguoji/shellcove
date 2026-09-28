package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
)

func (s *Server) registerAuditRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/audit/logs", s.admin(s.handleAuditLogs))
	mux.HandleFunc("DELETE /api/audit/logs", s.admin(s.handleAuditClear))
	mux.HandleFunc("GET /api/audit/actions", s.admin(s.handleAuditActions))
	mux.HandleFunc("GET /api/audit/sessions", s.admin(s.handleAuditSessions))
	mux.HandleFunc("GET /api/audit/sessions/{id}", s.admin(s.handleAuditSessionDetail))
	mux.HandleFunc("GET /api/audit/sessions/{id}/commands", s.admin(s.handleAuditSessionCommands))
	mux.HandleFunc("GET /api/audit/sessions/{id}/recording", s.admin(s.handleAuditRecording))
	mux.HandleFunc("DELETE /api/audit/sessions/{id}/recording", s.admin(s.handleAuditRecordingDelete))
}

// 审计日志可选的动作集合，供前端筛选下拉使用。
var auditActionOptions = []string{
	audit.ActionLogin, audit.ActionLoginFailed, audit.ActionLogout,
	audit.ActionPasswordChange, audit.ActionTOTPEnable, audit.ActionTOTPDisable,
	audit.ActionTOTPFailed, audit.ActionRecoveryUsed,
	audit.ActionSSHCreate, audit.ActionSSHUpdate, audit.ActionSSHDelete,
	audit.ActionSSHRestore, audit.ActionSSHPurge, audit.ActionSSHReveal,
	audit.ActionSSHRevealAlert, audit.ActionSSHConnect, audit.ActionSSHDisconnect,
	audit.ActionFolderCreate, audit.ActionFolderUpdate, audit.ActionFolderDelete,
	audit.ActionUserCreate, audit.ActionUserUpdate, audit.ActionUserDelete,
	audit.ActionUserDisable, audit.ActionUserEnable, audit.ActionPermissionUpdate,
	audit.ActionCommandCreate, audit.ActionCommandUpdate, audit.ActionCommandDelete,
	audit.ActionCommandSend,
	audit.ActionSFTPUpload, audit.ActionSFTPDownload, audit.ActionSFTPDelete,
	audit.ActionSFTPMkdir, audit.ActionSFTPRename, audit.ActionSFTPChmod,
	audit.ActionBackupExportPrev, audit.ActionBackupExport, audit.ActionBackupInspect,
	audit.ActionBackupImport, audit.ActionSettingsUpdate, audit.ActionTrashEmpty,
	audit.ActionSessionKill, audit.ActionMonitorRead, audit.ActionAuditClear,
}

func (s *Server) handleAuditActions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": auditActionOptions})
}

func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	page := pageParams(r)
	items, total, err := s.Audit.List(audit.QueryFilter{
		UserID: strings.TrimSpace(r.URL.Query().Get("user_id")),
		Action: strings.TrimSpace(r.URL.Query().Get("action")),
		Search: strings.TrimSpace(r.URL.Query().Get("search")),
		From:   strings.TrimSpace(r.URL.Query().Get("from")),
		To:     strings.TrimSpace(r.URL.Query().Get("to")),
		Limit:  page.size,
		Offset: page.offset,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"total":     total,
		"page":      page.page,
		"page_size": page.size,
	})
}

func (s *Server) handleAuditClear(w http.ResponseWriter, r *http.Request) {
	before := strings.TrimSpace(r.URL.Query().Get("before"))
	n, err := s.Audit.Clear(before)
	if err != nil {
		writeError(w, err)
		return
	}
	detail := "清空全部审计日志"
	if before != "" {
		detail = "清理 " + before + " 之前的审计日志"
	}
	s.log(r, audit.ActionAuditClear, "audit", "", "", detail+"，共 "+strconv.FormatInt(n, 10)+" 条", true)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

func (s *Server) handleAuditSessions(w http.ResponseWriter, r *http.Request) {
	page := pageParams(r)
	items, total, err := s.Audit.ListSessions(audit.SessionFilter{
		UserID: strings.TrimSpace(r.URL.Query().Get("user_id")),
		SSHID:  strings.TrimSpace(r.URL.Query().Get("ssh_id")),
		Status: strings.TrimSpace(r.URL.Query().Get("status")),
		Search: strings.TrimSpace(r.URL.Query().Get("search")),
		Limit:  page.size,
		Offset: page.offset,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"total":     total,
		"page":      page.page,
		"page_size": page.size,
	})
}

func (s *Server) handleAuditSessionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.Audit.GetSession(id)
	if err != nil {
		writeError(w, err)
		return
	}
	if session == nil {
		writeError(w, apperr.ErrNotFound.WithMessage("会话记录不存在"))
		return
	}
	commands, err := s.Audit.ListCommands(id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session":  session,
		"commands": commands,
	})
}

func (s *Server) handleAuditSessionCommands(w http.ResponseWriter, r *http.Request) {
	commands, err := s.Audit.ListCommands(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": commands, "total": len(commands)})
}

func (s *Server) handleAuditRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.Audit.GetSession(id)
	if err != nil {
		writeError(w, err)
		return
	}
	if session == nil || !session.HasRecording {
		writeError(w, apperr.ErrNotFound.WithMessage("该会话没有录像文件"))
		return
	}
	data, err := s.Audit.ReadRecording(id)
	if err != nil {
		writeError(w, apperr.ErrNotFound.WithMessage("录像文件不可读"))
		return
	}
	w.Header().Set("Content-Type", "application/x-asciicast")
	w.Header().Set("Content-Disposition", "inline; filename=\""+id+".cast\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleAuditRecordingDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Audit.DeleteRecording(id); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionAuditClear, "session", id, "", "删除会话录像", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type pagination struct {
	page   int
	size   int
	offset int
}

func pageParams(r *http.Request) pagination {
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	size := queryInt(r, "page_size", 50)
	if size < 1 || size > 500 {
		size = 50
	}
	return pagination{page: page, size: size, offset: (page - 1) * size}
}
