// Package permission 实现基于资源树的授权与权限继承。
//
// 资源类型：all（全部）、folder（文件夹，含全部子文件夹）、ssh（单个 SSH 信息）。
package permission

import (
	"database/sql"
	"strings"

	"github.com/google/uuid"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/db"
	"github.com/huoguoji/shellcove/internal/user"
)

// 资源类型。
const (
	TypeAll    = "all"
	TypeFolder = "folder"
	TypeSSH    = "ssh"
)

// Grant 一条授权记录。
type Grant struct {
	ID           string  `json:"id"`
	UserID       string  `json:"user_id"`
	ResourceType string  `json:"resource_type"`
	ResourceID   *string `json:"resource_id"`
	CanSFTP      bool    `json:"can_sftp"`
	CanMonitor   bool    `json:"can_monitor"`
	CreatedAt    string  `json:"created_at"`
}

// Service 权限服务。
type Service struct {
	conn *sql.DB
}

// New 构造权限服务。
func New(conn *sql.DB) *Service { return &Service{conn: conn} }

// List 返回某用户的全部授权。
func (s *Service) List(userID string) ([]*Grant, error) {
	rows, err := s.conn.Query(
		`SELECT id, user_id, resource_type, resource_id, can_sftp, can_monitor, created_at
		 FROM permissions WHERE user_id = ? ORDER BY resource_type, created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Grant, 0, 8)
	for rows.Next() {
		g := &Grant{}
		var resID sql.NullString
		if err := rows.Scan(&g.ID, &g.UserID, &g.ResourceType, &resID, &g.CanSFTP, &g.CanMonitor, &g.CreatedAt); err != nil {
			return nil, err
		}
		g.ResourceID = db.NullStrPtr(resID)
		out = append(out, g)
	}
	return out, rows.Err()
}

// Replace 全量替换某用户的授权（事务）。
func (s *Service) Replace(userID string, grants []*Grant) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM permissions WHERE user_id = ?`, userID); err != nil {
		return err
	}

	seen := map[string]bool{}
	now := db.NowStr()
	for _, g := range grants {
		switch g.ResourceType {
		case TypeAll, TypeFolder, TypeSSH:
		default:
			return apperr.ErrBadRequest.WithMessage("授权资源类型不合法: " + g.ResourceType)
		}
		key := g.ResourceType + "|" + deref(g.ResourceID)
		if seen[key] {
			continue
		}
		seen[key] = true

		if g.ResourceType != TypeAll && (g.ResourceID == nil || *g.ResourceID == "") {
			return apperr.ErrBadRequest.WithMessage("授权缺少资源 ID")
		}
		if g.ResourceType == TypeAll {
			if _, err := tx.Exec(
				`INSERT INTO permissions (id, user_id, resource_type, resource_id, can_sftp, can_monitor, created_at)
				 VALUES (?, ?, ?, NULL, 1, 1, ?)`, uuid.NewString(), userID, TypeAll, now); err != nil {
				return err
			}
			continue
		}
		if err := s.assertResourceExists(tx, g.ResourceType, *g.ResourceID); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO permissions (id, user_id, resource_type, resource_id, can_sftp, can_monitor, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), userID, g.ResourceType, *g.ResourceID, g.CanSFTP, g.CanMonitor, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) assertResourceExists(tx *sql.Tx, resourceType, resourceID string) error {
	var table string
	switch resourceType {
	case TypeFolder:
		table = "folders"
	case TypeSSH:
		table = "ssh_infos"
	default:
		return apperr.ErrBadRequest
	}
	var one int
	err := tx.QueryRow(`SELECT 1 FROM `+table+` WHERE id = ?`, resourceID).Scan(&one)
	if err == sql.ErrNoRows {
		return apperr.ErrBadRequest.WithMessage("授权资源不存在")
	}
	return err
}

// Resolution 用户权限解析结果。
type Resolution struct {
	All       bool
	SSHIDs    map[string]bool
	noSFTP    map[string]bool
	noMonitor map[string]bool
}

// Allows 判断是否可访问指定 SSH 信息。
func (r *Resolution) Allows(sshID string) bool {
	if r == nil {
		return false
	}
	return r.All || r.SSHIDs[sshID]
}

// AllowsSFTP 判断是否可使用 SFTP。
func (r *Resolution) AllowsSFTP(sshID string) bool {
	if !r.Allows(sshID) {
		return false
	}
	return !r.noSFTP[sshID]
}

// AllowsMonitor 判断是否可读取监控快照。
func (r *Resolution) AllowsMonitor(sshID string) bool {
	if !r.Allows(sshID) {
		return false
	}
	return !r.noMonitor[sshID]
}

// Filter 过滤出可访问的 SSH ID 集合。
func (r *Resolution) Filter() map[string]bool {
	if r == nil {
		return map[string]bool{}
	}
	return r.SSHIDs
}

// Resolve 计算用户的权限视图；管理员拥有全部权限。
func (s *Service) Resolve(u *user.User) (*Resolution, error) {
	res := &Resolution{
		SSHIDs:    map[string]bool{},
		noSFTP:    map[string]bool{},
		noMonitor: map[string]bool{},
	}
	if u == nil {
		return res, nil
	}
	if u.IsAdmin() {
		res.All = true
		return res, nil
	}

	grants, err := s.List(u.ID)
	if err != nil {
		return nil, err
	}
	// 先展开全部文件夹父子关系，用于权限继承。
	children, err := s.folderChildren()
	if err != nil {
		return nil, err
	}
	sshByFolder, err := s.sshByFolder()
	if err != nil {
		return nil, err
	}

	for _, g := range grants {
		switch g.ResourceType {
		case TypeAll:
			res.All = true
		case TypeSSH:
			if g.ResourceID == nil {
				continue
			}
			res.SSHIDs[*g.ResourceID] = true
			if !g.CanSFTP {
				res.noSFTP[*g.ResourceID] = true
			}
			if !g.CanMonitor {
				res.noMonitor[*g.ResourceID] = true
			}
		case TypeFolder:
			if g.ResourceID == nil {
				continue
			}
			// 勾选文件夹 = 授权其下所有（含子文件夹）。
			for _, fid := range descendants(*g.ResourceID, children) {
				for _, sshID := range sshByFolder[fid] {
					res.SSHIDs[sshID] = true
					// 取消勾选的子项优先级更高，这里只在未显式禁止时放行。
					if !g.CanSFTP {
						res.noSFTP[sshID] = true
					} else {
						delete(res.noSFTP, sshID)
					}
					if !g.CanMonitor {
						res.noMonitor[sshID] = true
					} else {
						delete(res.noMonitor, sshID)
					}
				}
			}
		}
	}
	return res, nil
}

// folderChildren 返回 folderID -> 直接子文件夹 ID 列表。
func (s *Service) folderChildren() (map[string][]string, error) {
	rows, err := s.conn.Query(`SELECT id, COALESCE(parent_id, '') FROM folders`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var id, parent string
		if err := rows.Scan(&id, &parent); err != nil {
			return nil, err
		}
		out[parent] = append(out[parent], id)
	}
	return out, rows.Err()
}

// sshByFolder 返回 folderID -> 该文件夹下的 SSH 信息 ID 列表（排除回收站）。
func (s *Service) sshByFolder() (map[string][]string, error) {
	rows, err := s.conn.Query(`SELECT id, folder_id FROM ssh_infos WHERE deleted_at IS NULL AND folder_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var id string
		var folder sql.NullString
		if err := rows.Scan(&id, &folder); err != nil {
			return nil, err
		}
		if folder.Valid {
			out[folder.String] = append(out[folder.String], id)
		}
	}
	return out, rows.Err()
}

// descendants 返回 root 自身及其全部后代文件夹 ID。
func descendants(root string, children map[string][]string) []string {
	out := []string{root}
	queue := []string{root}
	visited := map[string]bool{root: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range children[cur] {
			if visited[child] {
				continue
			}
			visited[child] = true
			out = append(out, child)
			queue = append(queue, child)
		}
	}
	return out
}

// Sanitize 归一化前端提交的授权列表。
func Sanitize(raw []*Grant) []*Grant {
	out := make([]*Grant, 0, len(raw))
	for _, g := range raw {
		if g == nil {
			continue
		}
		g.ResourceType = strings.TrimSpace(g.ResourceType)
		if g.ResourceType == TypeAll {
			g.ResourceID = nil
			g.CanSFTP = true
			g.CanMonitor = true
		}
		if g.ResourceID != nil {
			id := strings.TrimSpace(*g.ResourceID)
			if id == "" {
				g.ResourceID = nil
			} else {
				g.ResourceID = &id
			}
		}
		out = append(out, g)
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
