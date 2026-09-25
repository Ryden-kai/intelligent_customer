// Package ratelimit 实现 v2.2 PR2 的接口级限流。
//
// 设计要点（详见 docs/v2-architecture-design.md §3.6 + docs/v2-ui-security-prd.md §6.3）：
//
//  1. 限流键格式："<dimension>:<value>:<endpoint>"，例如：
//     "ip:1.2.3.4:POST:/api/auth/login" / "tenant_id:tnt_default:POST:/api/chat"
//
//  2. 底层用 golang.org/x/time/rate 的 token bucket；每个 key 一个 Limiter。
//
//  3. 配置存 DB (rate_limit_configs 表)；启动时 + 每次配置变更时 reload。
//
//  4. 内存存储（v2.2 不引入 Redis），重启清空可接受；v2.2.1 评估 LRU。
//
//  5. 触发 429 + Retry-After header；响应 JSON 与 apperr.RateLimited 一致。
package ratelimit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Dimension 限流维度。
type Dimension string

const (
	DimensionIP       Dimension = "ip"
	DimensionTenantID Dimension = "tenant_id"
	DimensionActorID  Dimension = "actor_id"
)

// Config 是 rate_limit_configs 表的内存表示。
type Config struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	Endpoint    string `json:"endpoint"`    // 'POST:/api/auth/login'
	Dimension   string `json:"dimension"`   // ip / tenant_id / actor_id
	PerMinute   int    `json:"per_minute"`  // 阈值（每分钟上限）
	PerHour     int    `json:"per_hour"`    // 阈值（每小时上限；v2.2 暂用 per_minute 单一上限）
	Burst       int    `json:"burst"`       // token bucket 容量
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
}

// Allowed 表示一次 Allow 的判定结果。
type Allowed struct {
	OK         bool          // true = 通过
	RetryAfter time.Duration // 触限时建议等待时间
	Reason     string        // "rate_limit_exceeded" / "config_disabled" / "no_config"
}

// LimiterMap 内存里的限流器池。
//
//   - map[endpointKey]*configSnapshot: 端点 → 当前生效的 Config 列表（按 dimension）
//   - map[fullKey]*rate.Limiter:      (dim, value, endpoint) → token bucket
//   - reloadCh: 配置变更时通知后台 goroutine reload
//
// 读写均通过 RWMutex 保护。
type LimiterMap struct {
	mu       sync.RWMutex
	configs  map[string][]Config          // key = endpoint
	limiters map[string]*rate.Limiter     // key = dimension:value:endpoint
	repo     *Repo

	// reloadCh 触发立即 reload（cap 16，避免极端配置变更风暴丢信号）。
	reloadCh chan struct{}

	// 默认配置（没找到 endpoint 时回退）
	defaultConfig Config
}

// NewLimiterMap 构造 LimiterMap + 立即从 DB 加载 enabled 配置。
func NewLimiterMap(ctx context.Context, repo *Repo, defaultCfg Config) (*LimiterMap, error) {
	if repo == nil {
		return nil, errors.New("ratelimit: repo required")
	}
	m := &LimiterMap{
		configs:  make(map[string][]Config),
		limiters: make(map[string]*rate.Limiter),
		repo:     repo,
		reloadCh: make(chan struct{}, 16),
		defaultConfig: Config{
			Enabled:   false,
			PerMinute: 1000,
			PerHour:   10000,
			Burst:     50,
		},
	}
	if defaultCfg.PerMinute > 0 {
		m.defaultConfig = defaultCfg
	}
	if err := m.Reload(ctx); err != nil {
		return nil, fmt.Errorf("ratelimit: initial load: %w", err)
	}
	return m, nil
}

// Reload 从 DB 重新加载全部 enabled 配置，刷新内存表。
func (m *LimiterMap) Reload(ctx context.Context) error {
	configs, err := m.repo.ListEnabled(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs = make(map[string][]Config, len(configs))
	for _, c := range configs {
		m.configs[c.Endpoint] = append(m.configs[c.Endpoint], c)
	}
	// 清空 limiter 池（force re-create on next Allow），避免旧 config 影响。
	m.limiters = make(map[string]*rate.Limiter)
	return nil
}

// RequestReload 异步触发 reload；非阻塞。
func (m *LimiterMap) RequestReload() {
	select {
	case m.reloadCh <- struct{}{}:
	default:
		// 已有 pending 信号；不重复投递。
	}
}

// Run 后台 goroutine：监听 reloadCh → 调 Reload；ctx cancel 退出。
func (m *LimiterMap) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.reloadCh:
			if err := m.Reload(ctx); err != nil {
				// 错误吞掉（已被调用方记录）；下个 tick 再来。
				_ = err
			}
		}
	}
}

// Allow 判定 (dim, value, endpoint) 是否触发限流。
//
// 维度值 value 为空时直接放行（防误封 IP 未传、tenant 缺失等异常）。
// endpoint 未配置时回退 defaultConfig（默认 disabled = 放行）。
//
// 返回 Allowed.OK = false 时携带 RetryAfter（基于桶容量与速率估算）。
func (m *LimiterMap) Allow(dimension Dimension, value, endpoint string) Allowed {
	if value == "" || endpoint == "" {
		return Allowed{OK: true, Reason: "no_dimension_value"}
	}
	m.mu.RLock()
	cfgs, has := m.configs[endpoint]
	defaultCfg := m.defaultConfig
	m.mu.RUnlock()

	if !has {
		// 没找到 endpoint 配置：用 default；disabled 直接放行。
		if !defaultCfg.Enabled {
			return Allowed{OK: true, Reason: "no_config"}
		}
		cfgs = []Config{defaultCfg}
	}

	// 找到匹配 dimension 的 config。
	var matched *Config
	for i := range cfgs {
		if string(dimension) == cfgs[i].Dimension {
			matched = &cfgs[i]
			break
		}
	}
	if matched == nil {
		return Allowed{OK: true, Reason: "no_config_for_dimension"}
	}
	if !matched.Enabled {
		return Allowed{OK: true, Reason: "config_disabled"}
	}

	key := BuildKey(dimension, value, endpoint)
	lim := m.limiterFor(key, matched)
	if lim.Allow() {
		return Allowed{OK: true}
	}
	// 计算 Retry-After：1 / rate 秒，但留 0.5s buffer。
	ratePerSec := rate.Limit(float64(matched.PerMinute) / 60.0)
	if ratePerSec <= 0 {
		ratePerSec = 1
	}
	reservation := lim.Reserve()
	if reservation.OK() {
		delay := reservation.Delay()
		reservation.Cancel()
		if delay > 0 {
			return Allowed{OK: false, RetryAfter: delay, Reason: "rate_limit_exceeded"}
		}
	}
	return Allowed{OK: false, RetryAfter: time.Second, Reason: "rate_limit_exceeded"}
}

// limiterFor 取 key 对应的 limiter；不存在则按 cfg 创建。
func (m *LimiterMap) limiterFor(key string, cfg *Config) *rate.Limiter {
	m.mu.RLock()
	if l, ok := m.limiters[key]; ok {
		m.mu.RUnlock()
		return l
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.limiters[key]; ok {
		return l
	}
	ratePerSec := rate.Limit(float64(cfg.PerMinute) / 60.0)
	if ratePerSec <= 0 {
		ratePerSec = 1
	}
	burst := cfg.Burst
	if burst <= 0 {
		burst = int(ratePerSec) + 1
	}
	l := rate.NewLimiter(ratePerSec, burst)
	m.limiters[key] = l
	return l
}

// BuildKey 拼接限流键。
func BuildKey(dim Dimension, value, endpoint string) string {
	return string(dim) + ":" + value + ":" + endpoint
}

// ConfigForDebug 暴露当前 endpoint 的配置列表（管理后台用）。
func (m *LimiterMap) ConfigForDebug(endpoint string) []Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Config, len(m.configs[endpoint]))
	copy(out, m.configs[endpoint])
	return out
}

// Snapshot 返回 (endpoint → []Config) 的浅拷贝（管理后台"当前生效配置"页）。
func (m *LimiterMap) Snapshot() map[string][]Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string][]Config, len(m.configs))
	for k, v := range m.configs {
		cp := make([]Config, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// keep json imported even when not used
var _ = json.Marshal
var _ = strings.Repeat

// keep sql import referenced
var _ sql.IsolationLevel = sql.LevelDefault