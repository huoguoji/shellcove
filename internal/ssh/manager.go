package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	gossh "golang.org/x/crypto/ssh"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/sshinfo"
)

// defaultBufferSize 服务端保留的最近输出字节数，用于前端断线重连后回放。
const defaultBufferSize = 256 * 1024

// OutputSink 终端输出接收方，由 WebSocket 层实现。
type OutputSink interface {
	// SendData 发送终端输出数据。
	SendData(data []byte) error
	// SendControl 发送控制消息（ready / exit / error 等）。
	SendControl(msg map[string]any) error
}

// CreateOptions 创建终端会话的参数。
type CreateOptions struct {
	UserID   string
	Username string
	SSHID    string
	SSHName  string
	Title    string
	ClientIP string
	Creds    *sshinfo.Credentials
	Cols     int
	Rows     int
}

// Session 一个可断线重连的终端会话。
type Session struct {
	ID       string
	UserID   string
	Username string
	SSHID    string
	SSHName  string
	Title    string
	ClientIP string

	// Monitored 记录该 SSH 信息是否勾选了读取监控。
	Monitored bool

	owner *Manager

	client *gossh.Client
	shell  *gossh.Session
	stdinW *io.PipeWriter

	mu         sync.Mutex
	cols       int
	rows       int
	sinks      map[OutputSink]struct{}
	buf        *ringBuffer
	recorder   *audit.Recorder
	lineBuf    []byte
	bytesIn    int64
	bytesOut   int64
	startedAt  time.Time
	lastActive time.Time
	closed     bool
	reason     string
}

// Info 会话摘要，供 API 返回。
type Info struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	Username   string `json:"username"`
	SSHID      string `json:"ssh_id"`
	SSHName    string `json:"ssh_name"`
	Title      string `json:"title"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
	StartedAt  string `json:"started_at"`
	LastActive string `json:"last_active"`
	BytesIn    int64  `json:"bytes_in"`
	BytesOut   int64  `json:"bytes_out"`
}

// Manager 会话管理器。
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session

	idleTTL time.Duration
	audit   *audit.Logger
	bufSize int
	stopped chan struct{}
}

// NewManager 构造会话管理器，idleTTL 为前端断开后保留连接的时长。
func NewManager(logger *audit.Logger, idleTTL time.Duration) *Manager {
	if idleTTL <= 0 {
		idleTTL = 30 * time.Minute
	}
	return &Manager{
		sessions: map[string]*Session{},
		idleTTL:  idleTTL,
		audit:    logger,
		bufSize:  defaultBufferSize,
		stopped:  make(chan struct{}),
	}
}

// Create 建立 SSH 连接并启动 shell。
func (m *Manager) Create(opts CreateOptions) (*Session, error) {
	client, err := Dial(opts.Creds)
	if err != nil {
		return nil, err
	}

	shell, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, apperr.New("SSH_SESSION_FAILED", "创建 SSH 会话失败："+err.Error(), 502)
	}

	cols, rows := opts.Cols, opts.Rows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 30
	}

	if err := shell.RequestPty("xterm-256color", rows, cols, gossh.TerminalModes{
		gossh.ECHO:          1,
		gossh.TTY_OP_ISPEED: 14400,
		gossh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		shell.Close()
		client.Close()
		return nil, apperr.New("SSH_PTY_FAILED", "申请 PTY 失败："+err.Error(), 502)
	}

	stdinR, stdinW := io.Pipe()
	shell.Stdin = stdinR

	s := &Session{
		ID:         uuid.NewString(),
		UserID:     opts.UserID,
		Username:   opts.Username,
		SSHID:      opts.SSHID,
		SSHName:    opts.SSHName,
		Title:      opts.Title,
		ClientIP:   opts.ClientIP,
		Monitored:  opts.Creds != nil && opts.Creds.MonitorEnabled,
		owner:      m,
		client:     client,
		shell:      shell,
		stdinW:     stdinW,
		cols:       cols,
		rows:       rows,
		sinks:      map[OutputSink]struct{}{},
		buf:        newRingBuffer(m.bufSize),
		startedAt:  time.Now(),
		lastActive: time.Now(),
	}
	// 让输出直接回流到会话，避免额外拷贝与管道阻塞。
	writer := &sessionWriter{session: s}
	shell.Stdout = writer
	shell.Stderr = writer

	if rec, err := m.audit.NewRecorder(s.ID, cols, rows); err != nil {
		log.Printf("[ssh] 创建录像文件失败: %v", err)
	} else {
		s.recorder = rec
	}

	if err := m.audit.CreateSession(&audit.Session{
		ID:            s.ID,
		UserID:        s.UserID,
		Username:      s.Username,
		SSHID:         s.SSHID,
		SSHName:       s.SSHName,
		Title:         s.Title,
		ClientIP:      s.ClientIP,
		RecordingPath: s.recorder.Path(),
	}); err != nil {
		s.release()
		return nil, err
	}

	// 先注册再启动 shell，避免 shell 瞬间退出导致清理逻辑找不到会话。
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()

	if err := shell.Shell(); err != nil {
		s.close("启动 shell 失败: " + err.Error())
		return nil, apperr.New("SSH_SHELL_FAILED", "启动 shell 失败："+err.Error(), 502)
	}

	go s.waitLoop()
	go s.keepAliveLoop()

	return s, nil
}

// sessionWriter 将远端输出写入会话缓冲区与订阅者。
type sessionWriter struct {
	session *Session
}

func (w *sessionWriter) Write(p []byte) (int, error) {
	if w.session != nil {
		w.session.handleOutput(p)
	}
	return len(p), nil
}

func (s *Session) waitLoop() {
	err := s.shell.Wait()
	code := 0
	reason := "连接已关闭"
	if err != nil {
		var exitErr *gossh.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitStatus()
			reason = fmt.Sprintf("远端退出，状态码 %d", code)
		} else {
			reason = err.Error()
		}
	}
	s.broadcastControl(map[string]any{"type": "exit", "code": code, "reason": reason})
	s.close(reason)
}

// keepAliveLoop 定期发送 keepalive，并在空闲超时后回收会话。
func (s *Session) keepAliveLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		closed := s.closed
		idle := time.Since(s.lastActive)
		hasSink := len(s.sinks) > 0
		s.mu.Unlock()
		if closed {
			return
		}

		// 前端断开后仍保活一段时间，便于断线重连。
		if !hasSink && idle > s.owner.idleTTL {
			s.broadcastControl(map[string]any{"type": "closed", "reason": "会话空闲超时已回收"})
			s.close("空闲超时")
			return
		}
		if err := KeepAlive(s.client); err != nil {
			s.broadcastControl(map[string]any{"type": "error", "reason": "SSH 连接已断开: " + err.Error()})
			s.close("连接已断开")
			return
		}
		s.owner.audit.TouchSession(s.ID)
	}
}

func (s *Session) handleOutput(data []byte) {
	if len(data) == 0 {
		return
	}
	s.mu.Lock()
	s.bytesOut += int64(len(data))
	s.lastActive = time.Now()
	s.buf.Write(data)
	recorder := s.recorder
	sinks := make([]OutputSink, 0, len(s.sinks))
	for sink := range s.sinks {
		sinks = append(sinks, sink)
	}
	s.mu.Unlock()

	if recorder != nil {
		recorder.Write("o", data)
	}
	for _, sink := range sinks {
		if err := sink.SendData(data); err != nil {
			s.Detach(sink)
		}
	}
}

func (s *Session) broadcastControl(msg map[string]any) {
	s.mu.Lock()
	sinks := make([]OutputSink, 0, len(s.sinks))
	for sink := range s.sinks {
		sinks = append(sinks, sink)
	}
	s.mu.Unlock()

	for _, sink := range sinks {
		_ = sink.SendControl(msg)
	}
}

// Attach 绑定一个输出接收方，并回放缓冲区内容以支持断线重连。
func (s *Session) Attach(sink OutputSink) error {
	s.mu.Lock()
	if s.closed {
		reason := s.reason
		s.mu.Unlock()
		return apperr.ErrNotFound.WithMessage("会话已结束：" + reason)
	}
	s.sinks[sink] = struct{}{}
	backlog := s.buf.Bytes()
	cols, rows := s.cols, s.rows
	s.lastActive = time.Now()
	s.mu.Unlock()

	if err := sink.SendControl(map[string]any{
		"type":   "ready",
		"cols":   cols,
		"rows":   rows,
		"replay": len(backlog) > 0,
	}); err != nil {
		return err
	}
	if len(backlog) > 0 {
		if err := sink.SendData(backlog); err != nil {
			return err
		}
	}
	return nil
}

// Detach 解除绑定。
func (s *Session) Detach(sink OutputSink) {
	s.mu.Lock()
	delete(s.sinks, sink)
	s.mu.Unlock()
}

// WriteInput 将用户输入发送到远端。
func (s *Session) WriteInput(data []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return apperr.ErrNotFound.WithMessage("会话已结束")
	}
	s.bytesIn += int64(len(data))
	s.lastActive = time.Now()
	recorder := s.recorder
	s.mu.Unlock()

	if recorder != nil {
		recorder.Write("i", data)
	}
	s.captureCommands(data)

	_, err := s.stdinW.Write(data)
	return err
}

// captureCommands 从终端输入中尽力提取用户执行的命令并记录。
func (s *Session) captureCommands(data []byte) {
	s.mu.Lock()
	var commands []string
	for _, b := range data {
		switch b {
		case '\r', '\n':
			if line := strings.TrimSpace(string(s.lineBuf)); line != "" {
				commands = append(commands, line)
			}
			s.lineBuf = s.lineBuf[:0]
		case 0x7f, 0x08: // 退格
			if len(s.lineBuf) > 0 {
				s.lineBuf = s.lineBuf[:len(s.lineBuf)-1]
			}
		case 0x03, 0x15: // Ctrl+C / Ctrl+U
			s.lineBuf = s.lineBuf[:0]
		default:
			if b >= 0x20 && len(s.lineBuf) < 4096 {
				s.lineBuf = append(s.lineBuf, b)
			}
		}
	}
	userID, sessionID := s.UserID, s.ID
	s.mu.Unlock()

	for _, cmd := range commands {
		s.owner.audit.AddCommand(sessionID, userID, cmd)
	}
}

// Resize 同步终端尺寸到远端。
func (s *Session) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.cols, s.rows = cols, rows
	s.lastActive = time.Now()
	s.mu.Unlock()

	return s.shell.WindowChange(rows, cols)
}

// RunCommand 在独立 channel 中执行命令（监控采集等场景使用）。
func (s *Session) RunCommand(cmd string, timeout time.Duration) (string, error) {
	sess, err := s.client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(cmd)
		ch <- result{out: out, err: err}
	}()

	select {
	case r := <-ch:
		return string(r.out), r.err
	case <-time.After(timeout):
		_ = sess.Signal(gossh.SIGKILL)
		return "", errors.New("命令执行超时")
	}
}

// release 释放底层连接资源（未进入会话管理时的异常路径）。
func (s *Session) release() {
	if s.recorder != nil {
		s.recorder.Close()
		_ = s.owner.audit.DeleteRecording(s.ID)
	}
	_ = s.stdinW.Close()
	_ = s.shell.Close()
	_ = s.client.Close()
}

// close 关闭会话并释放资源（幂等）。
func (s *Session) close(reason string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.reason = reason
	recorder := s.recorder
	s.recorder = nil
	bytesIn, bytesOut := s.bytesIn, s.bytesOut
	startedAt := s.startedAt
	s.mu.Unlock()

	var recSize int64
	if recorder != nil {
		recSize = recorder.Close()
	}
	_ = s.stdinW.Close()
	_ = s.shell.Close()
	_ = s.client.Close()

	status := "closed"
	if strings.Contains(reason, "超时") || strings.Contains(reason, "断开") {
		status = "closed"
	}
	_ = s.owner.audit.EndSession(s.ID, status, reason, bytesIn, bytesOut, recSize)
	s.owner.audit.Log(audit.Entry{
		UserID:     s.UserID,
		Username:   s.Username,
		Action:     audit.ActionSSHDisconnect,
		TargetType: "ssh",
		TargetID:   s.SSHID,
		TargetName: s.SSHName,
		Detail: fmt.Sprintf("会话 %s 结束：%s；时长 %s；上行 %d 字节；下行 %d 字节",
			s.ID, reason, time.Since(startedAt).Round(time.Second), bytesIn, bytesOut),
		IP:      s.ClientIP,
		Success: true,
	})

	s.owner.mu.Lock()
	delete(s.owner.sessions, s.ID)
	s.owner.mu.Unlock()
}

// Close 主动关闭会话。
func (s *Session) Close(reason string) {
	s.broadcastControl(map[string]any{"type": "closed", "reason": reason})
	s.close(reason)
}

// Info 返回会话摘要。
func (s *Session) Info() *Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &Info{
		ID:         s.ID,
		UserID:     s.UserID,
		Username:   s.Username,
		SSHID:      s.SSHID,
		SSHName:    s.SSHName,
		Title:      s.Title,
		Cols:       s.cols,
		Rows:       s.rows,
		StartedAt:  s.startedAt.UTC().Format(time.RFC3339),
		LastActive: s.lastActive.UTC().Format(time.RFC3339),
		BytesIn:    s.bytesIn,
		BytesOut:   s.bytesOut,
	}
}

// Get 按 ID 获取会话。
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// List 列出会话，userID 非空时仅返回该用户的会话。
func (m *Manager) List(userID string) []*Info {
	m.mu.Lock()
	all := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()

	out := make([]*Info, 0, len(all))
	for _, s := range all {
		if userID != "" && s.UserID != userID {
			continue
		}
		out = append(out, s.Info())
	}
	return out
}

// CountByUser 统计用户的活跃会话数。
func (m *Manager) CountByUser(userID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if s.UserID == userID {
			n++
		}
	}
	return n
}

// CloseByUser 断开某用户的全部会话（禁用/删除账号时调用），返回关闭数量。
func (m *Manager) CloseByUser(userID, reason string) int {
	return m.closeMatching(func(s *Session) bool { return s.UserID == userID }, reason)
}

// CloseSSH 断开指向某台 SSH 信息的全部会话（凭证变更或删除时调用）。
func (m *Manager) CloseSSH(sshID, reason string) int {
	return m.closeMatching(func(s *Session) bool { return s.SSHID == sshID }, reason)
}

// CloseAll 关闭全部会话。
func (m *Manager) CloseAll(reason string) int {
	return m.closeMatching(func(*Session) bool { return true }, reason)
}

func (m *Manager) closeMatching(pred func(*Session) bool, reason string) int {
	m.mu.Lock()
	targets := make([]*Session, 0, 4)
	for _, s := range m.sessions {
		if pred(s) {
			targets = append(targets, s)
		}
	}
	m.mu.Unlock()

	for _, s := range targets {
		s.Close(reason)
	}
	return len(targets)
}

// Start 启动空闲会话清理循环。
func (m *Manager) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				m.CloseAll("服务关闭")
				close(m.stopped)
				return
			case <-ticker.C:
				m.reap()
			}
		}
	}()
}

func (m *Manager) reap() {
	m.mu.Lock()
	targets := make([]*Session, 0, 4)
	for _, s := range m.sessions {
		s.mu.Lock()
		idle := time.Since(s.lastActive)
		detached := len(s.sinks) == 0
		closed := s.closed
		s.mu.Unlock()

		// 前端长时间未重连的会话在此回收。
		if !closed && detached && idle > m.idleTTL {
			targets = append(targets, s)
		}
	}
	m.mu.Unlock()

	for _, s := range targets {
		s.Close("空闲超时")
	}
}

// Done 在管理器停止后关闭。
func (m *Manager) Done() <-chan struct{} { return m.stopped }

// ringBuffer 固定容量的环形缓冲，用于断线重连时回放最近的终端输出。
type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func newRingBuffer(max int) *ringBuffer {
	return &ringBuffer{max: max, data: make([]byte, 0, max)}
}

func (r *ringBuffer) Write(p []byte) {
	if r.max <= 0 || len(p) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(p) >= r.max {
		r.data = append(r.data[:0], p[len(p)-r.max:]...)
		return
	}
	if len(r.data)+len(p) > r.max {
		drop := len(r.data) + len(p) - r.max
		r.data = append(r.data[:0], r.data[drop:]...)
	}
	r.data = append(r.data, p...)
}

func (r *ringBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.data))
	copy(out, r.data)
	return out
}
