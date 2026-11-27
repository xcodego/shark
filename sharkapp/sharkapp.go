// Package sharkapp 初始化应用及其中间件，并管理服务生命周期。
package sharkapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/gin-gonic/gin"
	"github.com/howeyc/crc16"
	"github.com/panjf2000/ants/v2"
	"github.com/xcodego/shark/sharkclickhouse"
	"github.com/xcodego/shark/sharkdb"
	"github.com/xcodego/shark/sharkelastic"
	"github.com/xcodego/shark/sharketcd"
	"github.com/xcodego/shark/sharkgrpc"
	"github.com/xcodego/shark/sharkhttp"
	"github.com/xcodego/shark/sharkkafka"
	"github.com/xcodego/shark/sharkminio"
	"github.com/xcodego/shark/sharkmongodb"
	"github.com/xcodego/shark/sharkrabbitmq"
	"github.com/xcodego/shark/sharkredis"
	"github.com/xcodego/shark/sharkrisingwave"
	"github.com/xcodego/shark/sharkutils"
	"github.com/xcodego/shark/sharkws"

	"github.com/xcodego/shark/sharklog"
	"github.com/xcodego/shark/sharktimer"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/minio/minio-go/v7"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// App 保存应用生命周期状态及已初始化的服务和中间件。
type App struct {
	// Wg 等待已登记的后台 goroutine。
	Wg *sync.WaitGroup
	// Sg 合并相同 key 的并发请求。
	Sg *singleflight.Group
	// Context 是应用共享的 context。
	Context context.Context

	// Id 是实例编号。
	Id string
	// Env 是运行环境。
	Env string
	// Name 是服务名称。
	Name string
	// Project 是项目名称。
	Project string

	// Db 是 MySQL 数据库连接。
	Db *gorm.DB
	// Grpc 是 gRPC 服务端。
	Grpc *sharkgrpc.Server
	// Minio 是 MinIO 客户端。
	Minio *minio.Client
	// Timer 是定时器组件。
	Timer *sharktimer.Client
	// Kafka 是 Kafka 客户端。
	Kafka *sharkkafka.Client
	// RedisCluster 是 Redis 集群客户端。
	RedisCluster *redis.ClusterClient
	// RedisClient 是 Redis 单机或主从客户端。
	RedisClient *redis.Client
	// RedisHelper 提供 Redis 常用操作。
	RedisHelper *sharkredis.Helper
	// Logger 是结构化日志记录器。
	Logger *zap.Logger
	// Elastic 是 Elasticsearch 客户端。
	Elastic *sharkelastic.Client
	// ClickHouse 是 ClickHouse 原生客户端。
	ClickHouse driver.Conn
	// Etcd 是 etcd 客户端。
	Etcd *clientv3.Client
	// Mongodb 是 MongoDB 客户端。
	Mongodb *mongo.Client
	// Rabbitmq 是 RabbitMQ 客户端。
	Rabbitmq *sharkrabbitmq.Client
	// RisingWave 是 RisingWave 数据库连接。
	RisingWave *gorm.DB
	// GinEngine 是 Gin HTTP 引擎。
	GinEngine *gin.Engine
	// Ws 是 WebSocket 服务组件。
	Ws *sharkws.Server
	// AntPool 管理并发任务。
	AntPool *ants.Pool

	// cancelFunc 取消应用 context。
	cancelFunc context.CancelFunc
	// sharklog 封装 Shark 日志组件。
	sharklog *sharklog.SharkLog
	// muxServe 是 pprof HTTP 路由复用器。
	muxServe *http.ServeMux
}

// New 根据配置创建 App，并初始化已配置的中间件和服务。
func New(options *Options) (*App, error) {
	if options == nil {
		return nil, fmt.Errorf("options required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	app := &App{
		Context:    ctx,
		cancelFunc: cancel,
		Project:    options.project,
		Name:       options.name,
		Id:         options.id,
		Env:        options.env,
		Wg:         &sync.WaitGroup{},
		Sg:         &singleflight.Group{},
	}
	if options.antpool > 0 {
		pool, err := ants.NewPool(options.antpool)
		if err != nil {
			return nil, fmt.Errorf("创建 ants 池失败: %w", err)
		}
		app.AntPool = pool
	} else {
		pool, err := ants.NewPool(
			-1,
			ants.WithExpiryDuration(time.Minute),
			ants.WithPanicHandler(func(v any) {
				if app.Logger != nil {
					app.Logger.Error("sharkapp ants worker panic", zap.Any("panic", v))
				}
			}),
		)
		if err != nil {
			return nil, fmt.Errorf("创建 ants 池失败: %w", err)
		}
		app.AntPool = pool
	}

	app.sharklog = sharklog.New(app.Context, app.Name, app.Id)
	app.Logger = app.sharklog.Zap

	// ---- Kafka 初始化 ----
	if options.kafka != nil {
		app.Logger.Info("正在连接kafka", zap.Strings("host", options.kafka.Host), zap.String("user", options.kafka.User))
		kafka, err := sharkkafka.New(app.Context, options.kafka, app.Logger)
		if err != nil {
			return nil, err
		}
		app.Kafka = kafka
		app.Logger.Info("连接kafka成功", zap.Strings("host", options.kafka.Host), zap.String("user", options.kafka.User))

		if options.kafkalog {
			topic := fmt.Sprintf("%v_game_log_%v", app.Project, app.Env)
			kafkaLogWriter, err := app.Kafka.Writer(topic, &sharkkafka.WriterConfig{
				Async: sharkutils.Pointer(true),
			})
			if err != nil {
				app.Logger.Warn("创建Kafka日志Writer失败，日志仅输出到控制台", zap.Error(err))
			} else {
				app.sharklog.SetKafkaWriter(kafkaLogWriter)
			}
		}
	}

	// 自动模式先尝试集群连接，仅在服务端不支持集群时回退到单机/主从模式。
	if options.redis != nil {
		app.Logger.Info("正在连接redis", zap.Strings("host", options.redis.Host))
		cluster, err := sharkredis.NewCluster(app.Context, options.redis)
		if err == nil {
			app.RedisCluster = cluster
			app.RedisHelper = sharkredis.NewHelperWithCluster(cluster, app.Project, app.Name)
			app.Logger.Info("连接redis cluster成功", zap.Strings("host", options.redis.Host))
		} else {
			errMsg := err.Error()
			if strings.Contains(errMsg, "cluster support disabled") ||
				strings.Contains(errMsg, "CLUSTERDOWN") ||
				strings.Contains(errMsg, "ERR This instance has cluster support disabled") {
				client, err := sharkredis.NewClient(app.Context, options.redis)
				if err != nil {
					app.Logger.Error("连接redis client失败", zap.Strings("host", options.redis.Host), zap.Error(err))
					return nil, err
				}
				app.RedisClient = client
				app.RedisHelper = sharkredis.NewHelperWithClient(client, app.Project, app.Name)
				app.Logger.Info("连接redis client成功", zap.Strings("host", options.redis.Host))
			} else {
				app.Logger.Error("连接redis cluster失败", zap.Strings("host", options.redis.Host), zap.Error(err))
				return nil, err
			}
		}
	}

	if options.redis_cluster != nil && app.RedisCluster == nil {
		app.Logger.Info("正在连接redis cluster", zap.Strings("host", options.redis_cluster.Host))
		redis, err := sharkredis.NewCluster(app.Context, options.redis_cluster)
		if err != nil {
			app.Logger.Error("连接redis cluster失败", zap.Strings("host", options.redis_cluster.Host), zap.Error(err))
			return nil, err
		}
		app.Logger.Info("连接redis cluster成功", zap.Strings("host", options.redis_cluster.Host))
		app.RedisCluster = redis
		app.RedisHelper = sharkredis.NewHelperWithCluster(redis, app.Project, app.Name)
	}

	if options.redis_client != nil && app.RedisClient == nil {
		app.Logger.Info("正在连接redis client", zap.Strings("host", options.redis_client.Host))
		redis, err := sharkredis.NewClient(app.Context, options.redis_client)
		if err != nil {
			app.Logger.Error("连接redis client失败", zap.Strings("host", options.redis_client.Host), zap.Error(err))
			return nil, err
		}
		app.Logger.Info("连接redis client成功", zap.Strings("host", options.redis_client.Host))
		app.RedisClient = redis
		app.RedisHelper = sharkredis.NewHelperWithClient(redis, app.Project, app.Name)
	}

	// ---- MySQL 初始化 ----
	if options.db != nil {
		app.Logger.Info("正在连接db", zap.String("host", options.db.Host), zap.String("database", options.db.Database), zap.String("user", options.db.User))
		db, err := sharkdb.NewDb(app.Context, app.Logger, options.db)
		if err != nil {
			app.Logger.Error("连接db失败", zap.String("host", options.db.Host), zap.String("database", options.db.Database), zap.Error(err))
			return nil, err
		}
		app.Logger.Info("连接db成功", zap.String("host", options.db.Host), zap.String("database", options.db.Database), zap.String("user", options.db.User))
		app.Db = db
	}

	// ---- Elasticsearch 初始化 ----
	if options.elastic != nil {
		app.Logger.Info("正在连接elastic", zap.Strings("host", options.elastic.Host), zap.String("user", options.elastic.User))
		elastic, err := sharkelastic.New(app.Context, options.elastic)
		if err != nil {
			app.Logger.Error("连接elastic失败", zap.Strings("host", options.elastic.Host), zap.Error(err))
			return nil, err
		}
		app.Logger.Info("连接elastic成功", zap.Strings("host", options.elastic.Host), zap.String("user", options.elastic.User))
		app.Elastic = elastic
	}

	// ---- RabbitMQ 初始化 ----
	// 用服务名称的 CRC16 哈希值选择 broker 节点。
	if options.rabbitmq != nil {
		app.Logger.Info("正在连接rabbitmq", zap.Strings("host", options.rabbitmq.Host), zap.String("user", options.rabbitmq.User))
		mq, err := sharkrabbitmq.New(app.Context, app.Logger, app.Wg, options.rabbitmq, app.Name, app.Id)
		if err != nil {
			app.Logger.Error("连接rabbitmq失败", zap.Strings("host", options.rabbitmq.Host), zap.Error(err))
			return nil, err
		}
		if len(options.rabbitmq.Host) > 0 {
			index := crc16.Checksum([]byte(app.Name), crc16.IBMTable) % uint16(len(options.rabbitmq.Host))
			app.Logger.Info("连接rabbitmq成功", zap.String("host", options.rabbitmq.Host[index]), zap.String("user", options.rabbitmq.User))
		}
		app.Rabbitmq = mq
	}

	// ---- RisingWave 初始化 ----
	if options.risingwave != nil {
		app.Logger.Info("正在连接risingwave", zap.String("host", options.risingwave.Host), zap.String("database", options.risingwave.Database), zap.String("user", options.risingwave.User))
		rw, err := sharkrisingwave.New(app.Context, app.Logger, options.risingwave)
		if err != nil {
			app.Logger.Error("连接risingwave失败", zap.String("host", options.risingwave.Host), zap.String("database", options.risingwave.Database), zap.Error(err))
			return nil, err
		}
		app.Logger.Info("连接risingwave成功", zap.String("host", options.risingwave.Host), zap.String("database", options.risingwave.Database), zap.String("user", options.risingwave.User))
		app.RisingWave = rw
	}

	// ---- etcd 初始化 ----
	if options.etcd != nil {
		app.Logger.Info("正在连接etcd", zap.Strings("host", options.etcd.Host), zap.String("user", options.etcd.User))
		etcd, err := sharketcd.New(app.Context, options.etcd)
		if err != nil {
			app.Logger.Error("连接etcd失败", zap.Strings("host", options.etcd.Host), zap.Error(err))
			return nil, err
		}
		app.Logger.Info("连接etcd成功", zap.Strings("host", options.etcd.Host), zap.String("user", options.etcd.User))
		app.Etcd = etcd
	}

	// ---- MongoDB 初始化 ----
	if options.mongodb != nil {
		app.Logger.Info("正在连接mongodb", zap.String("host", options.mongodb.Host), zap.String("user", options.mongodb.User))
		mongodb, err := sharkmongodb.New(app.Context, options.mongodb)
		if err != nil {
			app.Logger.Error("连接mongodb失败", zap.String("host", options.mongodb.Host), zap.Error(err))
			return nil, err
		}
		app.Logger.Info("连接mongodb成功", zap.String("host", options.mongodb.Host), zap.String("user", options.mongodb.User))
		app.Mongodb = mongodb
	}

	// ---- MinIO 初始化 ----
	if options.minio != nil {
		app.Logger.Info("正在连接minio", zap.String("host", options.minio.Host), zap.String("user", options.minio.User))
		client, err := sharkminio.New(app.Context, options.minio)
		if err != nil {
			app.Logger.Error("连接minio失败", zap.String("host", options.minio.Host), zap.Error(err))
			return nil, err
		}
		app.Logger.Info("连接minio成功", zap.String("host", options.minio.Host), zap.String("user", options.minio.User))
		app.Minio = client
	}

	// ---- ClickHouse 初始化 ----
	if options.clickhouse != nil {
		app.Logger.Info("正在连接clickhouse", zap.Strings("host", options.clickhouse.Host), zap.String("database", options.clickhouse.Database), zap.String("user", options.clickhouse.User))
		client, err := sharkclickhouse.New(app.Context, options.clickhouse)
		if err != nil {
			app.Logger.Error("连接clickhouse失败", zap.Strings("host", options.clickhouse.Host), zap.Error(err))
			return nil, err
		}
		app.ClickHouse = client
		app.Logger.Info("连接clickhouse成功", zap.Strings("host", options.clickhouse.Host), zap.String("database", options.clickhouse.Database))
	}

	if options.timer {
		if app.RedisCluster != nil {
			app.Timer = sharktimer.NewTimer(app.Context, app.Project, app.Name, app.Id, app.RedisCluster, app.Logger)
			app.Logger.Info("初始化timer成功")
		} else if app.RedisClient != nil {
			app.Timer = sharktimer.NewTimer(app.Context, app.Project, app.Name, app.Id, app.RedisClient, app.Logger)
			app.Logger.Info("初始化timer成功")
		} else {
			app.Logger.Error("初始化timer失败, 依赖redis, 请确保已正确配置redis连接")
		}
	}

	if options.grpc > 0 {
		if app.RedisCluster != nil {
			server := sharkgrpc.New(app.Context, app.Project, app.RedisCluster, app.Logger, options.grpc)
			app.Logger.Info("开启rpc服务", zap.Int("port", options.grpc))
			app.Grpc = server
		} else if app.RedisClient != nil {
			server := sharkgrpc.New(app.Context, app.Project, app.RedisClient, app.Logger, options.grpc)
			app.Logger.Info("开启rpc服务", zap.Int("port", options.grpc))
			app.Grpc = server
		} else {
			app.Logger.Error("开启rpc服务失败, 依赖redis, 请确保已正确配置redis连接")
		}
	} else {
		if app.RedisCluster != nil {
			server := sharkgrpc.New(app.Context, app.Project, app.RedisCluster, app.Logger, -1)
			app.Grpc = server
		} else if app.RedisClient != nil {
			server := sharkgrpc.New(app.Context, app.Project, app.RedisClient, app.Logger, -1)
			app.Grpc = server
		}
	}

	if options.http > 0 {
		app.GinEngine = sharkhttp.New(app.Context, app.Env, app.Logger, options.http)
		app.Ws = sharkws.NewSharkWS(app.GinEngine)
		app.Logger.Info("开启http服务", zap.Int("port", options.http))
		if options.env == "dev" {
			app.Logger.Debug("swagger url: http://127.0.0.1" + ":" + fmt.Sprint(options.http) + "/swagger/index.html")
		}
	}

	if options.pprof > 0 {
		go app.pprof(options.pprof)
	}
	if options.health > 0 {
		go app.health_service(options.health)
	}

	return app, nil
}

// pprof 在指定端口启动性能分析服务。
func (a *App) pprof(port int) {
	a.Logger.Info("开启pprof服务", zap.Int("port", port))
	a.muxServe = http.NewServeMux()
	a.muxServe.HandleFunc("/debug/pprof/", pprof.Index)
	err := http.ListenAndServe(fmt.Sprintf(":%v", port), a.muxServe)
	if err != nil {
		a.Logger.Error("pprof服务启动失败", zap.Int("port", port), zap.Error(err))
	}
}

// health_service 在指定端口提供进程存活探测，不检查中间件状态。
func (a *App) health_service(port int) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(200)
		writer.Write([]byte(`{"status": "ok"}`))
	})
	a.Logger.Info("开启存活探测服务", zap.Int("port", port))
	err := http.ListenAndServe(fmt.Sprintf(":%v", port), mux)
	if err != nil {
		a.Logger.Error("存活探测服务启动失败", zap.Int("port", port), zap.Error(err))
	}
}

// AppComponent 是可由 Hunt 启动的应用组件。
type AppComponent interface {
	// Start 启动组件。
	Start()
}

// banner 记录服务启动信息。
func (a *App) banner() {
	hostName, _ := os.Hostname()
	a.Logger.Info("shark running", zap.String("name", a.Name), zap.String("id", a.Id), zap.String("env", a.Env), zap.String("host", hostName))
	a.Logger.Info("****************server start****************")
}

// Hunt 启动组件并阻塞至收到退出信号，然后取消应用 context 并等待已登记的 goroutine。
func (a *App) Hunt(components ...AppComponent) {
	for _, c := range components {
		c.Start()
	}
	if a.Grpc != nil {
		a.Grpc.Run()
	}
	a.banner()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig

	a.cancelFunc()
	time.Sleep(time.Millisecond * 500)
	a.Wg.Wait()
	if a.ClickHouse != nil {
		if err := a.ClickHouse.Close(); err != nil {
			a.Logger.Error("关闭clickhouse失败", zap.Error(err))
		}
	}
	a.Logger.Debug("****************server exit****************")
	a.sharklog.Close()
}

func (a *App) RunInPool(fn func(args ...any), args ...any) {
	if fn == nil {
		a.Logger.Error("RunInPool nil function provided")
		return
	}
	if a.AntPool == nil {
		a.Logger.Error("RunInPool AntPool is nil")
		return
	}
	a.AntPool.Submit(func() {
		defer func() {
			if r := recover(); r != nil {
				if a.Logger != nil {
					a.Logger.Error("RunInPool panic in go routine", zap.Any("panic", r), zap.String("stack", string(debug.Stack())))
				}
			}
		}()
		fn(args...)
	})
}

// WithTimeout 在指定时间内执行 fn；超时或 parent 被取消时返回 ErrTimeout。
func (a *App) WithTimeout(ctx context.Context, timeout time.Duration, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.AntPool == nil {
		return errors.New("WithTimeout AntPool is nil")
	}
	ctxinner, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ch := make(chan error, 1)

	err := a.AntPool.Submit(func() {
		var err error
		defer func() {
			if r := recover(); r != nil {
				select {
				case ch <- fmt.Errorf("panic: %v\n%s", r, debug.Stack()):
				default:
				}
				return
			}
			select {
			case ch <- err:
			default:
			}
		}()
		err = fn(ctxinner)
	})
	if err != nil {
		return err
	}
	select {
	case err := <-ch:
		return err
	case <-ctxinner.Done():
		select {
		case err := <-ch:
			return err
		default:
			if errors.Is(ctxinner.Err(), context.DeadlineExceeded) {
				return errors.New("timeout")
			}
			return ctxinner.Err()
		}
	}
}

// ParallelCall 并行执行函数，等待全部完成并合并返回的错误和 panic。
func (a *App) ParallelCall(funcs ...func() error) error {
	if a.AntPool == nil {
		return errors.New("AntPool is nil")
	}
	var wg sync.WaitGroup
	ch := make(chan error, len(funcs))
	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		f := fn
		wg.Add(1)
		a.AntPool.Submit(func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					ch <- fmt.Errorf("panic: %v\n%s", r, debug.Stack())
				}
			}()
			err := f()
			if err != nil {
				ch <- err
			}
		})
	}
	wg.Wait()
	close(ch)
	errs := make([]error, 0, len(funcs))
	for err := range ch {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (a *App) SafeGo(fn func(args ...any)) {
	if fn == nil {
		a.Logger.Error("RunInPool nil function provided")
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				if a.Logger != nil {
					a.Logger.Error("SafeGo panic in go routine", zap.Any("panic", r), zap.String("stack", string(debug.Stack())))
				}
			}
		}()
		fn()
	}()
}
