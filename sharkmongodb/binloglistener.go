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

type BinlogConfig struct {
	Addr     string
	User     string
	Password string
	Logger   *zap.Logger
	Redis    *redis.ClusterClient
	PosKey   string
	Database string
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

// Start 从 Redis 中保存的 resume token 开始监听数据库的 Change Stream，直到 Stop 被调用或发生错误。
// 首次启动时先保存当前 resume token，再开始监听；不会导出已有数据。
// 需要副本集或分片集群。delete 的完整文档需集合开启 changeStreamPreAndPostImages，否则仅包含 documentKey。
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

func (l *BinlogListener) savePosition(ctx context.Context, token bson.Raw) error {
	if len(token) == 0 {
		return errors.New("binlog: invalid position to save")
	}
	if err := l.config.Redis.Set(ctx, l.config.PosKey, []byte(token), 0).Err(); err != nil {
		return fmt.Errorf("binlog: save position to redis: %w", err)
	}
	return nil
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

// OnUpdate 注册更新事件回调，data 为修改后的完整文档。
// 对于 update，MongoDB 的 UpdateLookup 可能返回后续修改后的文档；若文档已删除则监听返回错误。
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
