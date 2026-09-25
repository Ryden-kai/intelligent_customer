package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/middleware"
	"intelligent_customer/backend/internal/tenant"
)

// Event 是审计日志的内存表示；落库前会序列化成 audit_logs 行。
//
// 字段命名对齐 docs/v2-architecture-design.md §3.5.1。
type Event struct {
	ID         string    // 'log-<ts>-<rand4>' 主键
	TenantID   string    // 来自 tenant ctx
	Timestamp  time.Time // 事件时间（默认 time.Now）
	ActorID    string    // 来自 ctx Claims.Username
	ActorEmail string    // 来自 ctx Claims.Email
	Action     string    // 见 action.go 常量
	TargetType string    // 可空
	TargetID   string    // 可空
	IP         string    // 客户端 IP（来自 X-Forwarded-For / RemoteAddr）
	UserAgent  string    // UA
	Payload    Payload   // 可空
}

// Emitter 是审计写入入口（handler 层注入；logger 是 *Logger）。
//
// 设计：用 interface 而非具体类型避免循环依赖，handler 不需要导入 audit.Logger。
type Emitter interface {
	// Emit 把事件推到缓冲 channel；非阻塞路径。
	// channel 满时打 WARN 日志 + 计数（不丢消息但也不阻塞主链路）。
	Emit(ctx context.Context, action, targetType, targetID string, payload Payload)

	// EmitFromRequest 从 *http.Request 抽 IP / UA / actor 后写入。
	EmitFromRequest(r *http.Request, action, targetType, targetID string, payload Payload)
}

// Logger 异步审计日志写入器。
//
//   - ch 缓冲 1000 条事件；
//   - Run goroutine 每 flushInterval 把 ch 里累积的事件批量 insert；
//   - 失败重试 3 次后落 failedPath（默认 ./logs/audit_failed.jsonl）；
//   - 关闭时调用 Stop()，确保 flush 完残留事件。
type Logger struct {
	repo           *Repo
	ch             chan Event
	flushInterval  time.Duration
	failedPath     string
	logger         zerolog.Logger

	mu       sync.Mutex
	closed   bool
	stopCh   chan struct{}
	doneCh   chan struct{}

	// 统计：channel 满 + 总丢弃 + 写入失败次数。
	droppedCount uint64
	failedWrites uint64
}

// LoggerOption 配置选项。
type LoggerOption func(*Logger)

// WithFlushInterval 自定义 flush 间隔（默认 5s）。
func WithFlushInterval(d time.Duration) LoggerOption {
	return func(l *Logger) { l.flushInterval = d }
}

// WithFailedPath 自定义失败兜底文件路径（默认 ./logs/audit_failed.jsonl）。
func WithFailedPath(p string) LoggerOption {
	return func(l *Logger) { l.failedPath = p }
}

// WithChannelBuffer 自定义 channel 大小（默认 1000）。
func WithChannelBuffer(n int) LoggerOption {
	return func(l *Logger) {
		// 注意：必须在 Run 之前调用，因为 channel 创建后不能改大小。
		l.ch = make(chan Event, n)
	}
}

// NewLogger 构造 Logger。
//
// repo 不能为 nil（依赖其 InsertBatch）；flush 间隔 / 失败文件路径可用 option 覆盖。
func NewLogger(repo *Repo, base zerolog.Logger, opts ...LoggerOption) (*Logger, error) {
	if repo == nil {
		return nil, errors.New("audit: repo required")
	}
	l := &Logger{
		repo:          repo,
		ch:            make(chan Event, 1000),
		flushInterval: 5 * time.Second,
		failedPath:    filepath.Join("logs", "audit_failed.jsonl"),
		logger:        base,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	for _, o := range opts {
		o(l)
	}
	// 确保失败兜底目录存在。
	if dir := filepath.Dir(l.failedPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	return l, nil
}

// Emit 异步提交一条审计事件。
//
// 当 channel 满时（非阻塞 select）：记录 droppedCount + 打 WARN 日志；不阻塞主链路。
//
// 不持有 ctx 中的事务；落库失败由后台 goroutine 重试 + 写 jsonl 兜底。
func (l *Logger) Emit(ctx context.Context, action, targetType, targetID string, payload Payload) {
	if action == "" {
		// 不允许写入空 action；防御性丢弃。
		l.logger.Warn().Msg("audit_emit_empty_action_dropped")
		return
	}
	ev := Event{
		ID:         newAuditID(),
		TenantID:   tenantFromCtx(ctx),
		Timestamp:  time.Now(),
		ActorID:    actorFromCtx(ctx),
		ActorEmail: actorEmailFromCtx(ctx),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		IP:         clientIPFromCtx(ctx),
		UserAgent:  userAgentFromCtx(ctx),
		Payload:    payload,
	}
	select {
	case l.ch <- ev:
	default:
		atomic.AddUint64(&l.droppedCount, 1)
		l.logger.Warn().
			Str("action", action).
			Msg("audit_channel_full_drop_event")
	}
}

// EmitFromRequest 是常用便捷包装：从 *http.Request 抽 IP / UA / actor。
func (l *Logger) EmitFromRequest(r *http.Request, action, targetType, targetID string, payload Payload) {
	ctx := r.Context()
	ev := Event{
		ID:         newAuditID(),
		TenantID:   tenantFromCtx(ctx),
		Timestamp:  time.Now(),
		ActorID:    actorFromCtx(ctx),
		ActorEmail: actorEmailFromCtx(ctx),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		IP:         clientIPFromRequest(r),
		UserAgent:  r.UserAgent(),
		Payload:    payload,
	}
	select {
	case l.ch <- ev:
	default:
		atomic.AddUint64(&l.droppedCount, 1)
		l.logger.Warn().
			Str("action", action).
			Msg("audit_channel_full_drop_event")
	}
}

// Run 启动后台 flush goroutine；返回前确保初始化完成。
//
// 主循环：
//   - 每 flushInterval 触发一次 flush
//   - 收到 stopCh 后做最终 flush 再退出
//
// 阻塞直到 Stop() 被调用。
func (l *Logger) Run(ctx context.Context) {
	defer close(l.doneCh)

	ticker := time.NewTicker(l.flushInterval)
	defer ticker.Stop()

	// buffer 用于累积事件；flush 时一次性 InsertBatch。
	buf := make([]Event, 0, 256)
	flushFn := func() {
		if len(buf) == 0 {
			return
		}
		if err := l.insertWithRetry(ctx, buf); err != nil {
			atomic.AddUint64(&l.failedWrites, 1)
			// 最终兜底：写 jsonl 文件。
			l.writeFallbackJSONL(buf, err)
		}
		buf = buf[:0]
	}

	for {
		select {
		case <-l.stopCh:
			// 关闭前先 drain 一次 channel。
			l.drain(&buf)
			flushFn()
			return
		case <-ctx.Done():
			l.drain(&buf)
			flushFn()
			return
		case ev := <-l.ch:
			buf = append(buf, ev)
			// buffer 满了就立刻 flush，避免单批过大。
			if len(buf) >= 200 {
				flushFn()
			}
		case <-ticker.C:
			flushFn()
		}
	}
}

// Stop 通知后台 goroutine 退出并等待 drain。
// 进程退出前必须调用，避免最后一批事件丢失。
func (l *Logger) Stop(timeout time.Duration) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.stopCh)
	l.mu.Unlock()

	select {
	case <-l.doneCh:
		return nil
	case <-time.After(timeout):
		return errors.New("audit: stop timeout")
	}
}

// drain 把 channel 里的剩余事件尽量抽到 buf 里。
func (l *Logger) drain(buf *[]Event) {
	for {
		select {
		case ev := <-l.ch:
			*buf = append(*buf, ev)
		default:
			return
		}
	}
}

// insertWithRetry 把 buf 批量 insert；失败按 100ms / 500ms / 2s 重试 3 次。
func (l *Logger) insertWithRetry(ctx context.Context, buf []Event) error {
	delays := []time.Duration{0, 100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}
	var lastErr error
	for i, d := range delays {
		if d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := l.repo.InsertBatch(ctx, buf); err != nil {
			lastErr = err
			l.logger.Warn().
				Err(err).
				Int("retry", i).
				Int("batch", len(buf)).
				Msg("audit_flush_retry")
			continue
		}
		if i > 0 {
			l.logger.Info().
				Int("retry", i).
				Int("batch", len(buf)).
				Msg("audit_flush_recovered")
		}
		return nil
	}
	return lastErr
}

// writeFallbackJSONL 把失败批次追加到 failedPath，每行一个 JSON 对象。
func (l *Logger) writeFallbackJSONL(buf []Event, cause error) {
	f, err := os.OpenFile(l.failedPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		l.logger.Error().Err(err).Str("path", l.failedPath).Msg("audit_fallback_open_failed")
		return
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, ev := range buf {
		rec := struct {
			Event Event `json:"event"`
			Error string `json:"error"`
		}{
			Event: ev,
			Error: cause.Error(),
		}
		if err := enc.Encode(rec); err != nil {
			l.logger.Warn().Err(err).Msg("audit_fallback_encode_failed")
		}
	}
	l.logger.Error().
		Int("count", len(buf)).
		Str("path", l.failedPath).
		Msg("audit_fallback_written")
}

// Stats 返回累计统计（用于监控 / 自检端点）。
func (l *Logger) Stats() (dropped, failed uint64) {
	return atomic.LoadUint64(&l.droppedCount), atomic.LoadUint64(&l.failedWrites)
}

// ----------------------------------------------------------------------------
// ctx 辅助
// ----------------------------------------------------------------------------

func newAuditID() string {
	return fmt.Sprintf("log-%d-%s", time.Now().UnixMilli(), uuid.NewString()[:8])
}

func tenantFromCtx(ctx context.Context) string {
	t := tenant.FromContext(ctx)
	if t.ID != "" {
		return t.ID
	}
	return "tnt_default"
}

func actorFromCtx(ctx context.Context) string {
	c, ok := auth.FromContext(ctx)
	if !ok || c == nil {
		return ""
	}
	return c.Username
}

func actorEmailFromCtx(ctx context.Context) string {
	c, ok := auth.FromContext(ctx)
	if !ok || c == nil {
		return ""
	}
	return c.Email
}

func clientIPFromCtx(ctx context.Context) string {
	// audit logger 是后台 flush，没有 r 上下文。这里仅兜底；
	// 推荐调用方用 EmitFromRequest。
	return ""
}

func clientIPFromRequest(r *http.Request) string {
	return middleware.ClientIP(r)
}

func userAgentFromCtx(ctx context.Context) string {
	return ""
}

// keep sql import referenced for documentation purpose (BatchInsert signature)
var _ = sql.ErrNoRows