// Package command 实现命令分组与命令片段管理。
package command

import (
	"database/sql"
	"strings"

	"github.com/google/uuid"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/db"
)

// Group 命令分类。
type Group struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	SortOrder int        `json:"sort_order"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
	Commands  []*Command `json:"commands"`
}

// Command 命令片段。
type Command struct {
	ID        string  `json:"id"`
	GroupID   *string `json:"group_id"`
	Name      string  `json:"name"`
	Content   string  `json:"content"`
	Remark    string  `json:"remark"`
	SortOrder int     `json:"sort_order"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// Input 命令新建/编辑入参。
type Input struct {
	GroupID   *string `json:"group_id"`
	Name      string  `json:"name"`
	Content   string  `json:"content"`
	Remark    string  `json:"remark"`
	SortOrder int     `json:"sort_order"`
}

// Store 命令数据访问层。
type Store struct {
	conn *sql.DB
}

// NewStore 构造命令 Store。
func NewStore(conn *sql.DB) *Store { return &Store{conn: conn} }

// Groups 返回分组及其命令。
func (s *Store) Groups() ([]*Group, error) {
	rows, err := s.conn.Query(`SELECT id, name, sort_order, created_at, updated_at FROM command_groups ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]*Group, 0, 8)
	index := map[string]*Group{}
	for rows.Next() {
		g := &Group{Commands: []*Command{}}
		if err := rows.Scan(&g.ID, &g.Name, &g.SortOrder, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
		index[g.ID] = g
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	cmds, err := s.List("")
	if err != nil {
		return nil, err
	}
	for _, c := range cmds {
		if c.GroupID == nil {
			continue
		}
		if g, ok := index[*c.GroupID]; ok {
			g.Commands = append(g.Commands, c)
		}
	}
	return groups, nil
}

// List 按关键词查询命令片段；term 为空返回全部。
func (s *Store) List(term string) ([]*Command, error) {
	query := `SELECT id, group_id, name, content, COALESCE(remark, ''), sort_order, created_at, updated_at FROM commands`
	var args []any
	if term = strings.TrimSpace(term); term != "" {
		like := "%" + term + "%"
		query += ` WHERE name LIKE ? OR content LIKE ? OR COALESCE(remark, '') LIKE ?`
		args = append(args, like, like, like)
	}
	query += ` ORDER BY sort_order, name`

	rows, err := s.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Command, 0, 16)
	for rows.Next() {
		c := &Command{}
		var groupID sql.NullString
		if err := rows.Scan(&c.ID, &groupID, &c.Name, &c.Content, &c.Remark, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.GroupID = db.NullStrPtr(groupID)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateGroup 新建分组。
func (s *Store) CreateGroup(name string, sortOrder int) (*Group, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 64 {
		return nil, apperr.ErrBadRequest.WithMessage("分组名称需为 1-64 个字符")
	}
	now := db.NowStr()
	g := &Group{ID: uuid.NewString(), Name: name, SortOrder: sortOrder, CreatedAt: now, UpdatedAt: now, Commands: []*Command{}}
	_, err := s.conn.Exec(
		`INSERT INTO command_groups (id, name, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		g.ID, g.Name, g.SortOrder, now, now)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// UpdateGroup 重命名/排序分组。
func (s *Store) UpdateGroup(id, name string, sortOrder int) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 64 {
		return apperr.ErrBadRequest.WithMessage("分组名称需为 1-64 个字符")
	}
	res, err := s.conn.Exec(`UPDATE command_groups SET name = ?, sort_order = ?, updated_at = ? WHERE id = ?`,
		name, sortOrder, db.NowStr(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.ErrNotFound.WithMessage("分组不存在")
	}
	return nil
}

// DeleteGroup 删除分组，并把组内命令移到未分组。
func (s *Store) DeleteGroup(id string) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE commands SET group_id = NULL, updated_at = ? WHERE group_id = ?`, db.NowStr(), id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM command_groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.ErrNotFound.WithMessage("分组不存在")
	}
	return tx.Commit()
}

// Create 新建命令。
func (s *Store) Create(in *Input) (*Command, error) {
	if err := s.validate(in); err != nil {
		return nil, err
	}
	now := db.NowStr()
	id := uuid.NewString()
	_, err := s.conn.Exec(
		`INSERT INTO commands (id, group_id, name, content, remark, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, nullable(in.GroupID), strings.TrimSpace(in.Name), in.Content, strings.TrimSpace(in.Remark), in.SortOrder, now, now)
	if err != nil {
		return nil, err
	}
	return s.Get(id)
}

// Get 按 ID 查询命令。
func (s *Store) Get(id string) (*Command, error) {
	c := &Command{}
	var groupID sql.NullString
	err := s.conn.QueryRow(
		`SELECT id, group_id, name, content, COALESCE(remark, ''), sort_order, created_at, updated_at
		 FROM commands WHERE id = ?`, id).
		Scan(&c.ID, &groupID, &c.Name, &c.Content, &c.Remark, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, apperr.ErrNotFound.WithMessage("命令不存在")
	}
	if err != nil {
		return nil, err
	}
	c.GroupID = db.NullStrPtr(groupID)
	return c, nil
}

// Update 编辑命令。
func (s *Store) Update(id string, in *Input) (*Command, error) {
	if err := s.validate(in); err != nil {
		return nil, err
	}
	res, err := s.conn.Exec(
		`UPDATE commands SET group_id = ?, name = ?, content = ?, remark = ?, sort_order = ?, updated_at = ? WHERE id = ?`,
		nullable(in.GroupID), strings.TrimSpace(in.Name), in.Content, strings.TrimSpace(in.Remark), in.SortOrder, db.NowStr(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, apperr.ErrNotFound.WithMessage("命令不存在")
	}
	return s.Get(id)
}

// Delete 删除命令。
func (s *Store) Delete(id string) error {
	res, err := s.conn.Exec(`DELETE FROM commands WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.ErrNotFound.WithMessage("命令不存在")
	}
	return nil
}

// Count 统计命令数量（备份概览用）。
func (s *Store) Count() (int, error) {
	var n int
	err := s.conn.QueryRow(`SELECT COUNT(*) FROM commands`).Scan(&n)
	return n, err
}

func (s *Store) validate(in *Input) error {
	if strings.TrimSpace(in.Name) == "" {
		return apperr.ErrBadRequest.WithMessage("命令名称不能为空")
	}
	if strings.TrimSpace(in.Content) == "" {
		return apperr.ErrBadRequest.WithMessage("命令内容不能为空")
	}
	if in.GroupID != nil && *in.GroupID != "" {
		var one int
		err := s.conn.QueryRow(`SELECT 1 FROM command_groups WHERE id = ?`, *in.GroupID).Scan(&one)
		if err == sql.ErrNoRows {
			return apperr.ErrBadRequest.WithMessage("命令分组不存在")
		}
		if err != nil {
			return err
		}
	} else {
		in.GroupID = nil
	}
	return nil
}

func nullable(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}
