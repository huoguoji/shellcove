// Command shellcove 启动 ShellCove 轻量运维工具。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/huoguoji/shellcove/internal/api"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/auth"
	"github.com/huoguoji/shellcove/internal/backup"
	"github.com/huoguoji/shellcove/internal/command"
	"github.com/huoguoji/shellcove/internal/config"
	"github.com/huoguoji/shellcove/internal/crypto"
	"github.com/huoguoji/shellcove/internal/db"
	"github.com/huoguoji/shellcove/internal/folder"
	"github.com/huoguoji/shellcove/internal/permission"
	"github.com/huoguoji/shellcove/internal/sftp"
	"github.com/huoguoji/shellcove/internal/ssh"
	"github.com/huoguoji/shellcove/internal/sshinfo"
	"github.com/huoguoji/shellcove/internal/trash"
	"github.com/huoguoji/shellcove/internal/user"
	"github.com/huoguoji/shellcove/web"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cipher, err := crypto.New(cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("初始化加密器失败: %w", err)
	}
	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}

	// ------------------------------------------------------------ 依赖装配
	users := user.NewStore(conn)
	if pwd, created, err := users.EnsureInitialAdmin(os.Getenv("ADMIN_PASSWORD")); err != nil {
		return fmt.Errorf("初始化管理员失败: %w", err)
	} else if created {
		log.Printf("已创建初始管理员 admin，初始密码：%s（首次登录必须修改）", pwd)
	}

	logger := audit.NewLogger(conn, cfg.RecordingsDir, cfg.RecordingMaxMB)
	authSvc := auth.New(conn, cipher, cfg, users)
	perms := permission.New(conn)
	folders := folder.NewStore(conn)
	infos := sshinfo.NewStore(conn, cipher)
	trashSvc := trash.New(conn, infos, cfg.TrashRetentionDays)
	commands := command.NewStore(conn)
	manager := ssh.NewManager(logger, cfg.SessionIdleTimeout)
	// 复用型连接池，供监控采集等低频短任务使用。
	sshPool := ssh.NewPool(ssh.DefaultPoolTTL)
	sftpSvc := sftp.NewService(conn, logger, cfg.UploadChunkSize, cfg.UploadMaxFileMB)
	backupSvc := backup.NewService(conn, cipher, cfg)
	// 导入备份时若主密钥不一致，用原主密钥重加密凭证。
	backupSvc.SetReEncryptor(infos.ReEncryptAll)

	static, err := web.Dist()
	if err != nil {
		return fmt.Errorf("加载前端资源失败: %w", err)
	}

	srv := api.New(api.Deps{
		Cfg:      cfg,
		Conn:     conn,
		Cipher:   cipher,
		Auth:     authSvc,
		Users:    users,
		Perms:    perms,
		Folders:  folders,
		Infos:    infos,
		Trash:    trashSvc,
		Commands: commands,
		Manager:  manager,
		SSHPool:  sshPool,
		SFTP:     sftpSvc,
		Audit:    logger,
		Backup:   backupSvc,
		Static:   static,
	})

	// ------------------------------------------------------------ 后台任务
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := logger.MarkStaleSessions(); err != nil {
		log.Printf("清理残留会话记录失败: %v", err)
	}
	manager.Start(ctx)
	sshPool.Start(ctx)
	trashSvc.Start(ctx, cfg.TrashCleanupInterval)
	go maintenanceLoop(ctx, conn, sftpSvc)

	// ------------------------------------------------------------ HTTP 服务
	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 20 * time.Second,
		// 终端会话与断点续传均为长连接，因此不设置读写超时。
		IdleTimeout: 120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("ShellCove %s 已启动，监听 %s（数据目录 %s）", config.AppVersion, httpSrv.Addr, cfg.DataDir)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Println("收到退出信号，正在关闭服务…")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)

	// 断开全部终端会话并释放 SFTP / 复用连接池。
	manager.CloseAll("服务正在关闭")
	sftpSvc.CloseAll()
	sshPool.CloseAll()
	sftpSvc.CloseUploads()
	<-manager.Done()
	log.Println("已安全退出")
	return nil
}

// maintenanceLoop 周期性执行清理：令牌黑名单、超期回收站、中断的上传任务。
func maintenanceLoop(ctx context.Context, conn *sql.DB, sftpSvc *sftp.Service) {
	cleanup := func() {
		if err := db.CleanupBlacklist(conn); err != nil {
			log.Printf("清理令牌黑名单失败: %v", err)
		}
		if n, err := sftpSvc.CleanupStale(24); err != nil {
			log.Printf("清理中断上传任务失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理 %d 个中断的上传任务", n)
		}
	}
	cleanup()

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}
