// Package sharkapp 是 Shark 框架的核心应用层，提供了应用生命周期管理、
// 中间件初始化、服务启动与优雅关闭等功能。
//
// Shark 是一个一站式后端微服务框架，统一封装了 MySQL、Redis、Kafka、
// Elasticsearch、MongoDB、RabbitMQ、MinIO、etcd、RisingWave 等常用中间件的
// 连接管理和配置加载，让开发者可以专注于业务逻辑。
//
// 典型用法:
//
//	// 方式一：从 YAML 配置文件加载
//	options, _ := sharkapp.NewOption("myproject", "myapp")
//	app, _ := sharkapp.New(options)
//	app.Hunt()
//
//	// 方式二：在代码中指定配置
//	options, _ := sharkapp.NewOption("myproject", "myapp")
//	options.WithDB(&sharkdb.Config{...}).WithRedis(&sharkredis.Config{...})
//	app, _ := sharkapp.New(options)
//	app.Hunt()
package sharkapp

import (
	"context"
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

	"github.com/gin-gonic/gin"
	"github.com/howeyc/crc16"
	"github.com/lornshark/shark/sharkdb"
	"github.com/lornshark/shark/sharkelastic"
	"github.com/lornshark/shark/sharketcd"
	"github.com/lornshark/shark/sharkfunc"
	"github.com/lornshark/shark/sharkgrpc"
	"github.com/lornshark/shark/sharkhttp"
	"github.com/lornshark/shark/sharkkafka"
	"github.com/lornshark/shark/sharkminio"
	"github.com/lornshark/shark/sharkmongodb"
	"github.com/lornshark/shark/sharkrabbitmq"
	"github.com/lornshark/shark/sharkredis"
	"github.com/lornshark/shark/sharkrisingwave"
	"github.com/lornshark/shark/sharkws"

	"github.com/lornshark/shark/sharklog"
	"github.com/lornshark/shark/sharktimer"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/minio/minio-go/v7"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// App 是 Shark 框架的核心结构体，聚合了所有中间件客户端和服务组件。
//
// App 负责：
//   - 管理应用的生命周期（context、WaitGroup、取消信号）
//   - 统一初始化所有中间件连接（数据库、缓存、消息队列等）
//   - 提供 HTTP/gRPC 服务的启动和优雅关闭
//   - 内建 pprof 性能分析和健康检查端点
//
// 字段分类:
//   - 生命周期: Wg（等待所有 goroutine 退出）、Sg（防缓存击穿）、Context（全局上下文）
//   - 基础信息: Id、Env、Name、Project（用于日志、分布式锁等场景的命名空间隔离）
//   - 基础组件: 各中间件的客户端实例，按需初始化
//   - 内部组件: cancelFunc（取消信号）、sharklog（日志组件）、muxServe（pprof HTTP 路由）
type App struct {
	// --- 生命周期管理 ---
	// Wg 用于等待所有后台 goroutine 完成后再退出
	Wg *sync.WaitGroup
	// Sg singleflight 组，防止并发场景下的缓存击穿（多个请求同时查询同一 key 时只执行一次）
	Sg *singleflight.Group
	// Context 应用全局 context，取消时通知所有组件退出
	Context context.Context

	// --- 基础信息 ---
	// Id 实例编号，用于多节点部署时区分不同实例
	Id string
	// Env 运行环境：dev / test / prod
	Env string
	// Name 服务名称
	Name string
	// Project 项目名称，用于日志 topic、定时器 key 等全局命名
	Project string

	// --- 基础组件（按需初始化，nil 表示未启用） ---
	// Db MySQL/GORM 数据库连接
	Db *gorm.DB
	// Grpc gRPC 服务端实例
	Grpc *sharkgrpc.RpcServer
	// Minio MinIO 对象存储客户端
	Minio *minio.Client
	// Timer 定时器组件（基于 Redis 分布式锁）
	Timer *sharktimer.Timer
	// Kafka Kafka 消息队列客户端
	Kafka *sharkkafka.SharkKafka
	// RedisCluster Redis 集群模式客户端
	RedisCluster *redis.ClusterClient
	// RedisClient Redis 单机/主从模式客户端
	RedisClient *redis.Client
	RedisHelper *sharkredis.Helper
	// Logger 结构化日志记录器（基于 zap）
	Logger *zap.Logger
	// Elastic Elasticsearch 客户端
	Elastic *sharkelastic.SharkElastic
	// Etcd etcd 客户端（分布式配置/服务发现）
	Etcd *clientv3.Client
	// Mongodb MongoDB 客户端
	Mongodb *mongo.Client
	// Rabbitmq RabbitMQ 客户端
	Rabbitmq *sharkrabbitmq.Client
	// RisingWave RisingWave 流数据库连接（复用 GORM）
	RisingWave *gorm.DB
	// GinEngine Gin HTTP 引擎（路由注册入口）
	GinEngine *gin.Engine
	// SharkWS WebSocket 服务组件
	Ws *sharkws.SharkWS

	// --- 内部组件（不对外暴露） ---
	// cancelFunc 取消 Context，触发优雅关闭
	cancelFunc context.CancelFunc
	// sharklog Shark 日志组件封装
	sharklog *sharklog.SharkLog
	// muxServe pprof 服务的 HTTP 路由复用器
	muxServe *http.ServeMux
}

// New 根据 Options 创建 App 实例，并初始化所有已配置的中间件连接。
//
// 初始化顺序（按代码顺序）:
//  1. 创建 context 和 WaitGroup
//  2. 初始化日志组件
//  3. 连接 Kafka（dev/test 环境自动配置日志写入 Kafka）
//  4. 连接 Redis（自动探测 cluster/client 模式）
//  5. 连接 MySQL
//  6. 连接 Elasticsearch
//  7. 连接 RabbitMQ（使用 CRC16 哈希选择 broker 节点）
//  8. 连接 RisingWave
//  9. 连接 etcd
//  10. 连接 MongoDB
//  11. 连接 MinIO
//  12. 初始化定时器（依赖 Redis）
//  13. 初始化 gRPC 服务（依赖 Redis）
//  14. 初始化 HTTP 服务（基于 Gin）
//  15. 按需启动 pprof、健康检查服务
//
// 参数:
//   - options: 应用配置，由 NewOption 或 NewOptionWithRedis 创建
//
// 返回值:
//   - *App: 初始化完成的应用实例
//   - error: 任一中件间连接失败时返回错误
func New(options *Options) (*App, error) {
	if options == nil {
		return nil, fmt.Errorf("options required")
	}

	// 创建带取消功能的 context，用于全局生命周期控制
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

	// 初始化日志组件
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

		// dev/test 环境自动将日志写入 Kafka，方便开发调试
		// 生产环境由运维统一收集日志，不需要应用层自行写入
		if app.Env == "dev" || app.Env == "test" {
			topic := fmt.Sprintf("%v_game_log_%v", app.Project, app.Env)
			kafkaLogWriter, err := app.Kafka.Writer(topic, &sharkkafka.WriterConfig{
				Async: sharkfunc.Pointer(true), // 异步写入 Kafka，避免阻塞业务日志
			})
			if err != nil {
				app.Logger.Warn("创建Kafka日志Writer失败，日志仅输出到控制台", zap.Error(err))
			} else {
				app.sharklog.SetKafkaWriter(kafkaLogWriter)
			}
		}
	}

	// ---- Redis 初始化（自动探测 cluster/client 模式） ----
	// 策略：先尝试以集群模式连接，如果服务端返回"cluster support disabled"，
	// 则回退到单机/主从模式。这种方式兼容了不同部署形态的 Redis。
	if options.redis != nil {
		app.Logger.Info("正在连接redis", zap.Strings("host", options.redis.Host))
		cluster, err := sharkredis.NewCluster(app.Context, options.redis)
		if err == nil {
			app.RedisCluster = cluster
			app.RedisHelper = sharkredis.NewHelperWithCluster(cluster)
			app.Logger.Info("连接redis cluster成功", zap.Strings("host", options.redis.Host))
		} else {
			errMsg := err.Error()
			if strings.Contains(errMsg, "cluster support disabled") ||
				strings.Contains(errMsg, "CLUSTERDOWN") ||
				strings.Contains(errMsg, "ERR This instance has cluster support disabled") {
				// 不是集群模式，回退到单机/主从模式
				client, err := sharkredis.NewClient(app.Context, options.redis)
				if err != nil {
					app.Logger.Error("连接redis client失败", zap.Strings("host", options.redis.Host), zap.Error(err))
					return nil, err
				}
				app.RedisClient = client
				app.RedisHelper = sharkredis.NewHelperWithClient(client)
				app.Logger.Info("连接redis client成功", zap.Strings("host", options.redis.Host))
			} else {
				// 集群模式连接失败（非"不支持集群"的原因，如网络不通、密码错误等）
				app.Logger.Error("连接redis cluster失败", zap.Strings("host", options.redis.Host), zap.Error(err))
				return nil, err
			}
		}
	}

	// ---- Redis Cluster 明确配置（独立于自动探测） ----
	if options.redis_cluster != nil && app.RedisCluster == nil {
		app.Logger.Info("正在连接redis cluster", zap.Strings("host", options.redis_cluster.Host))
		redis, err := sharkredis.NewCluster(app.Context, options.redis_cluster)
		if err != nil {
			app.Logger.Error("连接redis cluster失败", zap.Strings("host", options.redis_cluster.Host), zap.Error(err))
			return nil, err
		}
		app.Logger.Info("连接redis cluster成功", zap.Strings("host", options.redis_cluster.Host))
		app.RedisCluster = redis
		app.RedisHelper = sharkredis.NewHelperWithCluster(redis)
	}

	// ---- Redis Client 明确配置（独立于自动探测） ----
	if options.redis_client != nil && app.RedisClient == nil {
		app.Logger.Info("正在连接redis client", zap.Strings("host", options.redis_client.Host))
		redis, err := sharkredis.NewClient(app.Context, options.redis_client)
		if err != nil {
			app.Logger.Error("连接redis client失败", zap.Strings("host", options.redis_client.Host), zap.Error(err))
			return nil, err
		}
		app.Logger.Info("连接redis client成功", zap.Strings("host", options.redis_client.Host))
		app.RedisClient = redis
		app.RedisHelper = sharkredis.NewHelperWithClient(redis)
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
	// 使用服务名称的 CRC16 哈希值来选择连接的 broker 节点，
	// 实现多节点间的负载均衡分布
	if options.rabbitmq != nil {
		app.Logger.Info("正在连接rabbitmq", zap.Strings("host", options.rabbitmq.Host), zap.String("user", options.rabbitmq.User))
		mq, err := sharkrabbitmq.New(app.Context, app.Logger, app.Wg, options.rabbitmq, app.Name, app.Id)
		if err != nil {
			app.Logger.Error("连接rabbitmq失败", zap.Strings("host", options.rabbitmq.Host), zap.Error(err))
			return nil, err
		}
		// CRC16 哈希取模选择 broker 节点
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

	// ---- 定时器初始化（依赖 Redis） ----
	if options.timer {
		if app.RedisCluster != nil {
			app.Timer = sharktimer.NewTimer(app.Context, app.Project, app.Name, app.Id, app.RedisCluster)
			app.Logger.Info("初始化timer成功")
		} else if app.RedisClient != nil {
			app.Timer = sharktimer.NewTimer(app.Context, app.Project, app.Name, app.Id, app.RedisClient)
			app.Logger.Info("初始化timer成功")
		} else {
			app.Logger.Error("初始化timer失败, 依赖redis, 请确保已正确配置redis连接")
		}
	}

	// ---- gRPC 服务初始化（依赖 Redis） ----
	if options.grpc > 0 {
		// 端口 > 0：启动 gRPC 监听
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
		// 端口 <= 0：初始化 gRPC 但不启动监听（仅做服务发现注册）
		if app.RedisCluster != nil {
			server := sharkgrpc.New(app.Context, app.Project, app.RedisCluster, app.Logger, -1)
			app.Grpc = server
		} else if app.RedisClient != nil {
			server := sharkgrpc.New(app.Context, app.Project, app.RedisClient, app.Logger, -1)
			app.Grpc = server
		}
	}

	// ---- HTTP 服务初始化（基于 Gin） ----
	if options.http > 0 {
		app.GinEngine = sharkhttp.New(app.Context, app.Env, app.Logger, options.http)
		app.Ws = sharkws.NewSharkWS(app.GinEngine)
		app.Logger.Info("开启http服务", zap.Int("port", options.http))
		// 开发环境提示 Swagger 文档地址
		if options.env == "dev" {
			app.Logger.Debug("swagger url: http://127.0.0.1" + ":" + fmt.Sprint(options.http) + "/swagger/index.html")
		}
	}

	// ---- 辅助服务（非核心，异步启动） ----
	if options.pprof > 0 {
		go app.pprof(options.pprof)
	}
	if options.health > 0 {
		go app.health_service(options.health)
	}

	return app, nil
}

// pprof 启动 Go 性能分析 HTTP 服务。
//
// 提供标准的 Go pprof 端点：
//   - /debug/pprof/ — 索引页
//   - /debug/pprof/goroutine — goroutine 堆栈
//   - /debug/pprof/heap — 堆内存分析
//   - /debug/pprof/profile — CPU 分析（默认 30s）
//
// 参数:
//   - port: 监听端口
func (a *App) pprof(port int) {
	a.Logger.Info("开启pprof服务", zap.Int("port", port))
	a.muxServe = http.NewServeMux()
	// 注册 pprof 路由（使用标准库的 pprof.Index）
	a.muxServe.HandleFunc("/debug/pprof/", pprof.Index)
	err := http.ListenAndServe(fmt.Sprintf(":%v", port), a.muxServe)
	if err != nil {
		a.Logger.Error("pprof服务启动失败", zap.Int("port", port), zap.Error(err))
	}
}

// health_service 启动健康检查 HTTP 服务。
//
// 提供 /health 端点，返回 {"status": "ok"}，
// 用于 Kubernetes 的 liveness/readiness probe 或负载均衡器的健康检测。
//
// 参数:
//   - port: 监听端口
func (a *App) health_service(port int) {
	http.HandleFunc("/health", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(200)
		writer.Write([]byte(`{"status": "ok"}`))
	})
	a.Logger.Info("开启健康检查服务", zap.Int("port", port))
	err := http.ListenAndServe(fmt.Sprintf(":%v", port), nil)
	if err != nil {
		a.Logger.Error("健康检查服务启动失败", zap.Int("port", port), zap.Error(err))
	}
}

// AppComponent 定义了应用组件的启动接口。
//
// 实现了此接口的类型可以在 Hunt 启动时被调用 Start()
// 来初始化自己的后台任务（如消费 Kafka 消息、启动定时任务等）。
type AppComponent interface {
	// Start 启动组件的后台任务，通常以阻塞方式运行
	Start()
}

// Hunt 启动所有组件并进入主循环，等待退出信号后优雅关闭。
//
// 执行流程:
//  1. 等待 100ms（确保所有 goroutine 都准备好）
//  2. 依次启动所有传入的 AppComponent
//  3. 打印启动 banner
//  4. 阻塞等待 SIGTERM 或 SIGINT 信号
//  5. 收到信号后取消 context，等待 500ms 让正在处理的请求完成
//  6. 等待所有 WaitGroup 中的 goroutine 退出
//  7. 打印退出日志
//
// 参数:
//   - components: 需要在启动时初始化的应用组件列表
func (a *App) Hunt(components ...AppComponent) {
	// 依次启动所有组件
	for _, c := range components {
		c.Start()
	}
	if a.Grpc != nil {
		a.Grpc.Run()
	}
	// 打印启动横幅
	a.banner()

	// 监听系统信号（SIGTERM 来自 kill 命令，SIGINT 来自 Ctrl+C）
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	// 阻塞等待信号
	<-sig

	// 收到退出信号后，取消 context 通知所有组件退出
	a.cancelFunc()
	// 等待 500ms 让正在处理的请求完成
	time.Sleep(time.Millisecond * 500)
	// 等待所有注册的 goroutine 完成
	a.Wg.Wait()
	a.Logger.Debug("****************server exit****************")
	// 优雅关闭日志组件（Kafka Writer）
	a.sharklog.Close()
}

// banner 打印服务启动日志。
// 注释掉的 ASCII Art 是一个 "SHARK HUNTING" 的横幅，
// 保留以便后续需要时启用。
func (a *App) banner() {
	hostName, _ := os.Hostname()
	a.Logger.Info("shark running", zap.String("name", a.Name), zap.String("id", a.Id), zap.String("env", a.Env), zap.String("host", hostName))
	a.Logger.Info("****************server start****************")
}

// Go 安全地启动一个后台 goroutine，自动捕获 panic 并记录日志。
//
// 与直接使用 go 关键字不同，此方法会：
//   - 自动在 defer 中 recover panic，防止单个 goroutine 崩溃导致整个程序退出
//   - 将 panic 信息和完整调用栈记录到日志中
//   - 自动传入 app.Context 作为函数参数
//
// 参数:
//   - fn: 需要在后台 goroutine 中执行的函数，接收 app.Context 作为参数
//
// 使用示例:
//
//	app.Go(func(ctx context.Context) {
//	    // 后台任务，panic 不会导致程序崩溃
//	})
func (a *App) Go(fn func(ctx context.Context)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// 记录 panic 信息和完整调用栈
				a.Logger.Error("panic in go routine", zap.Any("panic", r), zap.String("stack", string(debug.Stack())))
			}
		}()
		fn(a.Context)
	}()
}
