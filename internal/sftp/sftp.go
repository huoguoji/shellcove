// Package sftp 提供 SFTP 目录浏览、文件读写与断点续传上传。
package sftp

import (
	"database/sql"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	pkgsftp "github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/db"
	"github.com/huoguoji/shellcove/internal/ssh"
	"github.com/huoguoji/shellcove/internal/sshinfo"
)

// SettingRoot 限制可操作目录的配置键（为空表示不限制，仍禁止路径遍历）。
const SettingRoot = "sftp_root"

// MaxInlineFileSize 在线编辑文件的大小上限（1MB）。
const MaxInlineFileSize = 1 << 20

// Entry 目录项。
type Entry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Mode      string `json:"mode"`
	IsDir     bool   `json:"is_dir"`
	IsSymlink bool   `json:"is_symlink"`
	ModTime   string `json:"mod_time"`
}

// Service SFTP 服务。
type Service struct {
	conn      *sql.DB
	pool      *Pool
	uploads   *uploadStore
	active    *activeRegistry
	chunkSize int64
	maxFileMB int64
}

// NewService 构造 SFTP 服务。
func NewService(conn *sql.DB, logger *audit.Logger, chunkSize, maxFileMB int64) *Service {
	if chunkSize <= 0 {
		chunkSize = 2 * 1024 * 1024
	}
	return &Service{
		conn:      conn,
		pool:      newPool(logger),
		uploads:   &uploadStore{conn: conn},
		active:    &activeRegistry{items: map[string]*activeUpload{}},
		chunkSize: chunkSize,
		maxFileMB: maxFileMB,
	}
}

func (s *Service) activeRegistry() *activeRegistry { return s.active }

// ChunkSize 返回约定的分片大小。
func (s *Service) ChunkSize() int64 { return s.chunkSize }

// MaxFileBytes 返回允许上传的单文件大小上限（0 表示不限制）。
func (s *Service) MaxFileBytes() int64 {
	if s.maxFileMB <= 0 {
		return 0
	}
	return s.maxFileMB * 1024 * 1024
}

// Pool 返回底层连接池，供后台清理使用。
func (s *Service) Pool() *Pool { return s.pool }

// Client 获取（或建立）指定 SSH 信息的 SFTP 客户端。
func (s *Service) Client(sshID string, creds *sshinfo.Credentials) (*pkgsftp.Client, error) {
	return s.pool.get(sshID, creds)
}

// CloseSSH 断开某台 SSH 信息的 SFTP 连接。
func (s *Service) CloseSSH(sshID string) { s.pool.Close(sshID) }

// Root 返回当前限制的可操作根目录。
func (s *Service) Root() string {
	return strings.TrimSpace(db.SettingGet(s.conn, SettingRoot, ""))
}

// SetRoot 设置可操作根目录。
func (s *Service) SetRoot(root string) error {
	root = strings.TrimSpace(root)
	if root != "" {
		cleaned, err := CleanPath(root)
		if err != nil {
			return err
		}
		root = cleaned
	}
	return db.SettingSet(s.conn, SettingRoot, root)
}

// CleanPath 归一化远端路径，拒绝路径遍历与非法字符。
func CleanPath(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", apperr.ErrBadRequest.WithMessage("路径包含非法字符")
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" {
		return "/", nil
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	cleaned := path.Clean(p)
	if cleaned == "." {
		return "/", nil
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == ".." {
			return "", apperr.ErrBadRequest.WithMessage("路径不合法")
		}
	}
	return cleaned, nil
}

// Resolve 校验路径是否位于允许的根目录内。
func (s *Service) Resolve(p string) (string, error) {
	cleaned, err := CleanPath(p)
	if err != nil {
		return "", err
	}
	root := s.Root()
	if root == "" {
		return cleaned, nil
	}
	rootCleaned, err := CleanPath(root)
	if err != nil {
		return "", err
	}
	if cleaned == rootCleaned || strings.HasPrefix(cleaned, strings.TrimSuffix(rootCleaned, "/")+"/") {
		return cleaned, nil
	}
	return "", apperr.ErrForbidden.WithMessage("路径超出允许操作的范围：" + rootCleaned)
}

// List 列出目录内容。
func (s *Service) List(sshID string, creds *sshinfo.Credentials, dir string) ([]*Entry, string, error) {
	full, err := s.Resolve(dir)
	if err != nil {
		return nil, "", err
	}
	client, err := s.Client(sshID, creds)
	if err != nil {
		return nil, "", err
	}

	infos, err := client.ReadDir(full)
	if err != nil {
		return nil, full, apperr.New("SFTP_LIST_FAILED", "读取目录失败："+err.Error(), 502)
	}

	entries := make([]*Entry, 0, len(infos))
	for _, fi := range infos {
		entryPath := path.Join(full, fi.Name())
		entries = append(entries, &Entry{
			Name:      fi.Name(),
			Path:      entryPath,
			Size:      fi.Size(),
			Mode:      fi.Mode().String(),
			IsDir:     fi.IsDir(),
			IsSymlink: fi.Mode()&os.ModeSymlink != 0,
			ModTime:   fi.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, full, nil
}

// Stat 查询单个路径。
func (s *Service) Stat(sshID string, creds *sshinfo.Credentials, target string) (*Entry, error) {
	full, err := s.Resolve(target)
	if err != nil {
		return nil, err
	}
	client, err := s.Client(sshID, creds)
	if err != nil {
		return nil, err
	}
	fi, err := client.Lstat(full)
	if err != nil {
		return nil, apperr.ErrNotFound.WithMessage("路径不存在：" + full)
	}
	return &Entry{
		Name:      fi.Name(),
		Path:      full,
		Size:      fi.Size(),
		Mode:      fi.Mode().String(),
		IsDir:     fi.IsDir(),
		IsSymlink: fi.Mode()&os.ModeSymlink != 0,
		ModTime:   fi.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

// HomeDir 读取远端用户家目录，用于前端初始定位。
func (s *Service) HomeDir(sshID string, creds *sshinfo.Credentials) string {
	client, err := s.Client(sshID, creds)
	if err != nil {
		return "/"
	}
	wd, err := client.Getwd()
	if err != nil || wd == "" {
		return "/"
	}
	return wd
}

// Mkdir 创建目录（支持多级）。
func (s *Service) Mkdir(sshID string, creds *sshinfo.Credentials, dir string) error {
	full, err := s.Resolve(dir)
	if err != nil {
		return err
	}
	client, err := s.Client(sshID, creds)
	if err != nil {
		return err
	}
	if err := client.MkdirAll(full); err != nil {
		return apperr.New("SFTP_MKDIR_FAILED", "创建目录失败："+err.Error(), 502)
	}
	return nil
}

// Remove 删除文件或目录（目录需 empty 为 true 才允许递归删除）。
func (s *Service) Remove(sshID string, creds *sshinfo.Credentials, target string, recursive bool) error {
	full, err := s.Resolve(target)
	if err != nil {
		return err
	}
	client, err := s.Client(sshID, creds)
	if err != nil {
		return err
	}
	fi, err := client.Lstat(full)
	if err != nil {
		return apperr.ErrNotFound.WithMessage("路径不存在：" + full)
	}
	if !fi.IsDir() {
		return wrapSFTPErr("SFTP_REMOVE_FAILED", "删除文件失败", client.Remove(full))
	}
	if recursive {
		return wrapSFTPErr("SFTP_REMOVE_FAILED", "删除目录失败", client.RemoveAll(full))
	}
	return wrapSFTPErr("SFTP_REMOVE_FAILED", "删除目录失败（需目录为空）", client.RemoveDirectory(full))
}

// Rename 重命名或移动。
func (s *Service) Rename(sshID string, creds *sshinfo.Credentials, from, to string) error {
	src, err := s.Resolve(from)
	if err != nil {
		return err
	}
	dst, err := s.Resolve(to)
	if err != nil {
		return err
	}
	client, err := s.Client(sshID, creds)
	if err != nil {
		return err
	}
	return wrapSFTPErr("SFTP_RENAME_FAILED", "重命名失败", client.Rename(src, dst))
}

// Chmod 修改权限。
func (s *Service) Chmod(sshID string, creds *sshinfo.Credentials, target string, mode os.FileMode) error {
	full, err := s.Resolve(target)
	if err != nil {
		return err
	}
	client, err := s.Client(sshID, creds)
	if err != nil {
		return err
	}
	return wrapSFTPErr("SFTP_CHMOD_FAILED", "修改权限失败", client.Chmod(full, mode))
}

// OpenRead 打开远端文件用于下载。
// Open 与 Stat 并行发出（pkg/sftp 的 Client 允许并发调用），省掉一次串行往返。
func (s *Service) OpenRead(sshID string, creds *sshinfo.Credentials, target string) (io.ReadCloser, *Entry, error) {
	full, err := s.Resolve(target)
	if err != nil {
		return nil, nil, err
	}
	client, err := s.Client(sshID, creds)
	if err != nil {
		return nil, nil, err
	}

	type statResult struct {
		fi  os.FileInfo
		err error
	}
	// 带缓冲的通道，即使 Open 先失败也不会阻塞这个协程。
	statCh := make(chan statResult, 1)
	go func() {
		fi, err := client.Stat(full)
		statCh <- statResult{fi: fi, err: err}
	}()

	f, err := client.Open(full)
	if err != nil {
		return nil, nil, apperr.New("SFTP_OPEN_FAILED", "打开文件失败："+err.Error(), 502)
	}
	res := <-statCh
	if res.err != nil {
		f.Close()
		return nil, nil, apperr.New("SFTP_STAT_FAILED", "读取文件信息失败："+res.err.Error(), 502)
	}
	fi := res.fi
	if fi.IsDir() {
		f.Close()
		return nil, nil, apperr.ErrBadRequest.WithMessage("目标是一个目录，无法下载")
	}
	return f, &Entry{
		Name:    fi.Name(),
		Path:    full,
		Size:    fi.Size(),
		Mode:    fi.Mode().String(),
		ModTime: fi.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

// ReadFile 读取小文件内容用于在线查看/编辑。
func (s *Service) ReadFile(sshID string, creds *sshinfo.Credentials, target string) (string, error) {
	rc, entry, err := s.OpenRead(sshID, creds, target)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	if entry.Size > MaxInlineFileSize {
		return "", apperr.ErrBadRequest.WithMessage("文件过大，请直接下载查看")
	}
	data, err := io.ReadAll(io.LimitReader(rc, MaxInlineFileSize+1))
	if err != nil {
		return "", apperr.New("SFTP_READ_FAILED", "读取文件失败："+err.Error(), 502)
	}
	if len(data) > MaxInlineFileSize {
		return "", apperr.ErrBadRequest.WithMessage("文件过大，请直接下载查看")
	}
	return string(data), nil
}

// WriteFile 覆盖写入文本文件。
func (s *Service) WriteFile(sshID string, creds *sshinfo.Credentials, target string, content []byte) error {
	full, err := s.Resolve(target)
	if err != nil {
		return err
	}
	client, err := s.Client(sshID, creds)
	if err != nil {
		return err
	}
	f, err := client.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return apperr.New("SFTP_WRITE_FAILED", "打开文件失败："+err.Error(), 502)
	}
	defer f.Close()

	if _, err := f.Write(content); err != nil {
		return apperr.New("SFTP_WRITE_FAILED", "写入文件失败："+err.Error(), 502)
	}
	return nil
}

// SaveStream 将数据流完整写入远端文件（用于小文件整体上传）。
func (s *Service) SaveStream(sshID string, creds *sshinfo.Credentials, target string, r io.Reader, size int64) error {
	if max := s.MaxFileBytes(); max > 0 && size > max {
		return apperr.ErrPayloadTooLarge.WithMessage("文件超过允许的大小上限")
	}
	full, err := s.Resolve(target)
	if err != nil {
		return err
	}
	client, err := s.Client(sshID, creds)
	if err != nil {
		return err
	}
	f, err := client.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return apperr.New("SFTP_WRITE_FAILED", "打开文件失败："+err.Error(), 502)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return apperr.New("SFTP_WRITE_FAILED", "写入文件失败："+err.Error(), 502)
	}
	return nil
}

// CloseAll 关闭所有 SFTP 连接。
func (s *Service) CloseAll() { s.pool.CloseAll() }

func wrapSFTPErr(code, msg string, err error) error {
	if err == nil {
		return nil
	}
	return apperr.New(code, msg+"："+err.Error(), 502)
}

// ErrNotFound 便于上层判断的哨兵错误。
var ErrNotFound = errors.New("not found")

// ---- 连接池 ----

// idleProbeAfter 连接空闲超过该时长后，复用前先做一次可用性探测。
// 连续操作（浏览目录、下载、上传分片）期间不会产生额外的往返开销。
const idleProbeAfter = time.Minute

type poolEntry struct {
	client   *pkgsftp.Client
	ssh      *gossh.Client
	lastUsed time.Time
}

// Pool 按 SSH 信息复用 SFTP 连接，减少频繁建连开销。
type Pool struct {
	mu    sync.Mutex
	items map[string]*poolEntry
	ttl   time.Duration
	audit *audit.Logger
}

func newPool(logger *audit.Logger) *Pool {
	return &Pool{items: map[string]*poolEntry{}, ttl: 5 * time.Minute, audit: logger}
}

func (p *Pool) get(sshID string, creds *sshinfo.Credentials) (*pkgsftp.Client, error) {
	p.mu.Lock()
	if entry, ok := p.items[sshID]; ok {
		// 仅在连接空闲较久时才探测可用性：避免每次请求都多花一次往返，
		// 同时仍能在连接被对端或 NAT 断开后自动重建。
		probe := time.Since(entry.lastUsed) > idleProbeAfter
		entry.lastUsed = time.Now()
		client := entry.client
		p.mu.Unlock()
		if !probe {
			return client, nil
		}
		if _, err := client.Getwd(); err == nil {
			return client, nil
		}
		p.Close(sshID)
	} else {
		p.mu.Unlock()
	}

	sshClient, err := ssh.Dial(creds)
	if err != nil {
		return nil, err
	}
	client, err := pkgsftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, apperr.New("SFTP_INIT_FAILED", "初始化 SFTP 失败："+err.Error(), 502)
	}

	p.mu.Lock()
	// 并发场景下若已有其他协程建好连接，则复用并释放本次连接。
	if existing, ok := p.items[sshID]; ok {
		existing.lastUsed = time.Now()
		reuse := existing.client
		p.mu.Unlock()
		client.Close()
		sshClient.Close()
		return reuse, nil
	}
	p.items[sshID] = &poolEntry{client: client, ssh: sshClient, lastUsed: time.Now()}
	p.mu.Unlock()
	return client, nil
}

// Close 关闭某台 SSH 信息的连接。
func (p *Pool) Close(sshID string) {
	p.mu.Lock()
	entry, ok := p.items[sshID]
	if ok {
		delete(p.items, sshID)
	}
	p.mu.Unlock()

	if ok {
		entry.client.Close()
		entry.ssh.Close()
	}
}

// CloseAll 关闭全部连接。
func (p *Pool) CloseAll() {
	p.mu.Lock()
	items := p.items
	p.items = map[string]*poolEntry{}
	p.mu.Unlock()

	for _, entry := range items {
		entry.client.Close()
		entry.ssh.Close()
	}
}

// Reap 关闭空闲超时的连接。
func (p *Pool) Reap() {
	now := time.Now()
	var stale []string

	p.mu.Lock()
	for id, entry := range p.items {
		if now.Sub(entry.lastUsed) > p.ttl {
			stale = append(stale, id)
		}
	}
	p.mu.Unlock()

	for _, id := range stale {
		p.Close(id)
	}
}
