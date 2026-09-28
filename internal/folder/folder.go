// Package folder 实现多级文件夹（parent_id 自关联）。
package folder

import (
	"database/sql"
	"strings"

	"github.com/google/uuid"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/db"
)

// Folder 文件夹节点。
type Folder struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ParentID  *string   `json:"parent_id"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
	Children  []*Folder `json:"children,omitempty"`
	SSHCount  int       `json:"ssh_count"`
}

// Store 文件夹数据访问层。
type Store struct {
	conn *sql.DB
}

// NewStore 构造文件夹 Store。
func NewStore(conn *sql.DB) *Store { return &Store{conn: conn} }

// List 返回扁平列表。
func (s *Store) List() ([]*Folder, error) {
	rows, err := s.conn.Query(`SELECT id, name, parent_id, created_at, updated_at FROM folders ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Folder, 0, 8)
	for rows.Next() {
		f := &Folder{}
		var parent sql.NullString
		if err := rows.Scan(&f.ID, &f.Name, &parent, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.ParentID = db.NullStrPtr(parent)
		out = append(out, f)
	}
	return out, rows.Err()
}

// Tree 返回树形结构，并附带每个文件夹直属的 SSH 数量。
func (s *Store) Tree() ([]*Folder, error) {
	flat, err := s.List()
	if err != nil {
		return nil, err
	}
	counts, err := s.sshCounts()
	if err != nil {
		return nil, err
	}

	byID := make(map[string]*Folder, len(flat))
	for _, f := range flat {
		f.SSHCount = counts[f.ID]
		f.Children = []*Folder{}
		byID[f.ID] = f
	}

	roots := make([]*Folder, 0, len(flat))
	for _, f := range flat {
		if f.ParentID == nil {
			roots = append(roots, f)
			continue
		}
		parent, ok := byID[*f.ParentID]
		if !ok {
			// 父节点缺失（异常数据）时按根节点处理，避免整棵树丢失。
			roots = append(roots, f)
			continue
		}
		parent.Children = append(parent.Children, f)
	}
	return roots, nil
}

func (s *Store) sshCounts() (map[string]int, error) {
	rows, err := s.conn.Query(
		`SELECT folder_id, COUNT(*) FROM ssh_infos WHERE deleted_at IS NULL AND folder_id IS NOT NULL GROUP BY folder_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// Get 按 ID 查询文件夹。
func (s *Store) Get(id string) (*Folder, error) {
	f := &Folder{}
	var parent sql.NullString
	err := s.conn.QueryRow(
		`SELECT id, name, parent_id, created_at, updated_at FROM folders WHERE id = ?`, id).
		Scan(&f.ID, &f.Name, &parent, &f.CreatedAt, &f.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, apperr.ErrNotFound.WithMessage("文件夹不存在")
	}
	if err != nil {
		return nil, err
	}
	f.ParentID = db.NullStrPtr(parent)
	return f, nil
}

// Create 新建文件夹。
func (s *Store) Create(name string, parentID *string) (*Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 64 {
		return nil, apperr.ErrBadRequest.WithMessage("文件夹名称需为 1-64 个字符")
	}
	if err := s.assertParent(parentID, ""); err != nil {
		return nil, err
	}
	if err := s.assertSiblingName(name, parentID, ""); err != nil {
		return nil, err
	}

	now := db.NowStr()
	f := &Folder{ID: uuid.NewString(), Name: name, ParentID: parentID, CreatedAt: now, UpdatedAt: now}
	_, err := s.conn.Exec(
		`INSERT INTO folders (id, name, parent_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		f.ID, f.Name, nullable(f.ParentID), now, now)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Rename 重命名。
func (s *Store) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 64 {
		return apperr.ErrBadRequest.WithMessage("文件夹名称需为 1-64 个字符")
	}
	current, err := s.Get(id)
	if err != nil {
		return err
	}
	if err := s.assertSiblingName(name, current.ParentID, id); err != nil {
		return err
	}
	_, err = s.conn.Exec(`UPDATE folders SET name = ?, updated_at = ? WHERE id = ?`, name, db.NowStr(), id)
	return err
}

// Move 移动文件夹到新的父节点，禁止移动到自身或后代下。
func (s *Store) Move(id string, parentID *string) error {
	if _, err := s.Get(id); err != nil {
		return err
	}
	if parentID != nil && *parentID == id {
		return apperr.ErrBadRequest.WithMessage("不能将文件夹移动到自身")
	}
	if err := s.assertParent(parentID, id); err != nil {
		return err
	}
	if parentID != nil {
		desc, err := s.Descendants(id)
		if err != nil {
			return err
		}
		for _, d := range desc {
			if d == *parentID {
				return apperr.ErrBadRequest.WithMessage("不能将文件夹移动到其子文件夹中")
			}
		}
	}
	_, err := s.conn.Exec(`UPDATE folders SET parent_id = ?, updated_at = ? WHERE id = ?`,
		nullable(parentID), db.NowStr(), id)
	return err
}

// Delete 删除文件夹。cascade 为 true 时连同子文件夹一起删除，
// 其下的 SSH 信息一律进入回收站（不物理删除凭证数据）。
func (s *Store) Delete(id string, cascade bool) error {
	if _, err := s.Get(id); err != nil {
		return err
	}
	desc, err := s.Descendants(id)
	if err != nil {
		return err
	}
	if !cascade && len(desc) > 0 {
		return apperr.ErrConflict.WithMessage("文件夹下存在子文件夹，请先处理子文件夹或使用级联删除")
	}

	tx, err := s.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := db.NowStr()
	ids := append([]string{id}, desc...)
	for _, fid := range ids {
		if _, err := tx.Exec(
			`UPDATE ssh_infos SET deleted_at = ?, updated_at = ? WHERE folder_id = ? AND deleted_at IS NULL`,
			now, now, fid); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE folders SET parent_id = NULL WHERE parent_id = ?`, fid); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM folders WHERE id = ?`, fid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Descendants 返回指定文件夹的全部后代 ID（不含自身）。
func (s *Store) Descendants(id string) ([]string, error) {
	children, err := s.childrenMap()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, 4)
	queue := append([]string{}, children[id]...)
	visited := map[string]bool{id: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		out = append(out, cur)
		queue = append(queue, children[cur]...)
	}
	return out, nil
}

// SubtreeIDs 返回自身与全部后代 ID。
func (s *Store) SubtreeIDs(id string) ([]string, error) {
	desc, err := s.Descendants(id)
	if err != nil {
		return nil, err
	}
	return append([]string{id}, desc...), nil
}

func (s *Store) childrenMap() (map[string][]string, error) {
	rows, err := s.conn.Query(`SELECT id, parent_id FROM folders WHERE parent_id IS NOT NULL`)
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

func (s *Store) assertParent(parentID *string, selfID string) error {
	if parentID == nil {
		return nil
	}
	if *parentID == "" {
		return nil
	}
	if selfID != "" && *parentID == selfID {
		return apperr.ErrBadRequest.WithMessage("不能将文件夹移动到自身")
	}
	var one int
	err := s.conn.QueryRow(`SELECT 1 FROM folders WHERE id = ?`, *parentID).Scan(&one)
	if err == sql.ErrNoRows {
		return apperr.ErrBadRequest.WithMessage("父文件夹不存在")
	}
	return err
}

func (s *Store) assertSiblingName(name string, parentID *string, selfID string) error {
	var one int
	var err error
	if parentID == nil || *parentID == "" {
		err = s.conn.QueryRow(`SELECT 1 FROM folders WHERE name = ? AND parent_id IS NULL AND id <> ?`,
			name, selfID).Scan(&one)
	} else {
		err = s.conn.QueryRow(`SELECT 1 FROM folders WHERE name = ? AND parent_id = ? AND id <> ?`,
			name, *parentID, selfID).Scan(&one)
	}
	if err == nil {
		return apperr.ErrConflict.WithMessage("同级目录下已存在同名文件夹")
	}
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}

func nullable(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}
