package sharkdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"
)

// BinlogConfig 配置 MySQL Binlog 监听器。
type BinlogConfig struct {
	Addr           string               // MySQL 地址，格式 "host:port"
	User           string               // 用户名
	Password       string               // 密码
	Logger         *zap.Logger          // 日志记录器
	Redis          *redis.ClusterClient // 位点存储
	PosKey         string               // 保存 binlog 位点的 Redis 键
	Database       string               // 只监听该库的表变更
	EnableCanalLog bool                 // 是否输出 Canal 内部日志，默认丢弃
}

// BinlogListener 监听 MySQL Binlog 并分发数据变更事件。
// 要求 binlog_row_image=FULL；位点持久化在 Redis，重启后从上次位点继续。
// 回调在 canal 事件协程中同步执行，应尽快返回。
type BinlogListener struct {
	config   *BinlogConfig
	onInsert func(table string, data map[string]any)
	onUpdate func(table string, data map[string]any)
	onDelete func(table string, data map[string]any)
	mu       sync.Mutex
	cancel   context.CancelFunc
}

// NewBinlogListener 创建 Binlog 监听器，需通过 OnInsert/OnUpdate/OnDelete 注册回调后调用 Start。
func NewBinlogListener(config *BinlogConfig) *BinlogListener {
	return &BinlogListener{
		config: config,
	}
}

// Start 从 Redis 中保存的位点开始监听，阻塞直到 Stop 被调用或发生错误。
// 首次启动（Redis 无位点）时先保存主库当前位点再监听，不会导出已有数据。
// 配置缺失、已在运行或位点非法时返回错误；同一监听器同时只能运行一个 Start。
func (l *BinlogListener) Start() error {
	if l.config == nil || l.config.Logger == nil || l.config.Redis == nil ||
		strings.TrimSpace(l.config.Addr) == "" || strings.TrimSpace(l.config.User) == "" ||
		strings.TrimSpace(l.config.PosKey) == "" || strings.TrimSpace(l.config.Database) == "" {
		return errors.New("binlog: config, addr, user, logger, database, redis and posKey are required")
	}

	l.mu.Lock()
	if l.cancel != nil {
		l.mu.Unlock()
		return errors.New("binlog: listener is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	l.mu.Unlock()
	defer func() {
		cancel()
		l.mu.Lock()
		l.cancel = nil
		l.mu.Unlock()
	}()

	value, err := l.config.Redis.Get(ctx, l.config.PosKey).Result()
	var pos mysql.Position
	switch {
	case errors.Is(err, redis.Nil):
	case err != nil:
		return fmt.Errorf("binlog: read position from redis: %w", err)
	default:
		if err := json.Unmarshal([]byte(value), &pos); err != nil {
			return fmt.Errorf("binlog: decode position: %w", err)
		}
		if pos.Name == "" || pos.Pos < 4 {
			return errors.New("binlog: invalid position in redis")
		}
	}

	cfg := l.canalConfig()
	c, err := canal.NewCanal(&cfg)
	if err != nil {
		return fmt.Errorf("binlog: create canal: %w", err)
	}
	handler := &eventHandler{listener: l, ctx: ctx}
	c.SetEventHandler(handler)
	var closeOnce sync.Once
	closeCanal := func() { closeOnce.Do(c.Close) }
	defer closeCanal()

	if err := c.CheckBinlogRowImage("FULL"); err != nil {
		return fmt.Errorf("binlog: check row image: %w", err)
	}
	if pos.Name == "" {
		pos, err = c.GetMasterPos()
		if err != nil {
			return fmt.Errorf("binlog: get current position: %w", err)
		}
		if pos.Name == "" || pos.Pos < 4 {
			return errors.New("binlog: invalid current position")
		}
		if err := handler.savePosition(pos); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			closeCanal()
		case <-done:
		}
	}()
	defer func() {
		close(done)
		<-stopped
	}()
	l.config.Logger.Info("开始监听 binlog", zap.String("database", l.config.Database), zap.String("file", pos.Name), zap.Uint32("position", pos.Pos))
	if err := c.RunFrom(pos); err != nil {
		return fmt.Errorf("binlog: stream: %w", err)
	}
	return ctx.Err()
}

// canalLogHandler 包装 slog.Handler，用于将 canal 日志转发到 zap。
type canalLogHandler struct {
	slog.Handler
}

// canalConfig 构建 canal 配置：不使用 mysqldump，只订阅配置库下的表。
func (l *BinlogListener) canalConfig() canal.Config {
	cfg := canal.NewDefaultConfig()
	cfg.Addr = l.config.Addr
	cfg.User = l.config.User
	cfg.Password = l.config.Password
	cfg.Dump.ExecutionPath = ""
	cfg.IncludeTableRegex = []string{"^" + regexp.QuoteMeta(l.config.Database) + `\.`}
	if l.config.EnableCanalLog {
		cfg.Logger = slog.New(canalLogHandler{Handler: zapslog.NewHandler(l.config.Logger.Core(), zapslog.WithName(l.config.Logger.Name()))})
	} else {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return *cfg
}

// Stop 请求停止监听；Start 返回后监听才算完全停止。
func (l *BinlogListener) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
	}
}

// OnInsert 注册插入事件回调，data 为新行（列名到值）。
func (l *BinlogListener) OnInsert(fn func(table string, data map[string]any)) *BinlogListener {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onInsert = fn
	return l
}

// OnUpdate 注册更新事件回调，data 为修改后的完整数据。
func (l *BinlogListener) OnUpdate(fn func(table string, data map[string]any)) *BinlogListener {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onUpdate = fn
	return l
}

// OnDelete 注册删除事件回调，data 为被删除的行。
func (l *BinlogListener) OnDelete(fn func(table string, data map[string]any)) *BinlogListener {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onDelete = fn
	return l
}

// eventHandler 实现 canal.EventHandler：分发行变更并持久化位点。
type eventHandler struct {
	canal.DummyEventHandler
	listener *BinlogListener
	ctx      context.Context
}

// OnRow 校验行事件后分发给回调；其他库的事件直接忽略。
// 更新事件的 Rows 为 (旧行, 新行) 成对出现，只回调新行。
func (h *eventHandler) OnRow(event *canal.RowsEvent) error {
	if event == nil || event.Table == nil {
		return errors.New("binlog: row event has no table metadata")
	}
	if event.Table.Schema != h.listener.config.Database {
		return nil
	}
	switch event.Action {
	case canal.InsertAction, canal.UpdateAction, canal.DeleteAction:
	default:
		return fmt.Errorf("binlog: unsupported row action %q", event.Action)
	}
	if event.Action == canal.UpdateAction && len(event.Rows)%2 != 0 {
		return errors.New("binlog: update event has an odd number of rows")
	}
	for _, row := range event.Rows {
		if len(row) != len(event.Table.Columns) {
			return fmt.Errorf("binlog: row column count does not match table %s.%s", event.Table.Schema, event.Table.Name)
		}
	}
	rowMap := func(row []any) map[string]any {
		data := make(map[string]any, len(row))
		for i, value := range row {
			data[event.Table.Columns[i].Name] = value
		}
		return data
	}
	h.listener.mu.Lock()
	onInsert, onUpdate, onDelete := h.listener.onInsert, h.listener.onUpdate, h.listener.onDelete
	h.listener.mu.Unlock()
	for i := 0; i < len(event.Rows); i++ {
		switch event.Action {
		case canal.InsertAction:
			if onInsert != nil {
				onInsert(event.Table.Name, rowMap(event.Rows[i]))
			}
		case canal.DeleteAction:
			if onDelete != nil {
				onDelete(event.Table.Name, rowMap(event.Rows[i]))
			}
		case canal.UpdateAction:
			if onUpdate != nil {
				onUpdate(event.Table.Name, rowMap(event.Rows[i+1]))
			}
			i++
		}
	}
	return nil
}

// OnPosSynced 在位点推进时写入 Redis。
func (h *eventHandler) OnPosSynced(header *replication.EventHeader, pos mysql.Position, _ mysql.GTIDSet, _ bool) error {
	if header == nil {
		return nil
	}
	return h.savePosition(pos)
}

// savePosition 将位点序列化为 JSON 保存到 Redis（不过期）。
func (h *eventHandler) savePosition(pos mysql.Position) error {
	if pos.Name == "" || pos.Pos < 4 {
		return errors.New("binlog: invalid position to save")
	}
	value, err := json.Marshal(pos)
	if err != nil {
		return fmt.Errorf("binlog: encode position: %w", err)
	}
	if err := h.listener.config.Redis.Set(h.ctx, h.listener.config.PosKey, value, 0).Err(); err != nil {
		return fmt.Errorf("binlog: save position to redis: %w", err)
	}
	return nil
}
