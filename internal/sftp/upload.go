package sftp

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	pkgsftp "github.com/pkg/sftp"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/db"
	"github.com/huoguoji/shellcove/internal/sshinfo"
)

// 上传状态。
const (
	UploadStatusUploading = "uploading"
	UploadStatusDone      = "done"
	UploadStatusAborted   = "aborted"
)

// UploadInfo 断点续传任务状态（返回给前端）。
type UploadInfo struct {
	ID           string `json:"id"`
	UserID       string `json:"user_id"`
	SSHID        string `json:"ssh_id"`
	RemotePath   string `json:"remote_path"`
	Filename     string `json:"filename"`
	TotalSize    int64  `json:"total_size"`
	ReceivedSize int64  `json:"received_size"`
	ChunkSize    int64  `json:"chunk_size"`
	FileHash     string `json:"file_hash"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type uploadStore struct {
	conn *sql.DB
}

func (u *uploadStore) create(info *UploadInfo) error {
	now := db.NowStr()
	info.CreatedAt, info.UpdatedAt = now, now
	_, err := u.conn.Exec(
		`INSERT INTO upload_progress (id, user_id, ssh_id, remote_path, filename, total_size, received_size,
			chunk_size, file_hash, temp_path, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?)`,
		info.ID, info.UserID, info.SSHID, info.RemotePath, info.Filename, info.TotalSize,
		info.ReceivedSize, info.ChunkSize, info.FileHash, info.Status, now, now)
	return err
}

func (u *uploadStore) get(id string) (*UploadInfo, error) {
	row := u.conn.QueryRow(
		`SELECT id, user_id, ssh_id, remote_path, filename, total_size, received_size, chunk_size,
		        COALESCE(file_hash, ''), status, created_at, updated_at
		 FROM upload_progress WHERE id = ?`, id)
	info := &UploadInfo{}
	err := row.Scan(&info.ID, &info.UserID, &info.SSHID, &info.RemotePath, &info.Filename,
		&info.TotalSize, &info.ReceivedSize, &info.ChunkSize, &info.FileHash, &info.Status,
		&info.CreatedAt, &info.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, apperr.ErrNotFound.WithMessage("上传任务不存在或已过期")
	}
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (u *uploadStore) updateProgress(id string, received int64) error {
	_, err := u.conn.Exec(`UPDATE upload_progress SET received_size = ?, updated_at = ? WHERE id = ?`,
		received, db.NowStr(), id)
	return err
}

func (u *uploadStore) list(userID, sshID string) ([]*UploadInfo, error) {
	rows, err := u.conn.Query(
		`SELECT id, user_id, ssh_id, remote_path, filename, total_size, received_size, chunk_size,
		        COALESCE(file_hash, ''), status, created_at, updated_at
		 FROM upload_progress WHERE user_id = ? AND ssh_id = ? AND status = ?
		 ORDER BY updated_at DESC`, userID, sshID, UploadStatusUploading)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*UploadInfo, 0, 4)
	for rows.Next() {
		info := &UploadInfo{}
		if err := rows.Scan(&info.ID, &info.UserID, &info.SSHID, &info.RemotePath, &info.Filename,
			&info.TotalSize, &info.ReceivedSize, &info.ChunkSize, &info.FileHash, &info.Status,
			&info.CreatedAt, &info.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

func (u *uploadStore) remove(id string) error {
	_, err := u.conn.Exec(`DELETE FROM upload_progress WHERE id = ?`, id)
	return err
}

// CleanupStale 清理长时间未更新的上传任务记录。
func (s *Service) CleanupStale(hours int) (int, error) {
	if hours <= 0 {
		return 0, nil
	}
	threshold := db.TimeStr(nowAddHours(-hours))
	res, err := s.uploads.conn.Exec(
		`DELETE FROM upload_progress WHERE status = ? AND updated_at < ?`, UploadStatusUploading, threshold)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ---- 活跃上传句柄 ----

type activeUpload struct {
	mu       sync.Mutex
	file     *pkgsftp.File
	hasher   hash.Hash
	chained  bool // 全程连续写入时才能用增量哈希校验
	received int64
	closed   bool
}

type activeRegistry struct {
	mu    sync.Mutex
	items map[string]*activeUpload
}

func (r *activeRegistry) get(id string) (*activeUpload, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	return item, ok
}

func (r *activeRegistry) put(id string, item *activeUpload) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[id] = item
}

func (r *activeRegistry) drop(id string) {
	r.mu.Lock()
	item, ok := r.items[id]
	delete(r.items, id)
	r.mu.Unlock()
	if ok {
		item.mu.Lock()
		if !item.closed {
			item.closed = true
			_ = item.file.Close()
		}
		item.mu.Unlock()
	}
}

// InitUpload 创建（或复用）一个断点续传任务。
func (s *Service) InitUpload(userID, sshID string, creds *sshinfo.Credentials, remoteDir, filename string, size int64, fileHash string) (*UploadInfo, error) {
	if max := s.MaxFileBytes(); max > 0 && size > max {
		return nil, apperr.ErrPayloadTooLarge.WithMessage("文件超过允许的大小上限")
	}
	filename = strings.TrimSpace(filename)
	if filename == "" || strings.ContainsAny(filename, "/\\") {
		return nil, apperr.ErrBadRequest.WithMessage("文件名不合法")
	}
	dir, err := s.Resolve(remoteDir)
	if err != nil {
		return nil, err
	}

	target := path.Join(dir, filename)
	// 同名未完成任务直接复用，实现真正的断点续传。
	existing, err := s.uploads.list(userID, sshID)
	if err != nil {
		return nil, err
	}
	for _, item := range existing {
		if item.RemotePath == target && item.TotalSize == size {
			return item, nil
		}
	}

	info := &UploadInfo{
		ID:         uuid.NewString(),
		UserID:     userID,
		SSHID:      sshID,
		RemotePath: target,
		Filename:   filename,
		TotalSize:  size,
		ChunkSize:  s.chunkSize,
		FileHash:   strings.ToLower(strings.TrimSpace(fileHash)),
		Status:     UploadStatusUploading,
	}
	if err := s.uploads.create(info); err != nil {
		return nil, err
	}
	return info, nil
}

// UploadStatus 查询上传进度。
func (s *Service) UploadStatus(userID, sshID, uploadID string) (*UploadInfo, error) {
	info, err := s.uploads.get(uploadID)
	if err != nil {
		return nil, err
	}
	if info.UserID != userID || info.SSHID != sshID {
		return nil, apperr.ErrNotFound.WithMessage("上传任务不存在")
	}
	return info, nil
}

// ListUploads 列出可续传的任务。
func (s *Service) ListUploads(userID, sshID string) ([]*UploadInfo, error) {
	return s.uploads.list(userID, sshID)
}

// WriteChunk 写入一个分片。offset 必须等于服务端已接收的长度，保证顺序写入与增量哈希有效。
func (s *Service) WriteChunk(userID, sshID string, creds *sshinfo.Credentials, uploadID string, offset int64, data []byte) (*UploadInfo, error) {
	info, err := s.UploadStatus(userID, sshID, uploadID)
	if err != nil {
		return nil, err
	}
	if info.Status != UploadStatusUploading {
		return nil, apperr.ErrConflict.WithMessage("上传任务已结束")
	}
	if len(data) == 0 {
		return nil, apperr.ErrBadRequest.WithMessage("分片内容为空")
	}
	if int64(len(data)) > s.chunkSize {
		return nil, apperr.ErrBadRequest.WithMessage("分片大小超出上限")
	}
	if offset < 0 || (info.TotalSize > 0 && offset+int64(len(data)) > info.TotalSize) {
		return nil, apperr.ErrBadRequest.WithMessage("分片偏移超出文件范围")
	}
	if offset > info.ReceivedSize {
		return nil, apperr.ErrConflict.WithMessage("分片乱序，请从服务端已接收的位置继续上传")
	}

	client, err := s.Client(sshID, creds)
	if err != nil {
		return nil, err
	}

	active, ok := s.activeRegistry().get(uploadID)
	if !ok {
		flags := os.O_WRONLY | os.O_CREATE
		if offset == 0 {
			flags |= os.O_TRUNC
		}
		file, err := client.OpenFile(info.RemotePath, flags)
		if err != nil {
			return nil, apperr.New("SFTP_UPLOAD_FAILED", "打开远端文件失败："+err.Error(), 502)
		}
		active = &activeUpload{
			file:     file,
			hasher:   sha256.New(),
			chained:  offset == 0,
			received: info.ReceivedSize,
		}
		s.activeRegistry().put(uploadID, active)
	}

	active.mu.Lock()
	defer active.mu.Unlock()
	if active.closed {
		s.activeRegistry().drop(uploadID)
		return nil, apperr.ErrConflict.WithMessage("上传句柄已失效，请重新发起")
	}

	n, err := active.file.WriteAt(data, offset)
	if err != nil {
		s.activeRegistry().drop(uploadID)
		return nil, apperr.New("SFTP_UPLOAD_FAILED", "写入远端文件失败："+err.Error(), 502)
	}
	if active.chained {
		// 重传的分片会破坏哈希连续性，此时降级为长度校验。
		if offset != active.received {
			active.chained = false
		} else if _, err := active.hasher.Write(data[:n]); err != nil {
			active.chained = false
		}
	}

	if end := offset + int64(n); end > active.received {
		active.received = end
	}

	if err := s.uploads.updateProgress(uploadID, active.received); err != nil {
		return nil, err
	}
	info.ReceivedSize = active.received
	return info, nil
}

// CompleteUpload 完成上传：校验大小与哈希，并释放句柄。
func (s *Service) CompleteUpload(userID, sshID string, creds *sshinfo.Credentials, uploadID string) (*UploadInfo, error) {
	info, err := s.UploadStatus(userID, sshID, uploadID)
	if err != nil {
		return nil, err
	}
	if info.TotalSize > 0 && info.ReceivedSize != info.TotalSize {
		return nil, apperr.ErrConflict.WithMessage("文件尚未上传完整")
	}

	client, err := s.Client(sshID, creds)
	if err != nil {
		return nil, err
	}

	var computed string
	if active, ok := s.activeRegistry().get(uploadID); ok {
		active.mu.Lock()
		if active.chained && active.hasher != nil {
			computed = hex.EncodeToString(active.hasher.Sum(nil))
		}
		if !active.closed {
			active.closed = true
			_ = active.file.Close()
		}
		active.mu.Unlock()
		s.activeRegistry().drop(uploadID)
	}

	// 远端实际大小校验，防止中断导致的残缺文件。
	fi, err := client.Stat(info.RemotePath)
	if err != nil {
		return nil, apperr.New("SFTP_UPLOAD_FAILED", "校验远端文件失败："+err.Error(), 502)
	}
	if info.TotalSize > 0 && fi.Size() != info.TotalSize {
		return nil, apperr.ErrConflict.WithMessage("远端文件大小与预期不一致，请重新上传")
	}
	if info.FileHash != "" && computed != "" && !strings.EqualFold(computed, info.FileHash) {
		return nil, apperr.ErrChecksumMismatch.WithMessage("文件校验失败，请重新上传")
	}

	info.Status = UploadStatusDone
	if err := s.uploads.remove(uploadID); err != nil {
		return nil, err
	}
	return info, nil
}

// AbortUpload 取消上传并删除任务记录。
func (s *Service) AbortUpload(userID, sshID, uploadID string) error {
	if _, err := s.UploadStatus(userID, sshID, uploadID); err != nil {
		return err
	}
	s.activeRegistry().drop(uploadID)
	return s.uploads.remove(uploadID)
}

// CloseUploads 关闭全部活跃上传句柄（服务停止时调用）。
func (s *Service) CloseUploads() {
	r := s.activeRegistry()
	r.mu.Lock()
	ids := make([]string, 0, len(r.items))
	for id := range r.items {
		ids = append(ids, id)
	}
	r.mu.Unlock()

	for _, id := range ids {
		r.drop(id)
	}
}

// hashReader 计算读取内容的 sha256（用于小文件整体上传校验）。
func hashReader(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func nowAddHours(hours int) time.Time {
	return time.Now().UTC().Add(time.Duration(hours) * time.Hour)
}
