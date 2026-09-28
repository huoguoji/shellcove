package ssh

import (
	"context"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/huoguoji/shellcove/internal/sshinfo"
)

// DefaultPoolTTL 连接空闲多久后允许回收。
const DefaultPoolTTL = 5 * time.Minute

// Pool 按主机复用 SSH 连接，供低频短任务（例如监控采集）使用。
//
// 监控面板每 5 秒采集一次，如果每次都重新握手，既白花一次往返，也会在目标机上
// 不断创建又销毁 sshd 会话；这里按 SSH 信息缓存连接，空闲超时后由后台回收。
type Pool struct {
	mu    sync.Mutex
	items map[string]*pooledConn
	ttl   time.Duration
}

type pooledConn struct {
	client   *ssh.Client
	lastUsed time.Time
}

// NewPool 构造连接池，ttl 不大于 0 时使用 DefaultPoolTTL。
func NewPool(ttl time.Duration) *Pool {
	if ttl <= 0 {
		ttl = DefaultPoolTTL
	}
	return &Pool{items: map[string]*pooledConn{}, ttl: ttl}
}

// Get 取用指定主机的连接，池中没有时新建。
// 返回的 reused 表示连接来自缓存（未重新握手），上层可据此决定失败后是否重试。
func (p *Pool) Get(sshID string, creds *sshinfo.Credentials) (client *ssh.Client, reused bool, err error) {
	p.mu.Lock()
	if item, ok := p.items[sshID]; ok {
		item.lastUsed = time.Now()
		cached := item.client
		p.mu.Unlock()
		return cached, true, nil
	}
	p.mu.Unlock()

	dialed, err := Dial(creds)
	if err != nil {
		return nil, false, err
	}

	p.mu.Lock()
	// 并发场景下若其他协程已建好连接，则复用并释放本次新建的连接。
	if item, ok := p.items[sshID]; ok {
		item.lastUsed = time.Now()
		cached := item.client
		p.mu.Unlock()
		dialed.Close()
		return cached, true, nil
	}
	p.items[sshID] = &pooledConn{client: dialed, lastUsed: time.Now()}
	p.mu.Unlock()
	return dialed, false, nil
}

// Drop 关闭并移除指定主机的连接（主机删除或凭证变更时调用）。
func (p *Pool) Drop(sshID string) {
	p.mu.Lock()
	item, ok := p.items[sshID]
	if ok {
		delete(p.items, sshID)
	}
	p.mu.Unlock()

	if ok {
		item.client.Close()
	}
}

// Reset 丢弃指定的失效连接并重新建立（仅当它仍是池中当前连接时才关闭）。
func (p *Pool) Reset(sshID string, creds *sshinfo.Credentials, stale *ssh.Client) (*ssh.Client, error) {
	p.mu.Lock()
	item, ok := p.items[sshID]
	if ok && item.client == stale {
		delete(p.items, sshID)
	}
	p.mu.Unlock()

	if ok && item.client == stale {
		item.client.Close()
	}
	client, _, err := p.Get(sshID, creds)
	return client, err
}

// Start 启动后台回收，直到 ctx 结束。
func (p *Pool) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				p.CloseAll()
				return
			case <-ticker.C:
				p.Reap()
			}
		}
	}()
}

// Reap 关闭空闲超时的连接。
func (p *Pool) Reap() {
	now := time.Now()
	var stale []*pooledConn

	p.mu.Lock()
	for id, item := range p.items {
		if now.Sub(item.lastUsed) > p.ttl {
			delete(p.items, id)
			stale = append(stale, item)
		}
	}
	p.mu.Unlock()

	for _, item := range stale {
		item.client.Close()
	}
}

// CloseAll 关闭全部连接。
func (p *Pool) CloseAll() {
	p.mu.Lock()
	items := p.items
	p.items = map[string]*pooledConn{}
	p.mu.Unlock()

	for _, item := range items {
		item.client.Close()
	}
}
