package api

import (
	"net/http"
	"strings"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/db"
)

// SettingForceTOTPAll 强制全员启用 2FA 的配置键。
const SettingForceTOTPAll = "force_totp_all"

func (s *Server) registerSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", s.admin(s.handleSettingsGet))
	mux.HandleFunc("PUT /api/settings", s.admin(s.handleSettingsUpdate))
	mux.HandleFunc("GET /api/system/info", s.admin(s.handleSystemInfo))
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsSnapshot())
}

func (s *Server) settingsSnapshot() map[string]any {
	sshCount, _ := s.Infos.CountAll()
	trashCount, _ := s.Trash.Count()
	userCount := 0
	if items, err := s.Users.List(); err == nil {
		userCount = len(items)
	}
	cmdCount, _ := s.Commands.Count()
	auditCount, _ := s.Audit.Count()

	return map[string]any{
		"sftp_root":             s.SFTP.Root(),
		"trash_retention_days":  s.Trash.RetentionDays(),
		"force_totp_all":        s.Auth.ForcedTOTP(),
		"app_version":           s.Cfg.AppVersion,
		"session_idle_minutes":  int(s.Cfg.SessionIdleTimeout.Minutes()),
		"upload_chunk_size":     s.SFTP.ChunkSize(),
		"upload_max_file_mb":    s.SFTP.MaxFileBytes() / (1024 * 1024),
		"recording_max_mb":      s.Cfg.RecordingMaxMB,
		"ip_whitelist":          s.Cfg.IPWhitelist,
		"secure_cookie":         s.Cfg.SecureCookie,
		"reveal_warn_threshold": s.Cfg.RevealWarnThreshold,
		"counts": map[string]any{
			"ssh":      sshCount,
			"trash":    trashCount,
			"users":    userCount,
			"commands": cmdCount,
			"audit":    auditCount,
		},
	}
}

func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SFTPRoot       *string `json:"sftp_root"`
		TrashRetention *int    `json:"trash_retention_days"`
		ForceTOTPAll   *bool   `json:"force_totp_all"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}

	changes := make([]string, 0, 3)
	if req.SFTPRoot != nil {
		if err := s.SFTP.SetRoot(strings.TrimSpace(*req.SFTPRoot)); err != nil {
			writeError(w, err)
			return
		}
		changes = append(changes, "sftp_root="+s.SFTP.Root())
	}
	if req.TrashRetention != nil {
		if *req.TrashRetention < 0 || *req.TrashRetention > 3650 {
			writeError(w, apperr.ErrBadRequest.WithMessage("保留天数需在 0-3650 之间"))
			return
		}
		if err := s.Trash.SetRetentionDays(*req.TrashRetention); err != nil {
			writeError(w, err)
			return
		}
		changes = append(changes, "trash_retention_days="+itoa(*req.TrashRetention))
	}
	if req.ForceTOTPAll != nil {
		value := "0"
		if *req.ForceTOTPAll {
			value = "1"
		}
		if err := db.SettingSet(s.Conn, SettingForceTOTPAll, value); err != nil {
			writeError(w, err)
			return
		}
		changes = append(changes, "force_totp_all="+value)
	}

	s.log(r, audit.ActionSettingsUpdate, "settings", "", "", "更新系统设置："+strings.Join(changes, ", "), true)
	writeJSON(w, http.StatusOK, s.settingsSnapshot())
}

// handleSystemInfo 返回运行状态概览，供首页看板使用。
func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	activeSessions := len(s.Manager.List(""))
	records, _, err := s.Audit.ListSessions(audit.SessionFilter{Limit: 10})
	if err != nil {
		writeError(w, err)
		return
	}
	snapshot := s.settingsSnapshot()
	snapshot["active_sessions"] = activeSessions
	snapshot["recent_sessions"] = records
	writeJSON(w, http.StatusOK, snapshot)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 12)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}
