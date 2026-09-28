package api

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/permission"
	"github.com/huoguoji/shellcove/internal/sshinfo"
)

func (s *Server) registerFileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sftp/root", s.admin(s.handleSFTPRootGet))
	mux.HandleFunc("PUT /api/sftp/root", s.admin(s.handleSFTPRootSet))

	mux.HandleFunc("GET /api/sftp/{id}/list", s.protected(s.handleSFTPList))
	mux.HandleFunc("GET /api/sftp/{id}/stat", s.protected(s.handleSFTPStat))
	mux.HandleFunc("POST /api/sftp/{id}/mkdir", s.protected(s.handleSFTPMkdir))
	mux.HandleFunc("POST /api/sftp/{id}/rename", s.protected(s.handleSFTPRename))
	mux.HandleFunc("POST /api/sftp/{id}/chmod", s.protected(s.handleSFTPChmod))
	mux.HandleFunc("POST /api/sftp/{id}/delete", s.protected(s.handleSFTPDelete))
	mux.HandleFunc("GET /api/sftp/{id}/download", s.protected(s.handleSFTPDownload))
	mux.HandleFunc("GET /api/sftp/{id}/read", s.protected(s.handleSFTPRead))
	mux.HandleFunc("POST /api/sftp/{id}/write", s.protected(s.handleSFTPWrite))
	mux.HandleFunc("POST /api/sftp/{id}/upload-simple", s.protected(s.handleSFTPUploadSimple))

	mux.HandleFunc("POST /api/sftp/{id}/upload/init", s.protected(s.handleUploadInit))
	mux.HandleFunc("PUT /api/sftp/{id}/upload/{uploadId}/chunk", s.protected(s.handleUploadChunk))
	mux.HandleFunc("POST /api/sftp/{id}/upload/{uploadId}/complete", s.protected(s.handleUploadComplete))
	mux.HandleFunc("DELETE /api/sftp/{id}/upload/{uploadId}", s.protected(s.handleUploadAbort))
	mux.HandleFunc("GET /api/sftp/{id}/uploads", s.protected(s.handleUploadList))
}

// requireSFTPAccess 校验 SFTP 权限并解密连接凭证。
func (s *Server) requireSFTPAccess(r *http.Request, id string) (*permission.Resolution, *sshinfo.Credentials, *sshinfo.Info, error) {
	u, err := s.currentUser(r)
	if err != nil {
		return nil, nil, nil, err
	}
	resolution, err := s.Perms.Resolve(u)
	if err != nil {
		return nil, nil, nil, err
	}
	if !resolution.AllowsSFTP(id) {
		return nil, nil, nil, apperr.ErrNoPermission.WithMessage("没有该主机的文件传输权限")
	}
	info, err := s.Infos.Get(id)
	if err != nil {
		return nil, nil, nil, err
	}
	creds, err := s.Infos.Credentials(id)
	if err != nil {
		return nil, nil, nil, err
	}
	return resolution, creds, info, nil
}

func (s *Server) handleSFTPRootGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"root": s.SFTP.Root()})
}

func (s *Server) handleSFTPRootSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Root string `json:"root"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.SFTP.SetRoot(req.Root); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSettingsUpdate, "settings", "", "", "设置 SFTP 根目录："+s.SFTP.Root(), true)
	writeJSON(w, http.StatusOK, map[string]any{"root": s.SFTP.Root()})
}

func (s *Server) handleSFTPList(w http.ResponseWriter, r *http.Request) {
	_, creds, _, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("path"))
	entries, resolved, err := s.SFTP.List(r.PathValue("id"), creds, target)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":     resolved,
		"parent":   parentPath(resolved),
		"root":     s.SFTP.Root(),
		"home":     s.SFTP.HomeDir(r.PathValue("id"), creds),
		"items":    entries,
		"total":    len(entries),
		"writable": true,
	})
}

func parentPath(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	parent := path.Dir(p)
	if parent == "." {
		return "/"
	}
	return parent
}

func (s *Server) handleSFTPStat(w http.ResponseWriter, r *http.Request) {
	_, creds, _, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	entry, err := s.SFTP.Stat(r.PathValue("id"), creds, strings.TrimSpace(r.URL.Query().Get("path")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) handleSFTPMkdir(w http.ResponseWriter, r *http.Request) {
	_, creds, info, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.SFTP.Mkdir(r.PathValue("id"), creds, req.Path); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSFTPMkdir, "ssh", r.PathValue("id"), info.Name, "新建目录："+req.Path, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSFTPRename(w http.ResponseWriter, r *http.Request) {
	_, creds, info, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.SFTP.Rename(r.PathValue("id"), creds, req.From, req.To); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSFTPRename, "ssh", r.PathValue("id"), info.Name,
		"重命名："+req.From+" → "+req.To, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSFTPChmod(w http.ResponseWriter, r *http.Request) {
	_, creds, info, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(req.Mode), 8, 32)
	if err != nil {
		writeError(w, apperr.ErrBadRequest.WithMessage("权限值需为八进制，例如 0644"))
		return
	}
	if err := s.SFTP.Chmod(r.PathValue("id"), creds, req.Path, os.FileMode(parsed)); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSFTPChmod, "ssh", r.PathValue("id"), info.Name,
		"修改权限："+req.Path+" → "+req.Mode, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSFTPDelete(w http.ResponseWriter, r *http.Request) {
	_, creds, info, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.SFTP.Remove(r.PathValue("id"), creds, req.Path, req.Recursive); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSFTPDelete, "ssh", r.PathValue("id"), info.Name, "删除："+req.Path, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSFTPDownload(w http.ResponseWriter, r *http.Request) {
	_, creds, info, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("path"))
	reader, entry, err := s.SFTP.OpenRead(r.PathValue("id"), creds, target)
	if err != nil {
		writeError(w, err)
		return
	}
	defer reader.Close()

	name := path.Base(entry.Path)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Content-Type", "application/octet-stream")
	if entry.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(entry.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	written, _ := io.Copy(w, reader)
	// 小文件的响应体会先落在 http 服务器的 bufio 里，直到处理器返回才写出；
	// 这里立即 Flush，避免浏览器还要等审计写库等收尾动作。
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	s.log(r, audit.ActionSFTPDownload, "ssh", r.PathValue("id"), info.Name,
		"下载文件："+entry.Path+"（"+strconv.FormatInt(written, 10)+" 字节）", true)
}

// maxTextPreviewBytes 文本预览上限（1MB）。
const maxTextPreviewBytes = 1 << 20

func (s *Server) handleSFTPRead(w http.ResponseWriter, r *http.Request) {
	_, creds, _, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("path"))
	reader, entry, err := s.SFTP.OpenRead(r.PathValue("id"), creds, target)
	if err != nil {
		writeError(w, err)
		return
	}
	defer reader.Close()

	if entry.Size > maxTextPreviewBytes {
		writeError(w, apperr.ErrPayloadTooLarge.WithMessage("文件过大，无法在线预览"))
		return
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxTextPreviewBytes))
	if err != nil {
		writeError(w, apperr.ErrInternal.WithMessage("读取文件失败"))
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"path":     entry.Path,
		"name":     entry.Name,
		"size":     entry.Size,
		"encoding": "utf-8",
		"content":  string(content),
	})
}

func (s *Server) handleSFTPWrite(w http.ResponseWriter, r *http.Request) {
	_, creds, info, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Base64  bool   `json:"base64"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	payload := []byte(req.Content)
	if req.Base64 {
		decoded, err := base64.StdEncoding.DecodeString(req.Content)
		if err != nil {
			writeError(w, apperr.ErrBadRequest.WithMessage("Base64 内容不合法"))
			return
		}
		payload = decoded
	}
	if int64(len(payload)) > s.SFTP.MaxFileBytes() {
		writeError(w, apperr.ErrPayloadTooLarge.WithMessage("文件超过允许的大小上限"))
		return
	}
	if err := s.SFTP.WriteFile(r.PathValue("id"), creds, req.Path, payload); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSFTPUpload, "ssh", r.PathValue("id"), info.Name, "保存文件："+req.Path, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "size": len(payload)})
}

// handleSFTPUploadSimple 处理小文件整体上传（multipart/form-data）。
func (s *Server) handleSFTPUploadSimple(w http.ResponseWriter, r *http.Request) {
	_, creds, info, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	limit := s.SFTP.MaxFileBytes() + (1 << 20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, apperr.ErrBadRequest.WithMessage("解析上传内容失败"))
		return
	}
	dir := strings.TrimSpace(r.FormValue("dir"))
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, apperr.ErrBadRequest.WithMessage("缺少上传文件"))
		return
	}
	defer file.Close()
	if header.Size > limit {
		writeError(w, apperr.ErrPayloadTooLarge.WithMessage("文件超过允许的大小上限"))
		return
	}
	target := path.Join(dirOrRoot(dir), header.Filename)
	if err := s.SFTP.SaveStream(r.PathValue("id"), creds, target, file, header.Size); err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSFTPUpload, "ssh", r.PathValue("id"), info.Name,
		"上传文件："+target+"（"+strconv.FormatInt(header.Size, 10)+" 字节）", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": target, "size": header.Size})
}

func dirOrRoot(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "/"
	}
	if !strings.HasPrefix(dir, "/") {
		return "/" + dir
	}
	return dir
}

// ------------------------------------------------------------------ 断点续传

func (s *Server) handleUploadInit(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	_, creds, info, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Dir      string `json:"dir"`
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
		FileHash string `json:"file_hash"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	upload, err := s.SFTP.InitUpload(u.ID, r.PathValue("id"), creds,
		dirOrRoot(req.Dir), strings.TrimSpace(req.Filename), req.Size, req.FileHash)
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSFTPUpload, "ssh", r.PathValue("id"), info.Name,
		"开始上传："+upload.Filename, true)
	writeJSON(w, http.StatusOK, map[string]any{
		"upload":     upload,
		"chunk_size": s.SFTP.ChunkSize(),
	})
}

func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	_, creds, _, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	offset := int64(queryInt(r, "offset", -1))
	if offset < 0 {
		writeError(w, apperr.ErrBadRequest.WithMessage("缺少分片偏移量"))
		return
	}
	limit := s.SFTP.ChunkSize() + (64 << 10)
	data, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		writeError(w, apperr.ErrBadRequest.WithMessage("读取分片数据失败"))
		return
	}
	if len(data) == 0 {
		writeError(w, apperr.ErrBadRequest.WithMessage("分片内容为空"))
		return
	}
	upload, err := s.SFTP.WriteChunk(u.ID, r.PathValue("id"), creds, r.PathValue("uploadId"), offset, data)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, upload)
}

func (s *Server) handleUploadComplete(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	_, creds, info, err := s.requireSFTPAccess(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	upload, err := s.SFTP.CompleteUpload(u.ID, r.PathValue("id"), creds, r.PathValue("uploadId"))
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionSFTPUpload, "ssh", r.PathValue("id"), info.Name,
		"完成上传："+upload.RemotePath+"（"+strconv.FormatInt(upload.TotalSize, 10)+" 字节）", true)
	writeJSON(w, http.StatusOK, upload)
}

func (s *Server) handleUploadAbort(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, _, _, err := s.requireSFTPAccess(r, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	if err := s.SFTP.AbortUpload(u.ID, r.PathValue("id"), r.PathValue("uploadId")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUploadList(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, _, _, err := s.requireSFTPAccess(r, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	items, err := s.SFTP.ListUploads(u.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}
