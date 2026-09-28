// Package trash 封装回收站操作与定时清理。
package trash

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/huoguoji/shellcove/internal/db"
	"github.com/huoguoji/shellcove/internal/sshinfo"
)

// SettingRetentionDays 回收站保留天数的配置键。
const SettingRetentionDays = "trash_retention_days"

// Service 回收站服务。
type Service struct {
	conn  *sql.DB
	infos *sshinfo.Store
	def   int
}

// New 构造回收站服务，defaultRetention 为配置默认保留天数。
func New(conn *sql.DB, infos *sshinfo.Store, defaultRetention int) *Service {
	return &Service{conn: conn, infos: infos, def: defaultRetention}
}

// RetentionDays 读取当前保留天数（设置项优先于环境变量默认值）。
func (s *Service) RetentionDays() int {
	return db.SettingGetInt(s.conn, SettingRetentionDays, s.def)
}

// SetRetentionDays 更新保留天数。
func (s *Service) SetRetentionDays(days int) error {
	return db.SettingSet(s.conn, SettingRetentionDays, itoa(days))
}

// List 列出回收站中的记录。
func (s *Service) List() ([]*sshinfo.Info, error) {
	return s.infos.List(sshinfo.ListFilter{OnlyTrashed: true})
}

// Restore 恢复记录。
func (s *Service) Restore(id string) error { return s.infos.Restore(id) }

// Purge 彻底删除记录。
func (s *Service) Purge(id string) error { return s.infos.Purge(id) }

// Empty 清空回收站，返回清理条数。
func (s *Service) Empty() (int, error) { return s.infos.EmptyTrash() }

// Count 返回回收站记录数。
func (s *Service) Count() (int, error) { return s.infos.CountTrashed() }

// Cleanup 按保留天数清理过期记录。
func (s *Service) Cleanup() (int, error) {
	return s.infos.CleanupExpired(s.RetentionDays())
}

// Start 启动定时清理协程，ctx 取消后退出。
func (s *Service) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		// 启动后先跑一次，避免长期未重启导致过期数据堆积。
		if n, err := s.Cleanup(); err != nil {
			log.Printf("[trash] 回收站清理失败: %v", err)
		} else if n > 0 {
			log.Printf("[trash] 已清理 %d 条过期回收站记录", n)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := s.Cleanup(); err != nil {
					log.Printf("[trash] 回收站清理失败: %v", err)
				} else if n > 0 {
					log.Printf("[trash] 已清理 %d 条过期回收站记录", n)
				}
			}
		}
	}()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
