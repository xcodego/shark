# Shark

一个轻量级、模块化的 Go 微服务开发框架，内置了十余种基础设施组件的连接管理和实用工具。

## 项目简介

Shark 封装了微服务开发中常见的中间件和工具库，提供统一的配置加载（`config.yaml`）、连接管理和常用工具。各子包均可独立使用，无需依赖完整的应用。

**模块路径：** `github.com/xcodego/shark`

**Go 版本要求：** 1.26+

## 特性

- **统一配置** — `config.yaml` 加载全部中间件，结构体带 json/yaml/mapstructure 标签；环境变量优先于文件
- **双通道日志** — 控制台（Console Encoder）与 Kafka（JSON Encoder）经 `zapcore.NewTee` 合并；设置 Kafka Writer 后，每条 Kafka 日志附带 Snowflake `log_id`
- **gRPC 服务发现** — 服务端只监听，不向 Redis 注册自身；客户端读取外部写入的地址列表，round_robin 负载均衡，指数退避重试
- **Snowflake ID** — 距纪元的秒数（高位）加 19 位序列号，每秒最多 52 万个 int64；故意没有 worker id，序列用尽时持锁等到下一秒
- **SQL 条件构建** — 流式 API 构建参数化 WHERE / JOIN ON，支持 AND/OR 嵌套和字段对字段比较；nil、空切片、空 map 会跳过，0 和空字符串不会
- **Keyset 游标分页** — 泛型 `TableScan`，深分页成本不随偏移增大，支持 Next/Prev 和 Excel/CSV 流式导出
- **高精度数值** — 基于 shopspring/decimal，先在更高精度上四舍五入再截断到指定位数
- **批量消费** — Kafka 与 RabbitMQ 批量拉取；Kafka 在处理函数返回 true 后提交 offset（至少一次）
- **定时器** — Redis ZSet 单机定时器，约 1 秒轮询一次，回调放在容量为 10 的 ants 池里执行
- **并发工具** — `App` 上的 `WithTimeout`、`ParallelCall`、`RunInPool`、`SafeGo`；`sharkutils` 提供 `DrainChannelN`、`Pointer`
- **多层缓存** — singleflight 合并同一 key，seeker 链按顺序回源；存储错误立即返回，nil / `ErrNotFound` 才尝试下一层
- **RBAC 权限树** — 多叉树，支持归一化、裁剪、父子同步，以及 Flatten / Permissions 查表

## 快速开始

### 安装

```bash
go get github.com/xcodego/shark
```

### Go 源码检查

`sharklint` 可独立检查指定目录，不依赖 Shark 应用初始化：

```bash
go install github.com/xcodego/shark/sharklint/cmd/sharklint@latest
sharklint /path/to/project
```

检查器输出违规的文件、行号和列号。发现违规或检查错误时退出码为 1，参数错误时为 2。

`sharklint/go/` 下是通用 Go 语言规则：

- `any`：递归扫描所有非测试 `.go` 文件（包括 vendor 和不满足当前构建条件的文件），不跟随符号链接，禁止使用 `interface{}`，请改用 `any`。
- `beego`：禁止导入 Beego 框架包，包括 `github.com/beego/beego` 和 `github.com/astaxie/beego` 的所有版本及子包。
- `format`：递归检查所有 `.go` 文件（包括测试文件和被构建标签排除的文件），不跟随符号链接，文件必须与 `gofmt` 输出一致。
- `vet`：在目标目录运行 `go vet -unreachable=false ./...`，检查当前构建条件下的 Go 包；目录没有 Go 文件时跳过。unreachable 由单独的检查器负责。
- `goroutine`：递归扫描所有非测试 `.go` 文件（包括 vendor 和不满足当前构建条件的文件），不跟随符号链接，禁止直接使用 `go` 启动协程。
- `panic`：递归扫描所有非测试 `.go` 文件，不跟随符号链接，禁止直接调用内置 `panic`（`package main` 的入口函数 `main` 除外）。
- `maprace`：通过类型信息检查当前构建条件下的非测试 Go 包，检查直接 `go func(...)` 和 `sharkapp.App.SafeGo` 的函数字面量捕获的 map 的索引读写、`delete`、`clear`、`len` 和遍历；也比较同一函数中启动后的访问和其他异步闭包。单纯的并发只读不报告；共享 map 的异步写入若未持有 `sync.Mutex` 或 `sync.RWMutex` 的写锁会报告，互相冲突的访问须持有同一个锁（读操作允许读锁）。控制流分支合并时仅认可所有路径均持有的锁。检查采用保守近似，异步写入即使实际只执行一次也可能报告；不识别同步等待、channel、跨函数调用、map 或锁的别名、闭包参数传入的 map、间接调用、其他协程池和自定义锁，不能替代 `go test -race`。仅分析函数声明内的直接异步闭包，不追踪嵌套函数、复杂 map 表达式或 map 重新赋值，启动位置之前的访问不考虑循环下一次迭代；需要 Go 模块或工作区，不检查其他构建条件、vendor 和嵌套模块。
- `syncpair`：通过类型信息和函数内控制流检查 `sync.Mutex` / `sync.RWMutex` 的 `Lock`、`Unlock` 和 `RLock`、`RUnlock` 配对，及局部 `sync.WaitGroup` 的常量 `Add`、`Done` 配对；检查返回或显式 panic 路径，并识别直接 `defer` 释放。WaitGroup 支持直接 `go func` 和 `sharkapp.App.SafeGo` 闭包捕获的 `Done` 或负数 `Add`，检查闭包分支是否遗漏完成操作，以及 `Wait` 前是否存在未完成的计数。检查为近似分析，不推断分支条件之间的关系，可能误报；计数上限为 64。WaitGroup 被传给其他函数、被其他回调捕获、使用动态或超出 ±64 的 Add，或在工作闭包中增加计数时不检查该对象；不追踪别名、重新赋值、闭包参数、跨函数完成、嵌套协程、自定义协程池或 defer 闭包。使用 `TryLock` / `TryRLock` 的锁不检查；锁通过参数或字段在函数间转移、间接调用和复杂接收者也无法可靠分析；仅在函数内有获取操作或锁为局部变量时报告多余释放。需要 Go 模块或工作区，不检查其他构建条件、vendor 和嵌套模块。
- `logging`：禁止使用 `fmt.Print*`、导入 `log`/`log/slog` 或使用 zap 全局 Logger；业务代码应使用注入的 `*zap.Logger`。只有 `sharklog.New` 可创建 Logger；`sharkdb/binloglistener.go` 中为 go-mysql 提供的 `log/slog` 适配除外，检查器命令的格式化输出不作为日志处理。
- `unreachable`：运行 `go vet -unreachable=true`，只启用 unreachable 分析器。
- `swagger`：仅检查 Gin 和 `net/http` 的 POST 路由是否关联 Swagger handler 文档，校验 `@Router` 路径和方法、路径参数、请求体绑定类型、`@Success` 类型，并要求请求/响应结构体的具名字段有注释；嵌入结构体成员本身可不写注释，但会递归检查其内部字段。可识别 Gin `Group` 前缀及字符串常量路由；成功响应类型比对覆盖 handler 中直接调用 Gin JSON 响应的方法，Shark 的 `sharkerror.Error` 可作为 HTTP 200 错误响应与成功响应并存。动态路由会报告无法静态校验；间接响应和外部依赖结构体字段无法验证。

`redis` 规则单独位于 `sharklint/redis/`：

- 通过类型信息检查当前构建条件下的非测试 Go 包，禁止 `KEYS`、`FLUSHALL`、`FLUSHDB`、`CONFIG`、`DEBUG`、`SHUTDOWN`、`SAVE`、`BGSAVE`、`BGREWRITEAOF`、`REPLICAOF`、`SLAVEOF`、`SWAPDB`、`MIGRATE`、`MONITOR`、`CLIENT KILL` 和 `CLIENT PAUSE`；`KEYS` 建议使用 `SCAN` 替代。支持 go-redis（v6、v7、v8、v9）和 rueidiscompat 的命令方法、`Do`、`DoRaw`、`DoRawWriteTo` 及 go-redis 的 `New*Cmd`，Redigo 的 `Do`、`Send`、`DoContext` 和 `DoWithTimeout`，以及 Rueidis 的命令构造器和 `Builder.Arbitrary`。原生命令的命令名及 `CLIENT` 子命令须为字符串常量；动态生成的命令名、重新声明的自定义接口方法和间接函数调用无法静态识别。此检查依赖 Go 包加载，目录需属于 Go 模块或工作区；不检查其他构建条件、vendor 和嵌套模块。
- Redis key 禁止直接使用字符串字面量、字符串拼接、类型转换以及 `fmt.Sprint*`、`strings` 或 `strconv` 的字符串构造结果。检查同一函数内的局部赋值、分支和循环，追踪切片和 map 别名、元素修改及 `append`；分支合并和无法确定的切片下标采用保守分析。函数参数、包级常量及自定义函数返回值允许使用，不检查自定义函数内部的数据流。支持上述客户端的常见 key 参数、部分结构体 key 字段及常见原生命令；`Do`、`DoRaw`、`DoRawWriteTo`、`Send`、`DoContext` 和 `DoWithTimeout` 支持局部命令名和局部切片展开。`EVAL` / `FCALL` 等命令按照 key 数量判断参数；数量无法确定时，数量之后的参数会保守视为可能的 key。无法确定的命令、未知原生命令、间接调用、跨函数数据流、结构体修改及通过函数修改的容器不在检查范围。

`sql`：通过类型信息检查当前构建条件下的非测试 Go 包。禁止 `database/sql` 的查询、执行及预编译和 GORM 的 `Raw`、`Exec`；GORM 条件、连接和 `Expr` 禁止手写或拼接 SQL，使用 `sharksql` 构建器。GORM 表名、字段名、条件及创建和更新 map 的键以及 `sharksql` 字段名、表名和别名须直接使用 `app/gconst` 包中的常量（import 路径为 `app/gconst` 或以 `/app/gconst` 结尾）；支持使用构建器表达式、常量键 map 和字段列表。map 变量须在同一文件中由字面量初始化，所有索引赋值的键均须为常量；重新赋值或赋予别名的 map 不予认可。`sharksql.Case`、`CaseAs`、`When`、`WhenAs` 接受原始 SQL 表达式，因此也禁止使用。检查需在 Go 模块或工作区中运行，不检查其他构建条件、vendor、嵌套模块、间接函数调用、自定义数据库接口及其他数据库库。结构体标签、通过函数调用或其他间接方式修改的 map 内容及跨函数数据流不在检查范围；未知字符串条件会被拒绝，构建器结果保存到局部变量时只接受没有其他赋值的变量。

事务检查支持 `database/sql` 的 `Begin`、`BeginTx` 和 GORM 的 `Begin`、直接传入函数字面量的 `Transaction`。对手动开启的事务，检查成功路径是否注册 `defer tx.Rollback()`，以及返回或 panic 路径是否提交或回滚；无条件显式回滚也不能替代 defer 要求。事务期间检查嵌套开启事务、gRPC 客户端方法和 `net/http` 客户端请求、`time.Sleep` 和阻塞的 `time.After`/`Timer.C`/`Ticker.C` 接收、未检查错误的数据修改，以及直接丢弃查询结果（GORM 查询仅在目标为 `nil` 且返回值被丢弃时报告）。等待和查询检查仅覆盖上述可识别形式，不能判断任意操作的执行时间或业务上是否需要查询。

事务进行期间，禁止通过非事务连接执行数据库操作：检查 `database/sql` 的 `DB`、`Conn` 及由它们预编译的局部 `Stmt`，以及 GORM 的查询、写入和迁移操作。GORM 连接检查在函数内追踪事务连接的局部别名和链式调用，并保守合并分支；事务结束后不再检查。`database/sql` 的事务 `Stmt` 可通过 `tx.Prepare` 或 `tx.Stmt` 获得。未知来源的 `Stmt` 不报告；无法确定为事务连接的 GORM 值（例如结构体字段或函数返回值）按非事务连接处理，可能误报。跨函数数据流和间接数据库调用无法可靠追踪。

事务生命周期等其他检查使用函数内控制流和类型信息，不追踪事务别名、跨函数调用、间接调用、事务转移给调用方或 defer 闭包内的回滚；GORM 自动事务仅分析直接传入的回调。错误检查为语法级近似：识别错误的比较或返回，及 GORM 结果的 `Error` 字段比较或返回，不保证每条路径都处理错误，也不识别所有自定义错误处理方式。无法确定的分支按可能执行分析，短路表达式和复杂条件可能产生误报。

在违规代码的上一行添加 `// force ignore sharklint: any`、`// force ignore sharklint: goroutine`、`// force ignore sharklint: logging`、`// force ignore sharklint: maprace`、`// force ignore sharklint: panic`、`// force ignore sharklint: syncpair`、`// force ignore sharklint: redis`、`// force ignore sharklint: sql` 或 `// force ignore sharklint: swagger` 可以分别忽略对应规则。也可以在 Go 程序中调用 `sharklint.Lint(path)`；提供 `Violation` 类型的规则可通过 `errors.As` 获取。`go vet` 的诊断输出会附在返回错误中，相对文件路径会转换为绝对路径；包加载、类型检查、文件系统和 Go 解析错误也会被报告。

### 配置文件

默认读取 `./config/config.yaml`。文件不存在或无法解析时 `NewOption` 返回 error。仓库里的 `config/config.yaml` 是整份注释掉的模板，取消注释后才会初始化对应组件；没有 `host` 的中间件保持 nil。

优先级：环境变量 > 文件 > 代码默认值。环境变量名是键的大写形式，`.` 换成 `_`，例如 `REDIS_HOST`。`env`、`id`、`http` 这类短键也会读取同名系统环境变量。`host` 可以写成 YAML 列表或逗号分隔字符串。

密码按明文读取。只有 `NewOptionWithRsaAndConfigFile` 传入 PKCS#1 或 PKCS#8 PEM 私钥时，才会解密 Base64 编码的 PKCS#1 v1.5 密文；私钥解析失败会 panic。

```yaml
env: dev          # dev / test / prod，默认 dev；dev 下注册 Swagger
id: "1"           # 默认 "1"
http: 3928        # 0 或不写则不启动
grpc: 3929
health: 3930      # GET /health，只表示进程在听
pprof: 3931
timer: true       # 依赖 Redis
kafkalog: true    # 依赖 kafka，topic 为 {project}_game_log_{env}

db:
  host: 127.0.0.1:4000
  user: root
  password: yourpassword
  database: mydb

redis:            # 先按集群连接，仅当服务端明确不支持集群时回退单机
  host: 127.0.0.1:6379
  password: ""

kafka:
  host: 127.0.0.1:9092
  user: ""
  password: ""
  tls: false
```

其余键：`redis_cluster`、`redis_client`（结构同 `redis`，仅在自动探测没连上对应模式时生效）、`elastic`、`clickhouse`（含 `database`、`tls`）、`rabbitmq`、`risingwave`、`etcd`、`mongodb`、`minio`。字段和默认值见 `config/config.yaml`。

### 启动应用

```go
package main

import "github.com/xcodego/shark/sharkapp"

func main() {
    options, err := sharkapp.NewOption("myproject", "instance-1")
    if err != nil {
        panic(err)
    }
    app, err := sharkapp.New(options)
    if err != nil {
        panic(err)
    }
    app.Hunt(&MyService{svc: app})
}

type MyService struct {
    svc *sharkapp.App
}

func (s *MyService) Start() {
    s.svc.Db.Table("users").Where("status = ?", 1).Find(&users)
}
```

`WithXxx` 在文件加载成功之后覆盖单项配置，例如 `options.WithDB(&sharkdb.Config{...})`。指定路径用 `NewOptionWithConfigFile`。

`Hunt` 调用各组件的 `Start()`（应尽快返回），再启动 gRPC，然后阻塞等待 SIGTERM / SIGINT。收到信号后在 `Hunt` 内按依赖顺序关闭 App 持有的全部组件。`SafeGo` 不登记 WaitGroup。

`AppComponent` 需要同时实现 `Start()` 和 `Stop()`。`Stop` 在入口停止后、Context 取消前，按传入 `Hunt` 的相反顺序调用，应在有限时间内返回；panic 会被恢复并记录。

关闭顺序：

1. 停入口：HTTP 优雅关闭（最长 10 秒）、gRPC 优雅停止（最长 10 秒，超时强停）、关闭全部 WebSocket 连接、关闭 pprof 与健康检查；
2. 调用业务组件的 `Stop()`；
3. 取消 Context，等待 500ms 和已登记的 WaitGroup；
4. 停后台任务：定时器、RabbitMQ（先发完队列中的消息）、gRPC 客户端连接；
5. 关闭数据客户端：Elasticsearch、MongoDB（各最长 5 秒）、etcd、ClickHouse、RisingWave、MySQL、Redis；
6. 关闭日志的 Kafka 推送，关闭 Kafka 客户端，最后释放协程池（最长 5 秒，等待仍在运行的任务）。

MinIO 没有需要关闭的资源。关闭失败只记录日志，不中断后续步骤。`New` 失败返回 error 时没有 App，已创建的连接不会被自动关闭。

## 包结构总览

| 包名 | 功能 | 核心导出 |
|------|------|----------|
| `sharkapp` | 应用启动与生命周期 | `App`, `New`, `NewOption`, `Hunt`, `SafeGo`, `RunInPool`, `WithTimeout`, `ParallelCall` |
| `sharklint` | Go 源码检查 | `Lint`；命令行 `sharklint <目录>` |
| `sharklog` | 控制台 + Kafka 日志 | `SharkLog`, `New`, `SetKafkaWriter`, `Close`（先 Sync） |
| `sharkdb` | MySQL / GORM | `Config`, `NewDb`, `Close`, `NewTable`, `NewTableScan`, `BinlogListener` |
| `sharksql` | SQL 条件、表达式、分页 | `SqlBuilder`, `NewSql`, `Where`, `PageQuery`, `IsDuplicateKeyError` |
| `sharkredis` | Redis 集群 / 单机 | `NewCluster`, `NewClient`, `Helper`, `Scan`, `Unlink`, `SharkMutex` |
| `sharkkafka` | Kafka 生产与批量消费 | `Client`, `New`, `Writer`, `Reader`, `BatchConsumer`, `Close` |
| `sharkgrpc` | gRPC 监听与 Redis 服务发现 | `Server`, `New`, `Run`, `GetRpcConnection`, `Stop`, `Close` |
| `sharkhttp` | Gin HTTP | `Server`, `NewServer`, `New`, `Shutdown`, `WsUpgrader` |
| `sharkws` | Gin 上的 WebSocket | `NewServer`, `Listen`, `Send`, `Broadcast`, `Close`, `CloseAll` |
| `sharktimer` | Redis ZSet 定时器 | `Client`, `NewTimer`, `AddTimer`, `AddTimeWithId`, `RemoveTimer`, `Close` |
| `sharksnowflake` | Snowflake ID | `New`, `Generate` |
| `sharkdecimal` | 高精度数值 | `Normalize`, `Normalize2`, `Normalize6` |
| `sharkelastic` | Elasticsearch | `Client`, `New`, `CreateIndex`, `Search`, `Insert`, `Build`, `Close` |
| `sharkclickhouse` | ClickHouse 原生协议 | `Config`, `New` |
| `sharkrabbitmq` | RabbitMQ | `Client`, `New`, `Publish`, `Consume`, `BatchConsume`, `Close` |
| `sharkmongodb` | MongoDB | `New`, `BinlogListener` |
| `sharkminio` | MinIO（固定 HTTP） | `New` |
| `sharketcd` | etcd | `New` |
| `sharkrisingwave` | RisingWave（PostgreSQL 协议） | `New` |
| `sharkcache` | 多层缓存 | `Cache`, `New`, `Get`, `ErrNotFound` |
| `sharkauth` | RBAC 权限树 | `AuthNode`, `NormalizeAuthTree`, `PruneAuth`, `SyncAuthTree`, `Permissions`, `Flatten` |
| `sharkerror` | 业务错误 | `New`, `WithData`, `WithErr`, `WithMsg` |
| `sharkid` | Redis 预生成用户 ID / 唯一 ID | `New`, `Run`, `GetUserId`, `GetUniqueId` |
| `sharkjson` | JSON（sonic） | `ParseJsonBytes`, `ParseJsonString`, `ToJsonBytes`, `ToJsonString` |
| `sharkutils` | 通用工具 | `RandNum`, `Md5`, `GetClientIp`, `BcryptHash`, `BcryptCheck`, `Pointer`, `DrainChannelN`, `Compress`, `Decompress` |
| `sharkverify` | TOTP | `NewSecret`, `VerifyCode`, `GetQrCodeUrl` |

## 核心模块

### 应用启动 (`sharkapp`)

`New` 按固定顺序初始化已配置的组件：ants 池、日志、Kafka、Redis、MySQL、Elasticsearch、RabbitMQ、RisingWave、etcd、MongoDB、MinIO、ClickHouse、定时器、gRPC、HTTP，然后异步启动 pprof 和健康检查。初始化失败返回 error，已创建的连接不会被统一关闭。

Redis 的 `redis` 配置先尝试集群，只有错误信息表明服务端不支持集群时才回退单机。`redis_cluster` / `redis_client` 不会覆盖已经连上的对应客户端。定时器和 gRPC 优先使用集群客户端。gRPC 端口未配置但 Redis 可用时，仍会创建端口为 -1 的 `Server`，只做客户端发现，`Run` 不监听。

```go
app.SafeGo(func(...any) {
    // panic 会被记录。不经过协程池，也不登记 WaitGroup。
})

app.RunInPool(func(args ...any) {
    // 提交到 AntPool；池未限制容量时，空闲 worker 1 分钟后回收。
})
```

`WithTimeout` 在池中执行函数。超时返回错误字符串 `"timeout"`，函数本身会继续在后台跑，应监听传入的 context。`ParallelCall` 等待全部函数结束后用 `errors.Join` 合并错误。

### 日志 (`sharklog`)

```go
log := sharklog.New(ctx, "myproject", "instance-1")
writer, _ := kafkaClient.Writer("app-logs", nil)
log.SetKafkaWriter(writer) // 只能设置一次

log.Zap.Info("服务启动", zap.String("env", "prod"))
```

未设置 Writer 时 Kafka 侧直接丢弃，控制台仍输出。Kafka 消息字段为 `log_id`、`server_name`（`name-id`）、`server_host`、`msg`。应用在 `kafkalog: true` 且 Kafka 已连接时，自动把日志写到 `{project}_game_log_{env}`。

### 数据库 (`sharkdb`)

```go
db, err := sharkdb.NewDb(ctx, logger, &sharkdb.Config{
    Host: "127.0.0.1:3306", User: "root",
    Password: "secret", Database: "mydb",
})
```

SQL 日志走 zap。`ErrRecordNotFound` 和 MySQL 1062 不记 Error。`Tls` 非空即启用 TLS（最低 TLS 1.2）。

```go
table := sharkdb.NewTable(db.Table("users"))
table.Eq("status", 1).Like("name", "张").Gte("age", 18).
    FromTo("created_at", start, end).Desc("id").Gorm().Find(&users)

b := sharksql.NewSql().Eq("status", "pending").Or(sharksql.NewSql().Eq("status", "done"))
table.Eq("deleted", 0).Or(b).Gorm().Find(&tasks)
```

列名直接拼进 SQL，必须是代码里的可信列名。

`TableScan` 用 keyset 翻页。排序字段同样必须是固定列名，多列合起来应能唯一确定一行。`ExportExcel` / `ExportCsv` 必须先 `Asc` 或 `Desc`，返回的是 `os.TempDir()` 下的文件名。

```go
scan := sharkdb.NewTableScan[User]().PageSize(500).Asc("create_time", "id")
var last *User
for {
    results, err := scan.Next(db.Where("deleted = 0"), last)
    if err != nil || len(results) == 0 {
        break
    }
    last = &results[len(results)-1]
}
fileName, err := scan.ExportExcel(ctx, db.Where("status = 1"),
    "用户列表", []any{"ID", "姓名"},
    func(u User) []any { return []any{u.Id, u.Name} },
)
```

`BinlogListener` 订阅 MySQL binlog，回调在 `OnInsert` / `OnUpdate` / `OnDelete` 注册。

### SQL 条件 (`sharksql`)

比较、LIKE、IN 的值走占位符。列名、别名、JSON 路径和表达式片段直接拼接，必须可信。`SqlBuilder` 会跳过 nil、空切片和空 map，不会跳过 0 和 `""`。

```go
b := sharksql.NewSql().Eq("deleted", 0).
    And(sharksql.NewSql().Eq("status", "pending").Or(sharksql.NewSql().Eq("status", "in_progress")))
sql, args := b.Build()
db.Where(sql, args...).Find(&results)

onSQL, onArgs := sharksql.LeftJoin("orders o", sharksql.NewSql().EqCol("u.id", "o.user_id").Eq("o.deleted", 0))
db.Joins(onSQL, onArgs...).Find(&results)
```

独立条件函数返回 SQL 和参数，需要拆开传给 GORM：

```go
sql, arg := sharksql.Eq("status", 1)
db.Where(sql, arg)
sql, lo, hi := sharksql.Between("age", 18, 60) // [18, 60)，不是 SQL BETWEEN
db.Where(sql, lo, hi)
db.Select(sharksql.SumAs("amount", "total")).Order(sharksql.Desc("created_at"))

setSQL, setArg := sharksql.Add("balance", 100)
db.Update("balance", gorm.Expr(setSQL, setArg))

searchSQL, searchArg := sharksql.JsonSearchOne("tags", "vip")
db.Where(searchSQL, searchArg)
```

`SumAs` / `CountAs` 等按「字段, 别名」成对传入，参数个数为奇数时返回空串。

```go
users, total, err := sharksql.PageQuery[User](db.Where("status = 1"), 1, 20)
if sharksql.IsDuplicateKeyError(dbErr) { /* 1062 */ }
```

`page` 小于 1 按 1，`pageSize` 小于 10 按 10。`page * pageSize` 超过 10000 返回业务错误。

`Where(struct)` 按结构体字段和 tag 生成条件。`ToUpdateData` 生成 UPDATE 用的 map。

### 高精度数值 (`sharkdecimal`)

```go
price := sharkdecimal.Normalize2(19.999)    // 先 Round 再截断到 2 位
rate := sharkdecimal.Normalize6(7.12345678) // 截断到 6 位
sharkdecimal.Normalize(3.1415926535, 4)
```

支持 decimal、字符串、整数和浮点数。无法解析时返回 `decimal.Zero`。负的小数位按 0 处理。

### Snowflake (`sharksnowflake`)

```go
sf := sharksnowflake.New()
id := sf.Generate()
```

纪元是 2025-01-01 00:00:00 UTC+8。ID 只保证同一进程内唯一。当前秒序列用尽时，`Generate` 会持锁阻塞到下一秒。

### 缓存 (`sharkcache`)

```go
cache := sharkcache.New[User](
    func(args ...any) (*User, error) { return localCache.Get(args[0].(int64)) },
    func(args ...any) (*User, error) { return redis.GetUser(args[0].(int64)) },
    func(args ...any) (*User, error) { return db.FindUser(args[0].(int64)) },
)
user, err := cache.Get(userId)
```

同一组参数经 `sharkutils.MakeKey` 合成 key，并发请求只查一次。返回非 nil 即命中；nil 且错误为 nil 或 `ErrNotFound` 时继续下一层；其他错误立即返回。

### 权限树 (`sharkauth`)

`Auth`：0 未设置（叶子会被 `PruneAuth` 删掉），1 有权限，2 可见但无权限。

```go
childTree := sharkauth.NormalizeAuthTree(fullTree, 2) // 带 URL 的节点设为给定 Auth，并清空 URL
pruned := sharkauth.PruneAuth(childTree)

sharkauth.SyncAuthTree(parentTree, childTree) // 直接改 parent，没有返回值
urlPerms := sharkauth.Permissions(fullTree)   // URL → 权限路径
flat := sharkauth.Flatten(childTree)          // "父.子" → Auth
```

### 业务错误 (`sharkerror`)

```go
var ErrUserNotFound = sharkerror.New(10001, "用户不存在")

if errors.Is(err, ErrUserNotFound) { /* 按 Code 匹配 */ }

err := ErrUserNotFound.WithData(map[string]any{"user_id": 123}).WithMsg("指定的用户不存在")
```

`WithXxx` 返回新实例。HTTP 错误中间件把 `*Error` 转成 JSON（`code` / `msg` / `data`）。`WithErr` 挂上的底层错误只进日志，不进响应。

### gRPC (`sharkgrpc`)

```go
server := sharkgrpc.New(ctx, "myproject", rdb, logger, 50051)
pb.RegisterEchoServer(server.Server, &EchoServiceImpl{})
server.Run()

conn, err := server.GetRpcConnection("user-service")
```

地址由外部写入 Redis。key 还不存在时，客户端会 `Set "[]"` 占位，这不是服务注册。已有连接会复用，地址列表大约每 5 秒刷新。

### Redis (`sharkredis`)

```go
cluster, err := sharkredis.NewCluster(ctx, &sharkredis.Config{
    Host: []string{"redis1:6379", "redis2:6379"}, Password: "secret",
})
client, err := sharkredis.NewClient(ctx, &sharkredis.Config{
    Host: []string{"127.0.0.1:6379"},
})

helper := sharkredis.NewHelperWithCluster(cluster, "myproject", "instance-1")
for key := range helper.Scan(ctx, "user:*") {
    fmt.Println(key)
}
err = helper.Unlink(ctx, "cache:*")
```

`PoolSize` / `MinIdleConns` 为 0 时分别是 200 和 20。`ReplaceFrom` / `ReplaceTo` 只在集群模式生效。`Scan` 必须消费完或取消 ctx。`NewClient` 只用 `Host[0]`。

### Kafka (`sharkkafka`)

```go
sk, err := sharkkafka.New(ctx, &sharkkafka.Config{
    Host: []string{"kafka:9092"}, User: "admin", Password: "secret",
}, logger)
defer sk.Close()

w, err := sk.Writer("order-events", nil) // nil 使用默认批量参数，Async 默认为 true
w.WriteMessages(ctx, kafka.Message{Key: []byte("order-1"), Value: []byte(`{"status":"created"}`)})

sk.BatchConsumer("order-events", "order-group", nil, func(msgs []kafka.Message) bool {
    return true // false 或 panic 都停止消费，且不提交这批 offset
})
```

`user` 和 `password` 都非空时启用 SASL/SCRAM-SHA-512。`tls: true` 时最低 TLS 1.2。

### RabbitMQ (`sharkrabbitmq`)

```go
mq, err := sharkrabbitmq.New(ctx, logger, wg, &sharkrabbitmq.Config{
    Host: []string{"mq1:5672", "mq2:5672"}, User: "guest", Password: "guest",
}, "my-service", "1")

err = mq.Publish("orders", "created", Order{ID: 123, Status: "pending"})

mq.BatchConsume("orders", "order-queue", "created", nil, func(msgs []amqp.Delivery) bool {
    return true // true 时整批 Ack；false 或 panic 时不 Ack，消息会重新投递
})
```

`New` 用服务名的 CRC16 选定一个 broker，首次连上之前一直重试。`Publish` 放入内存队列后返回，消息体上限 100KB。`Host` 不能为空，缺省协议是 `amqp`。

### 定时器 (`sharktimer`)

```go
timer := sharktimer.NewTimer(ctx, "myproject", "order-timer", "inst-1", rdb, logger)
timerID := timer.AddTimer(30*time.Minute, func() { cancelOrder("12345") })
timer.RemoveTimer(timerID)
timer.AddTimeWithId("order:cancel:12345", 30*time.Minute, func() { cancelOrder("12345") })
timer.DefaultCallback(func(timerID string) {
    callback := loadCallbackFromDB(timerID)
    callback()
})
```

Redis key 是 `{project}:timer:{name}-{id}`。回调只在本进程内存里；重启后到期任务走 `DefaultCallback`。单机轮询，不抢分布式锁。

### HTTP (`sharkhttp`) 与 WebSocket (`sharkws`)

`sharkhttp.NewServer` 异步监听端口并返回带 `Shutdown` 的 `Server`（`sharkhttp.New` 只返回 `*gin.Engine`，无法关闭），中间件顺序是 recovery、CORS、错误处理。CORS 允许任意 Origin，方法只放行 GET 和 POST，OPTIONS 返回 204。`evn == "dev"` 时挂上 `/swagger/*any`。recovery 把 panic 记入日志（含路径和请求体）并返回固定内部错误。业务错误转 JSON；未知错误只打日志，响应是固定文案。

`sharkws.NewServer` 挂在 Gin 上。收发队列满了会阻塞，连接和消息回调在接收协程里同步执行。不要对同一连接同时 `Close` 和 `Send`。`CloseAll` 关闭全部连接并停止收发协程，不触发 `OnClose`，`Hunt` 退出时会调用它。

### ClickHouse (`sharkclickhouse`)

配置键是 `clickhouse.host`、`user`、`password`、`database`、`tls`，也可以 `options.WithClickHouse`。地址是原生协议（常见端口 9000，TLS 常见 9440）。`tls: true` 时最低 TLS 1.2，并按默认 TLS 配置校验服务端证书。`New` 会 Ping。`Hunt` 退出时会关闭 `app.ClickHouse`。

### Elasticsearch (`sharkelastic`)

```go
es, err := sharkelastic.New(ctx, &sharkelastic.Config{
    Host: []string{"http://es:9200"}, User: "elastic", Password: "secret",
})
err = es.CreateIndex(ctx, "users", 2,
    sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
    sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
)
err = es.Insert(ctx, "users", "user_id",
    map[string]any{"user_id": "1", "name": "张三", "age": 25},
)
body, err := es.Search(ctx, "users", map[string]any{
    "query": map[string]any{"match": map[string]any{"name": "张三"}},
})
```

地址必须带 `http://` 或 `https://`。`Build` 把类 SQL 的 where 文本转成 Query DSL。`ExportExcelBySql` / `ExportCsvBySql` 返回临时目录中的文件名。

### 用户 ID (`sharkid`)

`sharkid.New` 用 Redis 位图给固定位数的用户 ID 去重，并预填分片列表。`UserIdDigits` 取值 1 到 10，小于等于 0 时默认 8。创建后要调用 `Run`。唯一 ID 由 `UniqueIdFunc` 提供，为 nil 时不生成。

### JSON、工具与 TOTP

```go
user := sharkjson.ParseJsonBytes[User](jsonBytes) // 失败返回 nil
text := sharkjson.ToJsonString(user)              // 失败返回 ""

n := sharkutils.RandNum(1, 100)          // [min, max)；min <= 0 或 min >= max 时返回 0
hash := sharkutils.Md5([]byte("hello"))
ip := sharkutils.GetClientIp(request)    // X-Forwarded-For、X-Real-Ip、RemoteAddr
hashed := sharkutils.BcryptHash("myPassword123")
ok := sharkutils.BcryptCheck(hashed, "myPassword123")
name := sharkutils.Pointer("hello")
compressed, err := sharkutils.Compress([]byte("large data"))
original, err := sharkutils.Decompress(compressed)

secret, qrURL := sharkverify.NewSecret("MyCompany", "user@example.com")
if sharkverify.VerifyCode(secret, "123456") { /* SHA1、6 位、30 秒，允许前后各一个周期 */ }
```

`DrainChannelN` 先阻塞读到第一条，之后最多收集到 `size` 条。`timeout > 0` 时，通道变空后还会再等一段时间。

`GetQrCodeUrl` 把传入的 `secret` 当作原始字节再做 Base32，issuer 或 accountName 为空会 panic。

## 初始化顺序

```
config.yaml / 环境变量
        │
        ▼
sharkapp.New
  ants 池 → 日志 → Kafka（可选 kafkalog）
  → Redis（集群，不支持时回退单机；再补显式配置）
  → MySQL → Elasticsearch → RabbitMQ → RisingWave
  → etcd → MongoDB → MinIO → ClickHouse
  → 定时器 → gRPC → HTTP
  → 异步 pprof /health
        │
        ▼
App.Hunt：Start() → gRPC Run → 等待信号
退出时：停入口 → 组件 Stop → 取消 Context → 500ms → WaitGroup
          → 定时器/RabbitMQ/gRPC 客户端 → 数据客户端 → 日志 → Kafka → 协程池
```

## 作者

xcodego

## 许可证

MIT License
