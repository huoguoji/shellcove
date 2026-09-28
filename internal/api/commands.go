package api

import (
	"net/http"
	"strings"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/command"
)

func (s *Server) registerCommandRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/commands", s.protected(s.handleCommandList))
	mux.HandleFunc("POST /api/commands", s.admin(s.handleCommandCreate))
	mux.HandleFunc("PUT /api/commands/{id}", s.admin(s.handleCommandUpdate))
	mux.HandleFunc("DELETE /api/commands/{id}", s.admin(s.handleCommandDelete))
	mux.HandleFunc("POST /api/commands/{id}/send", s.protected(s.handleCommandSend))
	mux.HandleFunc("POST /api/commands/send", s.protected(s.handleCommandSendRaw))

	mux.HandleFunc("POST /api/command-groups", s.admin(s.handleGroupCreate))
	mux.HandleFunc("PUT /api/command-groups/{id}", s.admin(s.handleGroupUpdate))
	mux.HandleFunc("DELETE /api/command-groups/{id}", s.admin(s.handleGroupDelete))
}

func (s *Server) handleCommandList(w http.ResponseWriter, r *http.Request) {
	groups, err := s.Commands.Groups()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := s.Commands.List(strings.TrimSpace(r.URL.Query().Get("search")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"groups": groups,
		"items":  items,
		"total":  len(items),
	})
}

func (s *Server) handleCommandCreate(w http.ResponseWriter, r *http.Request) {
	var in command.Input
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	item, err := s.Commands.Create(&in)
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionCommandCreate, "command", item.ID, item.Name, "新建命令", true)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleCommandUpdate(w http.ResponseWriter, r *http.Request) {
	var in command.Input
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("id")
	item, err := s.Commands.Update(id, &in)
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionCommandUpdate, "command", id, item.Name, "更新命令", true)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleCommandDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.Commands.Get(id)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.Commands.Delete(id); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionCommandDelete, "command", id, item.Name, "删除命令", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCommandSend(w http.ResponseWriter, r *http.Request) {
	item, err := s.Commands.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	s.sendToSession(w, r, req.SessionID, item.Content, item.Name)
}

func (s *Server) handleCommandSendRaw(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		Content   string `json:"content"`
		Name      string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, apperr.ErrBadRequest.WithMessage("命令内容不能为空"))
		return
	}
	s.sendToSession(w, r, req.SessionID, req.Content, req.Name)
}

// sendToSession 将命令内容写入指定终端会话（自动补回车）。
func (s *Server) sendToSession(w http.ResponseWriter, r *http.Request, sessionID, content, name string) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	session, ok := s.Manager.Get(strings.TrimSpace(sessionID))
	if !ok {
		writeError(w, apperr.ErrNotFound.WithMessage("会话不存在或已结束"))
		return
	}
	if !u.IsAdmin() && session.UserID != u.ID {
		writeError(w, apperr.ErrNoPermission)
		return
	}
	if err := session.WriteInput([]byte(strings.TrimRight(content, "\n") + "\r")); err != nil {
		writeError(w, apperr.ErrInternal.WithMessage("命令发送失败"))
		return
	}
	if strings.TrimSpace(name) == "" {
		name = content
	}
	s.log(r, audit.ActionCommandSend, "session", session.ID, session.SSHName, "执行命令："+name, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	group, err := s.Commands.CreateGroup(strings.TrimSpace(req.Name), req.SortOrder)
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionCommandCreate, "command_group", group.ID, group.Name, "新建命令分组", true)
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) handleGroupUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("id")
	if err := s.Commands.UpdateGroup(id, strings.TrimSpace(req.Name), req.SortOrder); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionCommandUpdate, "command_group", id, req.Name, "更新命令分组", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Commands.DeleteGroup(id); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionCommandDelete, "command_group", id, "", "删除命令分组", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
