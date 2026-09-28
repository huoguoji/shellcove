package api

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/auth"
	"github.com/huoguoji/shellcove/internal/backup"
	"github.com/huoguoji/shellcove/internal/crypto"
)

func (s *Server) registerBackupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/backup/preview", s.admin(s.handleBackupPreview))
	mux.HandleFunc("POST /api/backup/export/preview", s.admin(s.handleBackupExportPreview))
	mux.HandleFunc("POST /api/backup/export", s.admin(s.handleBackupExport))
	mux.HandleFunc("POST /api/backup/import/inspect", s.admin(s.handleBackupInspect))
	mux.HandleFunc("POST /api/backup/import", s.admin(s.handleBackupImport))
	mux.HandleFunc("GET /api/backup/files", s.admin(s.handleBackupFiles))
}

// pendingImports 暂存已预检的备份文件，导入时凭一次性令牌取回，避免重复上传。
type pendingImports struct {
	mu    sync.Mutex
	items map[string]pendingImport
	ttl   time.Duration
}

type pendingImport struct {
	data    []byte
	userID  string
	expires time.Time
}

func newPendingImports(ttl time.Duration) *pendingImports {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &pendingImports{items: map[string]pendingImport{}, ttl: ttl}
}

func (p *pendingImports) put(token, userID string, data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for key, item := range p.items {
		if item.expires.Before(now) {
			delete(p.items, key)
		}
	}
	p.items[token] = pendingImport{data: data, userID: userID, expires: now.Add(p.ttl)}
}

func (p *pendingImports) take(token, userID string) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	item, ok := p.items[token]
	if !ok {
		return nil, false
	}
	delete(p.items, token)
	if item.expires.Before(time.Now()) || item.userID != userID {
		return nil, false
	}
	return item.data, true
}

func (s *Server) handleBackupPreview(w http.ResponseWriter, r *http.Request) {
	includeAudit := queryBool(r, "include_audit")
	result, err := s.Backup.Preview(includeAudit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleBackupExportPreview 二次验证后返回数据概览与导出令牌（5 分钟有效）。
func (s *Server) handleBackupExportPreview(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Password    string `json:"password"`
		Code        string `json:"code"`
		VerifyToken string `json:"verify_token"`
		IncludeAudit bool  `json:"include_audit"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.requireSecondFactor(r, u, req.Password, req.Code, req.VerifyToken); err != nil {
		writeError(w, err)
		return
	}

	preview, err := s.Backup.Preview(req.IncludeAudit)
	if err != nil {
		writeError(w, err)
		return
	}
	if preview.NothingToExport {
		writeError(w, apperr.ErrNothingToExport)
		return
	}
	token, expiresAt, err := s.Auth.Tokens.Issue(u.ID, auth.PurposeExport, s.Cfg.ExportTokenTTL)
	if err != nil {
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionBackupExportPrev, "backup", "", "", "发起备份导出", true)
	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"preview":    preview,
		"export_token": token,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		ExportToken    string `json:"export_token"`
		BackupPassword string `json:"backup_password"`
		IncludeAudit   bool   `json:"include_audit"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	userID, err := s.Auth.Tokens.Consume(req.ExportToken, auth.PurposeExport)
	if err != nil || userID != u.ID {
		writeError(w, apperr.ErrExportTokenInvalid)
		return
	}
	if !crypto.ValidateBackupPassword(req.BackupPassword) {
		s.log(r, audit.ActionBackupExport, "backup", "", "", "备份密码强度不足", false)
		writeError(w, apperr.ErrWeakBackupPassword)
		return
	}

	var buf bytes.Buffer
	manifest, err := s.Backup.Export(&buf, req.BackupPassword, req.IncludeAudit)
	if err != nil {
		s.log(r, audit.ActionBackupExport, "backup", "", "", "导出失败："+apperr.MessageOf(err), false)
		writeError(w, err)
		return
	}

	name := "shellcove-backup-" + time.Now().UTC().Format("20060102-150405") + ".json.enc"
	s.log(r, audit.ActionBackupExport, "backup", "", name,
		"导出备份，文件级加密："+manifest.Encryption.Alg, true)

	noStore(w)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, &buf)
}

// handleBackupInspect 上传备份文件，读取明文 manifest 并完成版本预检。
func (s *Server) handleBackupInspect(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := readUploadedBackup(r)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := s.Backup.Inspect(data)
	if err != nil {
		s.log(r, audit.ActionBackupInspect, "backup", "", "", "预检失败："+apperr.MessageOf(err), false)
		writeError(w, err)
		return
	}

	token, expiresAt, err := s.Auth.Tokens.Issue(u.ID, auth.PurposeImport, s.Cfg.ImportTokenTTL)
	if err != nil {
		writeError(w, err)
		return
	}
	s.pending.put(token, u.ID, data)

	s.log(r, audit.ActionBackupInspect, "backup", "", "",
		"预检备份文件，版本 "+result.Version, true)
	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"inspect":     result,
		"import_token": token,
		"expires_at":  expiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleBackupImport(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		writeError(w, err)
		return
	}
	req, err := parseImportRequest(w, r)
	if err != nil {
		writeError(w, err)
		return
	}

	data := req.Data
	if len(data) == 0 {
		userID, err := s.Auth.Tokens.Consume(req.ImportToken, auth.PurposeImport)
		if err != nil || userID != u.ID {
			writeError(w, apperr.ErrImportTokenInvalid)
			return
		}
		cached, ok := s.pending.take(req.ImportToken, u.ID)
		if !ok {
			writeError(w, apperr.ErrImportTokenInvalid)
			return
		}
		data = cached
	}

	result, err := s.Backup.Import(backup.ImportRequest{
		Data:           data,
		BackupPassword: req.BackupPassword,
		MasterKeyHex:   req.MasterKey,
		Strategy:       req.Strategy,
		IncludeAudit:   req.IncludeAudit,
	})
	if err != nil {
		s.log(r, audit.ActionBackupImport, "backup", "", "", "导入失败："+apperr.MessageOf(err), false)
		writeError(w, err)
		return
	}
	s.log(r, audit.ActionBackupImport, "backup", "", "",
		"导入完成，策略 "+result.Strategy+"，重加密 "+boolText(result.ReEncrypted), true)
	writeJSON(w, http.StatusOK, result)
}

type importRequest struct {
	ImportToken    string
	BackupPassword string
	MasterKey      string
	Strategy       string
	IncludeAudit   bool
	Data           []byte
}

// parseImportRequest 兼容两种提交方式：JSON（配合 import_token）与 multipart 直传。
func parseImportRequest(w http.ResponseWriter, r *http.Request) (*importRequest, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return nil, apperr.ErrBadRequest.WithMessage("解析上传内容失败")
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			return nil, apperr.ErrInvalidFileFormat.WithMessage("缺少备份文件")
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxBodyBytes))
		if err != nil {
			return nil, apperr.ErrBadRequest.WithMessage("读取备份文件失败")
		}
		return &importRequest{
			BackupPassword: r.FormValue("backup_password"),
			MasterKey:      r.FormValue("master_key"),
			Strategy:       r.FormValue("strategy"),
			IncludeAudit:   parseBoolText(r.FormValue("include_audit")),
			Data:           data,
		}, nil
	}

	var payload struct {
		ImportToken    string `json:"import_token"`
		BackupPassword string `json:"backup_password"`
		MasterKey      string `json:"master_key"`
		Strategy       string `json:"strategy"`
		IncludeAudit   bool   `json:"include_audit"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		return nil, err
	}
	return &importRequest{
		ImportToken:    strings.TrimSpace(payload.ImportToken),
		BackupPassword: payload.BackupPassword,
		MasterKey:      strings.TrimSpace(payload.MasterKey),
		Strategy:       payload.Strategy,
		IncludeAudit:   payload.IncludeAudit,
	}, nil
}

func readUploadedBackup(r *http.Request) ([]byte, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, apperr.ErrBadRequest.WithMessage("解析上传内容失败")
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, apperr.ErrInvalidFileFormat.WithMessage("缺少备份文件")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBodyBytes))
	if err != nil {
		return nil, apperr.ErrBadRequest.WithMessage("读取备份文件失败")
	}
	if len(data) == 0 {
		return nil, apperr.ErrInvalidFileFormat
	}
	return data, nil
}

func parseBoolText(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func boolText(v bool) string {
	if v {
		return "已启用"
	}
	return "未启用"
}

// handleBackupFiles 列出导入前自动生成的快照文件。
func (s *Server) handleBackupFiles(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]any, 0, 16)
	entries, err := os.ReadDir(s.Cfg.BackupsDir)
	if err != nil && !os.IsNotExist(err) {
		writeError(w, apperr.ErrInternal.WithMessage("读取备份目录失败"))
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, map[string]any{
			"name":     entry.Name(),
			"size":     info.Size(),
			"modified": info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["name"].(string) > items[j]["name"].(string)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"dir":   filepath.ToSlash(s.Cfg.BackupsDir),
		"items": items,
		"total": len(items),
	})
}
