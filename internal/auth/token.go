package auth

import (
	"crypto/subtle"
	"strings"
	"sync"
	"time"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/crypto"
)

// 一次性令牌用途。
const (
	PurposeReveal = "reveal"
	PurposeExport = "export"
	PurposeImport = "import"
)

type tokenEntry struct {
	userID  string
	purpose string
	expires time.Time
}

// TokenStore 保存短期一次性令牌（内存态，重启即失效）。
type TokenStore struct {
	mu    sync.Mutex
	items map[string]tokenEntry
}

// NewTokenStore 构造一次性令牌存储。
func NewTokenStore() *TokenStore {
	return &TokenStore{items: make(map[string]tokenEntry)}
}

// Issue 签发一次性令牌。
func (t *TokenStore) Issue(userID, purpose string, ttl time.Duration) (string, time.Time, error) {
	token, err := crypto.RandomToken(24)
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(ttl)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.evictLocked()
	t.items[token] = tokenEntry{userID: userID, purpose: purpose, expires: expires}
	return token, expires, nil
}

// Consume 校验并消费令牌，返回所属用户 ID。
func (t *TokenStore) Consume(token, purpose string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.evictLocked()

	entry, ok := t.items[token]
	if !ok {
		return "", apperr.ErrExportTokenInvalid
	}
	delete(t.items, token)

	if entry.purpose != purpose || time.Now().After(entry.expires) {
		return "", apperr.ErrExportTokenInvalid
	}
	return entry.userID, nil
}

// Peek 校验令牌但不消费，用于上传后多步校验。
func (t *TokenStore) Peek(token, purpose string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.evictLocked()

	entry, ok := t.items[token]
	if !ok {
		return "", apperr.ErrExportTokenInvalid
	}
	if entry.purpose != purpose || time.Now().After(entry.expires) {
		return "", apperr.ErrExportTokenInvalid
	}
	return entry.userID, nil
}

func (t *TokenStore) evictLocked() {
	now := time.Now()
	for k, v := range t.items {
		if now.After(v.expires) {
			delete(t.items, k)
		}
	}
}

// RateCounter 统计窗口内的操作次数，用于查看凭证短时间多次调用告警。
type RateCounter struct {
	mu     sync.Mutex
	events map[string][]time.Time
	window time.Duration
	limit  int
}

// NewRateCounter 构造计数器。
func NewRateCounter(window time.Duration, limit int) *RateCounter {
	return &RateCounter{events: map[string][]time.Time{}, window: window, limit: limit}
}

// Hit 记录一次操作，返回是否超过阈值。
func (r *RateCounter) Hit(key string) bool {
	if r == nil || r.limit <= 0 {
		return false
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	kept := make([]time.Time, 0, len(r.events[key])+1)
	for _, ts := range r.events[key] {
		if now.Sub(ts) <= r.window {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	r.events[key] = kept
	return len(kept) >= r.limit
}

// Count 返回窗口内已记录次数。
func (r *RateCounter) Count(key string) int {
	if r == nil {
		return 0
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, ts := range r.events[key] {
		if now.Sub(ts) <= r.window {
			n++
		}
	}
	return n
}

// constantTimeEqual 常量时间字符串比较。
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// normalizeCode 去掉验证码中的空格与连字符。
func normalizeCode(code string) string {
	replacer := strings.NewReplacer(" ", "", "-", "", "\t", "")
	return replacer.Replace(strings.TrimSpace(code))
}
