package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/folder"
	"github.com/huoguoji/shellcove/internal/monitor"
	"github.com/huoguoji/shellcove/internal/permission"
	"github.com/huoguoji/shellcove/internal/ssh"
	"github.com/huoguoji/shellcove/internal/sshinfo"
)

func (s *Server) registerResourceRoutes(mux *http.ServeMux) {
	// SSH 信息
	mux.HandleFunc("GET /api/ssh", s.protected(s.handleSSHList))
	mux.HandleFunc("POST /api/ssh", s.admin(s.handleSSHCreate))
	mux.HandleFunc("GET /api/ssh/{id}", s.protected(s.handleSSHGet))
	mux.HandleFunc("PUT /api/ssh/{id}", s.admin(s.handleSSHUpdate))
	mux.HandleFunc("DELETE /api/ssh/{id}", s.admin(s.handleSSHDelete))
	mux.HandleFunc("POST /api/ssh/{id}/reveal", s.protected(s.handleSSHReveal))
	mux.HandleFunc("POST /api/ssh/{id}/connect", s.protected(s.handleSSHConnect))
	mux.HandleFunc("GET /api/ssh/{id}/monitor", s.protected(s.handleSSHMonitor))

	// 文件夹
	mux.HandleFunc("GET /api/folders", s.protected(s.handleFolderTree))
	mux.HandleFunc("GET /api/folders/list", s.protected(s.handleFolderList))
	mux.HandleFunc("POST /api/folders", s.admin(s.handleFolderCreate))
	mux.HandleFunc("PUT /api/folders/{id}", s.admin(s.handleFolderUpdate))
	mux.HandleFunc("DELETE /api/folders/{id}", s.admin(s.handleFolderDelete))

	// 回收站
	mux.HandleFunc("GET /api/trash", s.admin(s.handleTrashList))
	mux.HandleFunc("POST /api/trash/{id}/restore", s.admin(s.handleTrashRestore))
	mux.HandleFunc("DELETE /api/trash/{id}", s.admin(s.handleTrashPurge))
	mux.HandleFunc("DELETE /api/trash", s.admin(s.handleTrashEmpty))
}

// ------------------------------------------------------------------ SSH 信息

func (s *Server) handleSSHList(w http.ResponseWriter, r *http.Request) {
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

	items, err := s.Infos.List(sshinfo.ListFilter{
		Search:    strings.TrimSpace(r.URL.Query().Get("search")),
		FolderID:  strings.TrimSpace(r.URL.Query().Get("folder_id")),
		Recursive: queryBool(r, "recursive"),
	})
	if err != nil {
		writeError(w, err)
		return
	}

	out := make([]*sshinfo.Info, 0, len(items))
	for _, item := range items {
		if resolution.Allows(item.ID) {
			out = append(out, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

func (s *Server) handleSSHGet(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.requireSSHAccess(r, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	info, err := s.Infos.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleSSHCreate(w http.ResponseWriter, r *http.Request) {
	var in sshinfo.Input
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	info, err := s.Infos.Create(&in)
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSSHCreate, "ssh", info.ID, info.Name, info.Host, true)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleSSHUpdate(w http.ResponseWriter, r *http.Request) {
	var in sshinfo.Input
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("id")
	info, err := s.Infos.Update(id, &in)
	if err != nil {
		writeError(w, err)
		return
	}
	// 凭证可能已变更，丢弃基于旧凭证建立的缓存连接。
	s.SFTP.CloseSSH(id)
	s.SSHPool.Drop(id)
	s.log(r, audit.ActionSSHUpdate, "ssh", id, info.Name, "更新 SSH 信息", true)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleSSHDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, err := s.Infos.Get(id)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.Infos.SoftDelete(id); err != nil {
		writeError(w, err)
		return
	}
	// 删除后立即断开该主机的所有活动会话与缓存连接。
	s.Manager.CloseSSH(id, "主机已被删除")
	s.SFTP.CloseSSH(id)
	s.SSHPool.Drop(id)
	s.log(r, audit.ActionSSHDelete, "ssh", id, info.Name, "移入回收站", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSSHReveal 二次验证后返回明文凭证，响应禁止缓存并记审计。
func (s *Server) handleSSHReveal(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("id")
	if _, _, err := s.requireSSHAccess(r, id); err != nil {
		writeError(w, err)
		return
	}

	var req struct {
		Password    string `json:"password"`
		Code        string `json:"code"`
		VerifyToken string `json:"verify_token"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	tokenUsed, err := s.requireSecondFactor(r, u, req.Password, req.Code, req.VerifyToken)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = tokenUsed

	secret, err := s.Infos.Reveal(id)
	if err != nil {
		writeError(w, err)
		return
	}
	info, _ := s.Infos.Get(id)
	name := id
	if info != nil {
		name = info.Name
	}
	s.log(r, audit.ActionSSHReveal, "ssh", id, name, "查看明文凭证", true)

	// 短时间内频繁查看凭证视为异常行为，额外记录告警日志。
	if exceeded := s.Auth.RevealCounter.Hit(u.ID); exceeded {
		s.log(r, audit.ActionSSHRevealAlert, "ssh", id, name,
			"短时间内多次查看凭证，请注意账号安全", true)
	}

	noStore(w)
	writeJSON(w, http.StatusOK, secret)
}

func (s *Server) handleSSHConnect(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("id")
	resolution, info, err := s.requireSSHAccess(r, id)
	if err != nil {
		writeError(w, err)
		return
	}

	var req struct {
		Cols  int    `json:"cols"`
		Rows  int    `json:"rows"`
		Title string `json:"title"`
	}
	_ = decodeJSONOptional(r, &req)

	// 会话数上限（0 表示不限制）。
	if u.MaxSessions > 0 && s.Manager.CountByUser(u.ID) >= u.MaxSessions {
		writeError(w, apperr.ErrSessionLimit)
		return
	}

	creds, err := s.Infos.Credentials(id)
	if err != nil {
		writeError(w, err)
		return
	}
	session, err := s.Manager.Create(ssh.CreateOptions{
		UserID:   u.ID,
		Username: u.Username,
		SSHID:    id,
		SSHName:  info.Name,
		Title:    req.Title,
		ClientIP: clientIP(r),
		Creds:    creds,
		Cols:     req.Cols,
		Rows:     req.Rows,
	})
	if err != nil {
		s.log(r, audit.ActionSSHConnect, "ssh", id, info.Name, "连接失败："+err.Error(), false)
		writeError(w, err)
		return
	}

	monitored := info.MonitorEnabled && resolution.AllowsMonitor(id)
	s.log(r, audit.ActionSSHConnect, "ssh", id, info.Name, "建立 SSH 会话", true)
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": session.ID,
		"ws_url":     "/api/ws/terminal/" + session.ID,
		"monitored":  monitored,
	})
}

func (s *Server) handleSSHMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, info, err := s.requireSSHAccess(r, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if !info.MonitorEnabled {
		writeError(w, apperr.ErrBadRequest.WithMessage("该主机未开启监控采集"))
		return
	}
	creds, err := s.Infos.Credentials(id)
	if err != nil {
		writeError(w, err)
		return
	}
	// 复用连接：面板每 5 秒采集一次，重复握手既慢又会堆积短命会话。
	client, reused, err := s.SSHPool.Get(id, creds)
	if err != nil {
		writeError(w, apperr.ErrInternal.WithMessage("连接主机失败"))
		return
	}

	snapshot, err := monitor.Collect(client)
	if err != nil && reused && !monitor.IsTimeout(err) {
		// 缓存的连接可能已被对端断开，重建一次后重试。
		if fresh, derr := s.SSHPool.Reset(id, creds, client); derr == nil {
			snapshot, err = monitor.Collect(fresh)
		}
	}
	if err != nil {
		writeError(w, apperr.ErrInternal.WithMessage("读取监控数据失败"))
		return
	}
	s.log(r, audit.ActionMonitorRead, "ssh", id, info.Name, "读取监控快照", true)
	writeJSON(w, http.StatusOK, snapshot)
}

// requireSSHAccess 校验当前用户对指定 SSH 信息的访问权限。
func (s *Server) requireSSHAccess(r *http.Request, id string) (*permission.Resolution, *sshinfo.Info, error) {
	u, err := s.currentUser(r)
	if err != nil {
		return nil, nil, err
	}
	resolution, err := s.Perms.Resolve(u)
	if err != nil {
		return nil, nil, err
	}
	if !resolution.Allows(id) {
		return nil, nil, apperr.ErrNoPermission
	}
	info, err := s.Infos.Get(id)
	if err != nil {
		return nil, nil, err
	}
	return resolution, info, nil
}

// ------------------------------------------------------------------ 文件夹

func (s *Server) handleFolderTree(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	tree, err := s.Folders.Tree()
	if err != nil {
		writeError(w, err)
		return
	}
	if u.IsAdmin() {
		writeJSON(w, http.StatusOK, map[string]any{"items": tree})
		return
	}
	// 普通用户仅保留有权访问的分支。
	resolution, err := s.Perms.Resolve(u)
	if err != nil {
		writeError(w, err)
		return
	}
	allowed := map[string]bool{}
	for id := range resolution.Filter() {
		allowed[id] = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": pruneTree(tree, allowed)})
}

func pruneTree(nodes []*folder.Folder, allowed map[string]bool) []*folder.Folder {
	out := make([]*folder.Folder, 0, len(nodes))
	for _, node := range nodes {
		children := pruneTree(node.Children, allowed)
		if allowed[node.ID] || len(children) > 0 {
			clone := *node
			clone.Children = children
			out = append(out, &clone)
		}
	}
	return out
}

func (s *Server) handleFolderList(w http.ResponseWriter, r *http.Request) {
	items, err := s.Folders.List()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleFolderCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string  `json:"name"`
		ParentID *string `json:"parent_id"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	item, err := s.Folders.Create(strings.TrimSpace(req.Name), req.ParentID)
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionFolderCreate, "folder", item.ID, item.Name, "新建文件夹", true)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleFolderUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// parent_id 用 RawMessage：字段缺省表示不移动，显式传 null 表示移动到根目录。
	var req struct {
		Name     string          `json:"name"`
		ParentID json.RawMessage `json:"parent_id"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		if err := s.Folders.Rename(id, strings.TrimSpace(req.Name)); err != nil {
			writeError(w, err)
			return
		}
	}
	if len(req.ParentID) > 0 {
		var parentID *string
		if err := json.Unmarshal(req.ParentID, &parentID); err != nil {
			writeError(w, apperr.ErrBadRequest.WithMessage("上级文件夹参数不合法"))
			return
		}
		if err := s.Folders.Move(id, parentID); err != nil {
			writeError(w, err)
			return
		}
	}
	item, err := s.Folders.Get(id)
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionFolderUpdate, "folder", id, item.Name, "修改文件夹", true)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleFolderDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.Folders.Get(id)
	if err != nil {
		writeError(w, err)
		return
	}
	cascade := queryBool(r, "cascade")
	if err := s.Folders.Delete(id, cascade); err != nil {
		writeError(w, err)
		return
	}
	detail := "删除文件夹"
	if cascade {
		detail = "级联删除文件夹及其子项"
	}
	s.log(r, audit.ActionFolderDelete, "folder", id, item.Name, detail, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ------------------------------------------------------------------ 回收站

func (s *Server) handleTrashList(w http.ResponseWriter, r *http.Request) {
	items, err := s.Trash.List()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":          items,
		"total":          len(items),
		"retention_days": s.Trash.RetentionDays(),
	})
}

func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Trash.Restore(id); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSSHRestore, "ssh", id, "", "从回收站恢复", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTrashPurge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, _ := s.Infos.Get(id)
	name := id
	if info != nil {
		name = info.Name
	}
	if err := s.Trash.Purge(id); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSSHPurge, "ssh", id, name, "彻底删除", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	n, err := s.Trash.Empty()
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionTrashEmpty, "trash", "", "", "清空回收站", true)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}
