// binloglistener.go 基于 MongoDB Change Stream 实现类似 binlog 的数据变更监听。

package sharkmongodb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.uber.org/zap"
)

// BinlogConfig 是 BinlogListener 的配置，除 User、Password 外均为必填。
type BinlogConfig struct {
	Addr     string               // MongoDB 地址，格式为 host:port
	User     string               // 用户名
	Password string               // 密码
	Logger   *zap.Logger          // 日志
	Redis    *redis.ClusterClient // 保存 resume token 的 Redis
	PosKey   string               // resume token 在 Redis 中的 key
	Database string               // 监听的数据库，覆盖其下所有集合
}

// BinlogListener 监听整个数据库的 Change Stream，并把增删改事件分发给回调。
// resume token 持久化在 Redis 中，重启后从上次处理到的位置继续（至少一次，回调需幂等）。
type BinlogListener struct {
	config   *BinlogConfig
	onInsert func(table string, data map[string]any)
	onUpdate func(table string, data map[string]any)
	onDelete func(table string, data map[string]any)
	mu       sync.Mutex
	cancel   context.CancelFunc
}

// NewBinlogListener 创建监听器，配置校验推迟到 Start。
func NewBinlogListener(config *BinlogConfig) *BinlogListener {
	return &BinlogListener{
		config: config,
	}
}

// changeEvent 是 Change Stream 事件中用到的字段。
type changeEvent struct {
	ID            bson.Raw `bson:"_id"`
	OperationType string   `bson:"operationType"`
	NS            struct {
		Coll string `bson:"coll"`
	} `bson:"ns"`
	DocumentKey              map[string]any `bson:"documentKey"`
	FullDocument             map[string]any `bson:"fullDocument"`
	FullDocumentBeforeChange map[string]any `bson:"fullDocumentBeforeChange"`
}

// Start 阻塞监听 Change Stream，直到出错或 Stop/ctx 取消；同一监听器同时只能运行一个 Start。
// 从 Redis 保存的 resume token 继续；首次启动从当前位置开始，不导出旧数据。
// 事件的回调同步执行，回调返回后才保存位置。
// 需要副本集或分片集群；delete 事件的完整文档需启用 changeStreamPreAndPostImages，否则回调只收到 documentKey。
func (l *BinlogListener) Start() error {
	if l.config == nil || strings.TrimSpace(l.config.Addr) == "" || l.config.Logger == nil || l.config.Redis == nil ||
		strings.TrimSpace(l.config.PosKey) == "" || strings.TrimSpace(l.config.Database) == "" {
		return errors.New("binlog: config, addr, logger, database, redis and posKey are required")
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

	// 没有已保存位置（redis.Nil）时 token 为空，后面从当前位置开始
	value, err := l.config.Redis.Get(ctx, l.config.PosKey).Result()
	var token bson.Raw
	switch {
	case errors.Is(err, redis.Nil):
	case err != nil:
		return fmt.Errorf("binlog: read position from redis: %w", err)
	default:
		token = bson.Raw(value)
		if err := token.Validate(); err != nil || len(token) == 0 {
			return errors.New("binlog: invalid position in redis")
		}
	}

	opts := options.ChangeStream().
		SetFullDocument(options.UpdateLookup).
		SetFullDocumentBeforeChange(options.WhenAvailable)
	if token != nil {
		opts.SetResumeAfter(token)
	}
	client, err := New(ctx, &Config{Host: l.config.Addr, User: l.config.User, Password: l.config.Password})
	if err != nil {
		return fmt.Errorf("binlog: connect mongodb: %w", err)
	}
	defer client.Disconnect(context.Background())

	cs, err := client.Database(l.config.Database).Watch(ctx, mongo.Pipeline{}, opts)
	if err != nil {
		return fmt.Errorf("binlog: open change stream: %w", err)
	}
	defer cs.Close(context.Background())

	// 首次启动立即保存当前位置，避免没有事件时重启丢失启动之后的变更
	if token == nil {
		if token = cs.ResumeToken(); len(token) == 0 {
			return errors.New("binlog: invalid current position")
		}
		if err := l.savePosition(ctx, token); err != nil {
			return err
		}
	}
	l.config.Logger.Info("开始监听 change stream", zap.String("database", l.config.Database))

	for cs.Next(ctx) {
		var event changeEvent
		if err := cs.Decode(&event); err != nil {
			return fmt.Errorf("binlog: decode event: %w", err)
		}
		if err := l.dispatch(&event); err != nil {
			return err
		}
		if err := l.savePosition(ctx, event.ID); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cs.Err(); err != nil {
		return fmt.Errorf("binlog: stream: %w", err)
	}
	return nil
}

// dispatch 按操作类型调用对应回调。update/replace 事件缺少完整文档时返回错误，
// delete 事件优先使用变更前文档，否则退化为 documentKey。
func (l *BinlogListener) dispatch(e *changeEvent) error {
	l.mu.Lock()
	onInsert, onUpdate, onDelete := l.onInsert, l.onUpdate, l.onDelete
	l.mu.Unlock()
	switch e.OperationType {
	case "insert":
		if onInsert != nil {
			onInsert(e.NS.Coll, e.FullDocument)
		}
	case "update", "replace":
		if onUpdate != nil {
			if e.FullDocument == nil {
				return fmt.Errorf("binlog: %s event for collection %s has no full document", e.OperationType, e.NS.Coll)
			}
			onUpdate(e.NS.Coll, e.FullDocument)
		}
	case "delete":
		if onDelete != nil {
			data := e.FullDocumentBeforeChange
			if data == nil {
				data = e.DocumentKey
			}
			onDelete(e.NS.Coll, data)
		}
	}
	return nil
}

// savePosition 将 resume token 保存到 Redis（不过期）。
func (l *BinlogListener) savePosition(ctx context.Context, token bson.Raw) error {
	if len(token) == 0 {
		return errors.New("binlog: invalid position to save")
	}
	if err := l.config.Redis.Set(ctx, l.config.PosKey, []byte(token), 0).Err(); err != nil {
		return fmt.Errorf("binlog: save position to redis: %w", err)
	}
	return nil
}

// Stop 请求停止监听，Start 随后返回 context.Canceled；未运行时无操作。
func (l *BinlogListener) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
	}
}

// OnInsert 注册插入事件回调，table 为集合名；返回自身以便链式调用。
func (l *BinlogListener) OnInsert(fn func(table string, data map[string]any)) *BinlogListener {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onInsert = fn
	return l
}

// OnUpdate 注册 update 与 replace 事件回调；data 为 UpdateLookup 得到的最新完整文档，缺失时 Start 返回错误。
func (l *BinlogListener) OnUpdate(fn func(table string, data map[string]any)) *BinlogListener {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onUpdate = fn
	return l
}

// OnDelete 注册删除事件回调；data 为变更前文档，未启用前后镜像时为 documentKey。
func (l *BinlogListener) OnDelete(fn func(table string, data map[string]any)) *BinlogListener {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onDelete = fn
	return l
}
