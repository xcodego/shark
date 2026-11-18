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

type BinlogConfig struct {
	Addr           string
	User           string
	Password       string
	Logger         *zap.Logger
	Redis          *redis.ClusterClient
	PosKey         string
	Database       string
	EnableCanalLog bool
}

type BinlogListener struct {
	config   *BinlogConfig
	onInsert func(table string, data map[string]any)
	onUpdate func(table string, data map[string]any)
	onDelete func(table string, data map[string]any)
	mu       sync.Mutex
	cancel   context.CancelFunc
}

func NewBinlogListener(config *BinlogConfig) *BinlogListener {
	return &BinlogListener{
		config: config,
	}
}

// Start 从 Redis 中保存的位点开始监听，直到 Stop 被调用或发生错误。
// 首次启动时先保存主库当前位点，再开始监听；不会导出已有数据。
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

type canalLogHandler struct {
	slog.Handler
}

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

// OnInsert 注册插入事件回调。
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

// OnDelete 注册删除事件回调。
func (l *BinlogListener) OnDelete(fn func(table string, data map[string]any)) *BinlogListener {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onDelete = fn
	return l
}

type eventHandler struct {
	canal.DummyEventHandler
	listener *BinlogListener
	ctx      context.Context
}

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

func (h *eventHandler) OnPosSynced(header *replication.EventHeader, pos mysql.Position, _ mysql.GTIDSet, _ bool) error {
	if header == nil {
		return nil
	}
	return h.savePosition(pos)
}

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
