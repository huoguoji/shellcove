package api

import (
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/audit"
	"github.com/huoguoji/shellcove/internal/user"
)

func (s *Server) registerTerminalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ws/terminal/{sessionId}", s.protected(s.handleTerminalWS))
}

// wsSink 把 SSHSession 的输出转发到 WebSocket 连接。
type wsSink struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsSink) sendJSON(payload any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return w.conn.WriteJSON(payload)
}

// SendData 以 base64 传输原始字节，避免多字节字符被拆包破坏。
func (w *wsSink) SendData(data []byte) error {
	return w.sendJSON(map[string]any{
		"type":     "output",
		"encoding": "base64",
		"data":     base64.StdEncoding.EncodeToString(data),
	})
}

// SendControl 发送控制消息（ready / exit / error / pong）。
func (w *wsSink) SendControl(msg map[string]any) error {
	return w.sendJSON(msg)
}

type wsMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// handleTerminalWS 终端数据通道：接入既有会话，支持断线重连回放。
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	if claims == nil {
		writeError(w, apperr.ErrUnauthorized)
		return
	}
	id := r.PathValue("sessionId")
	session, ok := s.Manager.Get(id)
	if !ok {
		writeError(w, apperr.ErrNotFound.WithMessage("会话不存在或已结束"))
		return
	}
	if claims.Role != user.RoleAdmin && session.UserID != claims.UserID {
		writeError(w, apperr.ErrNoPermission)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	sink := &wsSink{conn: conn}
	if err := session.Attach(sink); err != nil {
		_ = sink.SendControl(map[string]any{"type": "exit", "reason": apperr.MessageOf(err)})
		return
	}
	defer session.Detach(sink)

	// 保活：定期发送 ping 控制帧，避免中间设备断开空闲连接。
	heartbeat := make(chan struct{})
	defer close(heartbeat)
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeat:
				return
			case <-ticker.C:
				if err := sink.SendControl(map[string]any{"type": "ping"}); err != nil {
					return
				}
			}
		}
	}()

	info := session.Info()
	s.log(r, audit.ActionSSHConnect, "session", id, session.SSHName,
		"接入终端（"+info.Title+"）", true)

	for {
		var msg wsMessage
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}
		switch msg.Type {
		case "input":
			if msg.Data == "" {
				continue
			}
			if err := session.WriteInput([]byte(msg.Data)); err != nil {
				_ = sink.SendControl(map[string]any{"type": "exit", "reason": apperr.MessageOf(err)})
				s.log(r, audit.ActionSSHDisconnect, "session", id, session.SSHName, "会话写入失败", false)
				return
			}
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				_ = session.Resize(msg.Cols, msg.Rows)
			}
		case "ping":
			_ = sink.SendControl(map[string]any{"type": "pong", "time": time.Now().UTC().Format(time.RFC3339)})
		}
	}

	s.log(r, audit.ActionSSHDisconnect, "session", id, session.SSHName, "终端连接已断开", true)
}
