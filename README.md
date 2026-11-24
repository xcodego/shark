# Shark

一个轻量级、模块化的 Go 微服务开发框架，内置了十余种基础设施组件的连接管理和实用工具。

## 项目简介

Shark 封装了微服务开发中常见的中间件和工具库，提供统一的配置加载（`config.yaml`）、连接管理和最佳实践。各子包均可独立使用，无需依赖完整的框架上下文。

**模块路径：** `github.com/xcodego/shark`

**Go 版本要求：** 1.26+

## 特性

- **统一的配置管理** — 所有组件通过 `config.yaml` 统一配置，支持 json/yaml/mapstructure 标签 + 环境变量覆盖
- **双通道日志** — 控制台（Console Encoder）+ Kafka（JSON Encoder），通过 `zapcore.NewTee` 合并，Snowflake 生成每条日志唯一 ID
- **内置服务发现** — gRPC 客户端从 Redis 读取地址列表，支持动态更新、round_robin 负载均衡和指数退避重试
- **高性能 ID 生成** — 改进型 Snowflake 算法，41位时间戳 + 19位序列号，每秒 52 万个 ID，int64 类型前端安全
- **SQL 条件构建器** — 流式 API 构建参数化 WHERE/JOIN ON 子句，空值自动跳过，防 SQL 注入，支持 AND/OR 任意嵌套、字段对字段比较
- **Keyset 游标分页** — 泛型表扫描器，深分页性能不受数据量影响，支持双向翻页（Next/Prev）和 Excel 流式导出
- **高精度数值** — 基于 shopspring/decimal 的类型归一化（10+ 种），Round 消浮点误差 + Truncate 截断到指定位数
- **批量消费者** — Kafka/RabbitMQ 批量拉取 + 管道缓冲 + 自动提交 offset，支持重试和优雅停止
- **定时器** — Redis ZSet 轻量级单机定时器，误差 ±1s，ants 协程池异步执行回调，支持默认回调
- **泛型工具** — PageQuery 分页、TableScan 扫描、WithTimeout 超时控制、DrainChannelN 管道排空、Pointer 指针安全
- **多层缓存防击穿** — 基于 singleflight + seeker 链的泛型缓存穿透防护（本地缓存 → Redis → DB 多级回退）
- **RBAC 权限树** — 多叉树权限模型，支持父子角色继承、权限裁剪、Sync 生成编辑树、Flatten/Permissions 高效查表
- **有序映射（跳表）** — 泛型 SkipList，O(log n) 查找/插入/删除，支持范围查询、Ceiling/Floor、有序遍历

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
- `vet`：在目标目录运行 `go vet ./...`，检查当前构建条件下的 Go 包；目录没有 Go 文件时跳过。
- `goroutine`：递归扫描所有非测试 `.go` 文件（包括 vendor 和不满足当前构建条件的文件），不跟随符号链接，禁止直接使用 `go` 启动协程。
- `panic`：递归扫描所有非测试 `.go` 文件，不跟随符号链接，禁止直接调用内置 `panic`（`package main` 的入口函数 `main` 除外）。
- `maprace`：通过类型信息检查当前构建条件下的非测试 Go 包，检查直接 `go func(...)` 和 `sharkapp.App.SafeGo` 的函数字面量捕获的 map 的索引读写、`delete`、`clear`、`len` 和遍历；也比较同一函数中启动后的访问和其他异步闭包。单纯的并发只读不报告；共享 map 的异步写入若未持有 `sync.Mutex` 或 `sync.RWMutex` 的写锁会报告，互相冲突的访问须持有同一个锁（读操作允许读锁）。控制流分支合并时仅认可所有路径均持有的锁。检查采用保守近似，异步写入即使实际只执行一次也可能报告；不识别同步等待、channel、跨函数调用、map 或锁的别名、闭包参数传入的 map、间接调用、其他协程池和自定义锁，不能替代 `go test -race`。仅分析函数声明内的直接异步闭包，不追踪嵌套函数、复杂 map 表达式或 map 重新赋值，启动位置之前的访问不考虑循环下一次迭代；需要 Go 模块或工作区，不检查其他构建条件、vendor 和嵌套模块。
- `syncpair`：通过类型信息和函数内控制流检查 `sync.Mutex` / `sync.RWMutex` 的 `Lock`、`Unlock` 和 `RLock`、`RUnlock` 配对，及局部 `sync.WaitGroup` 的常量 `Add`、`Done` 配对；检查返回或显式 panic 路径，并识别直接 `defer` 释放。WaitGroup 支持直接 `go func` 和 `sharkapp.App.SafeGo` 闭包捕获的 `Done` 或负数 `Add`，检查闭包分支是否遗漏完成操作，以及 `Wait` 前是否存在未完成的计数。检查为近似分析，不推断分支条件之间的关系，可能误报；计数上限为 64。WaitGroup 被传给其他函数、被其他回调捕获、使用动态或超出 ±64 的 Add，或在工作闭包中增加计数时不检查该对象；不追踪别名、重新赋值、闭包参数、跨函数完成、嵌套协程、自定义协程池或 defer 闭包。使用 `TryLock` / `TryRLock` 的锁不检查；锁通过参数或字段在函数间转移、间接调用和复杂接收者也无法可靠分析；仅在函数内有获取操作或锁为局部变量时报告多余释放。需要 Go 模块或工作区，不检查其他构建条件、vendor 和嵌套模块。
- `logging`：禁止使用 `fmt.Print*`、导入 `log`/`log/slog` 或使用 zap 全局 Logger；业务代码应使用注入的 `*zap.Logger`。只有 `sharklog.New` 可创建 Logger；`sharkdb/binloglistener.go` 中为 go-mysql 提供的 `log/slog` 适配除外，检查器命令的格式化输出不作为日志处理。
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

在项目根目录创建 `config/config.yaml`：

```yaml
http: 3928
grpcx: 3929
health: 3930
pprof: 3931

db:
  host: 127.0.0.1:4000
  user: root
  password: yourpassword
  database: mydb

redis:
  host: 127.0.0.1:6379
  password: ""

kafka:
  host: 127.0.0.1:9092
  user: ""
  password: ""
```

### 启动应用

```go
package main

import "github.com/xcodego/shark/sharkapp"

func main() {
    options := sharkapp.NewOption("myproject", "instance-1")
    // 可选：代码中覆盖部分配置
    // options.WithDB(&sharkdb.Config{Host: "custom-db:3306", ...})
    app, err := sharkapp.New(options)
    if err != nil {
        panic(err)
    }
    // 注册并启动业务模块
    app.Hunt(&MyService{svc: app})
}

type MyService struct {
    svc *sharkapp.App
}

func (s *MyService) Start() {
    // 使用 app 的各种客户端
    s.svc.Db.Table("users").Where("status = ?", 1).Find(&users)
}
```

## 包结构总览

| 包名 | 功能 | 核心导出 |
|------|------|----------|
| `sharkapp` | 应用启动与依赖注入 | `App`, `New`, `NewOption`, `Hunt`, `Go` |
| `sharklog` | 双通道日志系统 | `SharkLog`, `New`, `SetKafkaWriter` |
| `sharkdb` | 数据库连接与工具 | `Config`, `NewDb`, `NewTableScan`, `NewTable` |
| `sharkredis` | Redis 集群/单机客户端 | `NewCluster`, `NewClient`, `ScanKeys`, `DeleteKeys` |
| `sharkkafka` | Kafka 生产消费 | `SharkKafka`, `Writer`, `Reader`, `BatchConsumer` |
| `sharkgrpc` | gRPC 服务注册与发现 | `RpcServer`, `New`, `GetRpcConnection` |
| `sharkhttp` | HTTP 服务（Gin） | `New` (含 CORS/Recovery/Error 中间件) |
| `sharktimer` | 基于 Redis ZSet 的定时器 | `Timer`, `AddTimer`, `RemoveTimer`, `AddTimeWithId` |
| `sharksnowflake` | Snowflake ID 生成器 | `NewSnowflake`, `Generate` |
| `sharksql` | SQL 条件构建与分页 | `SqlBuilder`, `NewSql`, `LeftJoin`, `InnerJoin`, `PageQuery`, 40+ 函数 |
| `sharkdecimal` | 高精度数值处理 | `Normalize`, `Normalize2`, `Normalize6` |
| `sharkelastic` | Elasticsearch 客户端 | `SharkElastic`, `CreateIndex`, `Search`, `Insert` |
| `sharketcd` | etcd 分布式键值存储 | `New` (clientv3.Client) |
| `sharkmongodb` | MongoDB 文档数据库 | `New` (mongo.Client) |
| `sharkminio` | MinIO 对象存储 | `New` (minio.Client) |
| `sharkrabbitmq` | RabbitMQ 消息队列 | `Client`, `New`, `Publish`, `Consume`, `BatchConsume` |
| `sharkrisingwave` | RisingWave 流式数据库 | `New` (GORM + PostgreSQL 驱动) |
| `sharkerror` | 统一业务错误类型 | `New`, `WithData`, `WithErr`, `WithMsg` |
| `sharkjson` | 高性能 JSON 序列化 | `ParseJsonBytes`, `ToJsonString` |
| `sharkcache` | 多层缓存防击穿 | `Cache`, `New`, `Get` |
| `sharkauth` | RBAC 权限树管理 | `AuthNode`, `NormalizeAuthTree`, `PruneAuth`, `SyncAuthTree`, `Permissions`, `Flatten` |
| `sharkfunc` | 泛型/并发工具函数 | `WithTimeout`, `ParallelCall`, `DrainChannelN`, `Recover`, `Pointer` |
| `sharkutils` | 通用工具函数 | `RandNum`, `Md5`, `GetClientIp`, `BcryptHash`, `BcryptCheck` |
| `sharkverify` | TOTP 两步验证 | `VerifyCode`, `NewSecret`, `GetQrCodeUrl` |
| `sharkzip` | Zlib 数据压缩 | `Compress`, `Decompress` |
| `sharkskiplist` | 有序映射（跳表） | `SkipList`, `NewAsc`, `NewDesc`, `NewWithComparator`, `NewDescWithComparator`, `SetOrUpdate`, `SetIfNotExists`, `Get`, `Delete`, `RangeAsc/RangeDesc` |

## 核心模块详解

### 应用启动 (`sharkapp`)

`App` 是所有中间件客户端的聚合体，由 `New` 工厂函数根据 `Options` 自动初始化所有已配置的中间件。

```go
package main

import (
    "github.com/xcodego/shark/sharkapp"
    "github.com/xcodego/shark/sharkdb"
)

func main() {
    // 方式一：从 config.yaml 自动加载所有中间件
    opts := sharkapp.NewOption("myproject", "game-server")
    app, _ := sharkapp.New(opts)
    app.Hunt(&MyService{svc: app})

    // 方式二：纯代码配置（无需 config.yaml）
    opts2 := sharkapp.NewOption("myproject", "game-server").
        WithDB(&sharkdb.Config{
            Host: "127.0.0.1:3306", User: "root",
            Password: "secret", Database: "mydb",
        })
    app2, _ := sharkapp.New(opts2)

    // App 暴露的字段：app.Db, app.RedisCluster, app.Kafka, app.Logger 等
    // 安全启动 goroutine（panic 不会导致程序崩溃）
    app.Go(func(ctx context.Context) {
        for {
            select {
            case <-ctx.Done():
                return
            default:
                // 后台任务
            }
        }
    })
}
```

### 日志系统 (`sharklog`)

```go
log := sharklog.New(ctx, "myproject", "instance-1")

// 可选：注入 Kafka Writer 启用远程日志推送
kw := &kafka.Writer{Addr: kafka.TCP("kafka:9092"), Topic: "app-logs"}
log.SetKafkaWriter(kw)

// zap 原生方式记录日志
log.Zap.Info("服务启动", zap.String("env", "prod"))
log.Zap.Error("数据库错误", zap.Error(err))
log.Zap.Info("订单创建",
    zap.String("order_id", "ORD-001"),
    zap.Float64("amount", 99.99),
)
// 每条 Kafka 日志自动附加: log_id(Snowflake), server_name, server_host
```

### 数据库 (`sharkdb`)

#### 数据库连接

```go
db, err := sharkdb.NewDb(ctx, logger, &sharkdb.Config{
    Host: "127.0.0.1:3306", User: "root",
    Password: "secret", Database: "mydb",
})
// GORM 所有功能可用，SQL 日志自动输出到 zap（忽略 ErrRecordNotFound 和 1062 重复键）
db.Table("users").Where("age > ?", 18).Find(&users)
```

#### `SharkTable` — 便捷链式查询

```go
table := sharkdb.NewTable(app.Db.Table("users"))
var users []User
table.Eq("status", 1).
    Like("name", "张").
    Gte("age", 18).
    FromTo("created_at", start, end).
    Desc("id").
    Gorm().Find(&users)

// 支持 SqlBuilder OR 查询
b := sharksql.NewSql().Eq("status", "pending").Or(sharksql.NewSql().Eq("status", "done"))
table.Eq("deleted", 0).Or(b).Gorm().Find(&tasks)
```

#### `TableScan` — Keyset 游标分页

`TableScan` 仅供后端内部代码使用。`Asc`/`Desc` 的字段必须是代码中固定的可信数据库列名，不能直接采用前端请求字段；导出前必须至少配置一个排序字段（建议最后一列唯一）。

```go
scan := sharkdb.NewTableScan[User]().PageSize(500).Asc("create_time", "id")
var last *User
for {
    results, _ := scan.Next(db.Where("deleted = 0"), last)
    if len(results) == 0 { break }
    last = &results[len(results)-1]
    for _, u := range results { process(u) }
}

// 上一页
prev, _ := scan.Prev(db.Where("deleted = 0"), &results[0])

// 流式 Excel 导出（百万级数据，内存仅一页）
filePath, _ := scan.ExportExcel(ctx, db.Where("status = 1"),
    "用户列表", []any{"ID", "姓名", "创建时间"},
    func(u User) []any { return []any{u.Id, u.Name, u.CreateTime} },
)
```

### SQL 条件构建器 (`sharksql`)

#### SqlBuilder — 动态 WHERE/JOIN ON 子句

```go
// 基础 AND
b := sharksql.NewSql().Eq("status", 1).Gte("age", 18).Like("name", "张")

// OR 查询
b := sharksql.NewSql().Eq("created_by", uid).Or(sharksql.NewSql().Eq("assignee", uid))

// 复杂嵌套
b := sharksql.NewSql().Eq("deleted", 0).
    And(sharksql.NewSql().Eq("status", "pending").Or(sharksql.NewSql().Eq("status", "in_progress"))).
    And(sharksql.NewSql().Eq("created_by", uid).Or(sharksql.NewSql().Eq("assignee", uid)))

sql, args := b.Build()
db.Where(sql, args...).Find(&results)

// JOIN ON 子句（字段对字段比较）
onB := sharksql.NewSql().EqCol("u.id", "o.user_id").Eq("o.deleted", 0)
joinSQL, joinArgs := sharksql.LeftJoin("orders o", onB)
// → LEFT JOIN orders o ON (u.id = o.user_id AND o.deleted = ?)
db.Joins(joinSQL, joinArgs...).Find(&results)
```

#### 条件函数（与 GORM Where/Having 直接配合）

```go
// 比较运算符
db.Where(sharksql.Eq("status", 1), sharksql.Gte("amount", 100), sharksql.Like("title", "订单")).Find(&orders)
db.Where(sharksql.In("city", []string{"北京","上海"}), sharksql.IsNull("deleted_at")).Find(&users)
db.Where(sharksql.Between("age", 18, 60)).Find(&users)  // age >= ? AND age < ?

// 聚合函数
db.Select(sharksql.Count("*")).Find(&countResult)
db.Select(sharksql.SumAs("amount", "total", "fee", "total_fee")).Find(&result)
db.Select(sharksql.CountAs("id", "total_count", "DISTINCT user_id", "unique_users")).Find(&result)
db.Order(sharksql.Desc("created_at")).Find(&orders)

// 去重
db.Select(sharksql.Distinct("status")).Find(&statuses)

// JOIN
db.Joins(sharksql.LeftJoin("orders o", sharksql.NewSql().EqCol("u.id", "o.user_id"))).
   Joins(sharksql.InnerJoin("accounts a", sharksql.NewSql().EqCol("u.account_id", "a.id"))).
   Find(&results)

// 字段算术更新
db.Update("balance", gorm.Expr(sharksql.Add("balance", 100)))  // balance = balance + 100
db.Update("price", gorm.Expr(sharksql.Mul("price", 1.1)))      // price = price * 1.1

// MySQL JSON 操作
db.Where(sharksql.JsonSearchOne("tags", "vip")).Find(&users)      // JSON_SEARCH
db.Where(sharksql.JsonContains("roles", `"admin"`)).Find(&users)  // JSON_CONTAINS
db.Select(sharksql.JsonExtract("metadata", "$.name")).Find(&r)    // JSON_EXTRACT
db.Where(fmt.Sprintf("%s = ?", sharksql.JsonUnquote("metadata", "$.city")), "NYC").Find(&users)
db.Update("tags", gorm.Expr(sharksql.JsonArrayAppend("tags", "new_tag")))
db.Update("tags", gorm.Expr(sharksql.JsonRemove("tags", "$[0]")))
db.Update("tags", gorm.Expr(sharksql.JsonArrayInsert("tags", "$[0]", "vip")))
db.Select(sharksql.JsonLength("tags")).Find(&r)
db.Select(sharksql.JsonKeys("metadata")).Find(&r)
db.Where(fmt.Sprintf("%s = ?", sharksql.JsonType("metadata")), "OBJECT").Find(&r)

// 分页
users, total, _ := sharksql.PageQuery[User](db.Where("status = 1"), 1, 20)

// 重复键检测
if sharksql.IsDuplicateKey(err) { /* 幂等处理 */ }
```

### 高精度数值 (`sharkdecimal`)

```go
import sd "github.com/xcodego/shark/sharkdecimal"

// 金额（2 位小数）
price := sd.Normalize2(19.999)   // 19.99
tax := sd.Normalize2("3.50")     // 3.50
total := sd.Normalize2(price.Add(tax))

// 汇率（6 位小数）
rate := sd.Normalize6(7.12345678)  // 7.123456
usd := sd.Normalize2(100)
cny := sd.Normalize2(usd.Mul(rate))

// 自定义精度
sd.Normalize(3.1415926535, 4)    // 3.1415
sd.Normalize("1e2", 2)           // 100.00
sd.Normalize(1.0/3.0, 6)         // 0.333333（消除浮点误差）
```

### Snowflake ID (`sharksnowflake`)

```go
sf := sharksnowflake.NewSnowflake()

// 单个 ID
id := sf.Generate()  // int64，前端可直接使用，无需字符串转换

// 批量预生成（预填充 Redis 列表）
ids := make([]int64, 1000)
for i := range ids { ids[i] = sf.Generate() }
```

### 缓存防击穿 (`sharkcache`)

```go
import "github.com/xcodego/shark/sharkcache"

// seeker 链：本地缓存 → Redis → DB（任一命中即返回）
cache := sharkcache.New[User](
    func(args ...any) (*User, error) { return localCache.Get(args[0].(int64)) },
    func(args ...any) (*User, error) { return redis.GetUser(args[0].(int64)) },
    func(args ...any) (*User, error) { return db.FindUser(args[0].(int64)) },
)

user, err := cache.Get(userId)  // singleflight 防击穿；未命中才回退，存储错误直接返回
if errors.Is(err, sharkcache.ErrNotFound) {
    // 所有 seeker 都未命中
}
```

### RBAC 权限管理 (`sharkauth`)

```go
// 完整功能树
fullTree := []*sharkauth.AuthNode{{
    Name: "系统管理", Children: []*sharkauth.AuthNode{{
        Name: "用户管理", Children: []*sharkauth.AuthNode{
            {Name: "用户列表", Urls: []string{"/api/user/list"}},
            {Name: "用户详情", Urls: []string{"/api/user/detail"}},
        },
    }},
}}

// 初始化子角色权限模板（默认无权限）
childTree := sharkauth.NormalizeAuthTree(fullTree, 2)
childTree[0].Children[0].Children[0].Auth = 1  // 用户列表有权限

// 裁剪空节点
pruned := sharkauth.PruneAuth(childTree)

// 生成前端编辑树（标记权限状态）
editTree := sharkauth.SyncAuthTree(fullTree, childTree)
// Auth=1 → checkbox checked, Auth=2 → unchecked

// 生成 URL→权限 映射（鉴权中间件 O(1) 查表）
urlPerms := sharkauth.Permissions(fullTree)
// {"/api/user/list": ["系统管理.用户管理.用户列表"], ...}

// 扁平化存储（Redis/前端判断）
flat := sharkauth.Flatten(childTree)
// {"系统管理.用户管理.用户列表": 1}
```

### 错误处理 (`sharkerror`)

```go
import "github.com/xcodego/shark/sharkerror"

var ErrUserNotFound = sharkerror.New(10001, "用户不存在")
var ErrDBError      = sharkerror.New(20001, "数据库错误")

// 判断错误类型（基于 Code 比较）
if errors.Is(err, ErrUserNotFound) { /* ... */ }

// 附加数据
err := ErrUserNotFound.WithData(map[string]any{"user_id": 123}).
    WithMsg("指定的用户不存在")
// → {"code": 10001, "msg": "指定的用户不存在", "data": {"user_id": 123}}

// HTTP 中间件自动将 *Error 转为 JSON 响应
```

### gRPC 服务发现 (`sharkgrpc`)

```go
// 服务端：端口 > 0 启动监听
server := sharkgrpc.New(ctx, "myproject", rdb, logger, 50051)
pb.RegisterEchoServer(server.Server, &EchoServiceImpl{})

// 客户端：自动发现 + 负载均衡
conn, _ := server.GetRpcConnection("user-service")
client := pb.NewUserServiceClient(conn)
resp, _ := client.CreateOrder(ctx, &pb.CreateOrderReq{UserId: 12345})
```

### Redis (`sharkredis`)

```go
// 集群模式
cluster, _ := sharkredis.NewCluster(ctx, &sharkredis.Config{
    Host: []string{"redis1:6379", "redis2:6379"}, Password: "secret",
})
cluster.Set(ctx, "key", "value", 0)

// 单机模式
client, _ := sharkredis.NewClient(ctx, &sharkredis.Config{
    Host: []string{"127.0.0.1:6379"},
})
client.Get(ctx, "key").Result()

// 集群 scan（流式输出）
keysCh, errCh := sharkredis.ScanKeys(ctx, cluster, "user:*")
for key := range keysCh { fmt.Println(key) }

// 集群批量删除
sharkredis.DeleteKeys(ctx, cluster, "cache:*")
```

### Kafka (`sharkkafka`)

```go
sk, _ := sharkkafka.New(ctx, &sharkkafka.Config{
    Host: []string{"kafka:9092"}, User: "admin", Password: "secret",
}, logger)
defer sk.Close()

// 生产者
w, _ := sk.Writer("order-events")
w.WriteMessages(ctx, kafka.Message{Key: []byte("order-1"), Value: []byte(`{"status":"created"}`)})

// 批量消费者
sk.BatchConsumer("order-events", "order-group", func(msgs []kafka.Message) bool {
    for _, msg := range msgs {
        var order Order
        json.Unmarshal(msg.Value, &order)
        process(order)
    }
    return true  // 返回 false 停止消费
})
```

### RabbitMQ (`sharkrabbitmq`)

```go
mq, _ := sharkrabbitmq.New(ctx, logger, wg, &sharkrabbitmq.Config{
    Host: []string{"mq1:5672", "mq2:5672"}, User: "guest", Password: "guest",
}, "my-service", "1")

// 发布消息
type Order struct { ID int64 `json:"id"`; Status string `json:"status"` }
mq.Publish("orders", "created", Order{ID: 123, Status: "pending"})

// 批量消费
mq.BatchConsume("orders", "order-queue", "created", func(msgs []amqp.Delivery) bool {
    for _, msg := range msgs { process(msg.Body) }
    return true  // 整批 ack
})

// 单条消费（需手动 ack）
mq.Consume("orders", "order-queue", "created", func(msg amqp.Delivery) {
    process(msg.Body)
    msg.Ack(false)
})
```

### 定时器 (`sharktimer`)

```go
timer := sharktimer.NewTimer(ctx, "myproject", "order-timer", "inst-1", rdb, logger)

// 添加延迟任务
timerId := timer.AddTimer(30*time.Minute, func() { cancelOrder("12345") })
timer.RemoveTimer(timerId)  // 到期前取消

// 使用业务 ID
timer.AddTimeWithId("order:cancel:12345", 30*time.Minute, func() { cancelOrder("12345") })

// 默认回调（未注册回调的到期任务执行此函数）
timer.DefaultCallback(func(timerId string) {
    callback := loadCallbackFromDB(timerId)  // 从数据库加载回调配置
    callback()
})
```

### 通用函数 (`sharkfunc`)

```go
// 超时控制
err := sharkfunc.WithTimeout(ctx, 5*time.Second, func(ctx context.Context) error {
    return doSlowWork(ctx)
})
if errors.Is(err, sharkfunc.ErrTimeout) { /* 超时处理 */ }

// 并行执行
sharkfunc.ParallelCall(func() { loadUsers() }, func() { loadOrders() })

// Channel 批量排空（阻塞取首条 + 非阻塞尽可能多取）
msgs := sharkfunc.DrainChannelN(ctx, msgCh, 5000)

// 安全 panic 恢复
go func() {
    defer sharkfunc.Recover(logger, "worker")
    riskyOperation()
}()

// 泛型指针
name := sharkfunc.Pointer("hello")  // *string
```

### 工具函数 (`sharkutils`)

```go
// 随机数 [min, max)
n := sharkutils.RandNum(1, 100)

// MD5
hash := sharkutils.Md5([]byte("hello"))  // "5d41402abc4b2a76b9719d911017c592"

// 客户端 IP（支持 Nginx 代理穿透）
ip := sharkutils.GetClientIp(request)

// 密码加密与验证
hashed := sharkutils.BcryptHash("myPassword123")        // $2a$10$...
sharkutils.BcryptCheck(hashed, "myPassword123")          // true
```

### TOTP 两步验证 (`sharkverify`)

```go
// 生成密钥和二维码 URL（用户扫描绑定）
secret, qrUrl := sharkverify.NewSecret("MyCompany", "user@example.com")

// 验证 6 位验证码
if sharkverify.VerifyCode(secret, "123456") { /* 验证通过 */ }

// 根据已有密钥重新生成二维码 URL
qrUrl, _ := sharkverify.GetQrCodeUrl(secret, "MyCompany", "user@example.com")
```

### JSON 工具 (`sharkjson`)

```go
// 反序列化
user := sharkjson.ParseJsonBytes[User](jsonBytes)
user := sharkjson.ParseJsonString[User](jsonStr)

// 序列化
bytes := sharkjson.ToJsonBytes(map[string]int{"a": 1})
str := sharkjson.ToJsonString([]string{"apple", "banana"})  // `["apple","banana"]`
```

### Zlib 压缩 (`sharkzip`)

```go
compressed, _ := sharkzip.Compress([]byte("large data..."))
original, _ := sharkzip.Decompress(compressed)
```

### Elasticsearch (`sharkelastic`)

```go
es, _ := sharkelastic.New(ctx, &sharkelastic.Config{
    Host: []string{"http://es:9200"}, User: "elastic", Password: "secret",
})

// 创建索引
es.CreateIndex(ctx, "users", 2,
    sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
    sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
)

// 批量插入
es.Insert(ctx, "users", "user_id",
    map[string]any{"user_id": "1", "name": "张三", "age": 25},
    map[string]any{"user_id": "2", "name": "李四", "age": 30},
)

// 搜索
resp, _ := es.Search(ctx, "users", map[string]any{
    "query": map[string]any{"match": map[string]any{"name": "张三"}},
})
```

### 数据压缩 (`sharkzip`)

```go
compressed, _ := sharkzip.Compress([]byte("large data payload..."))
original, _ := sharkzip.Decompress(compressed)
```

## 架构设计

```
┌──────────────────────────────────────────────────────────┐
│                      config.yaml                          │
│  (http, grpc, db, redis, kafka, elastic, mongodb, ...)   │
└────────────────────────┬─────────────────────────────────┘
                         │
                  ┌──────▼──────┐
                  │  sharkapp   │  ← 加载配置、初始化所有中间件
                  │   App.Hunt  │  ← 启动业务模块（常驻，不以完整关闭中间件为目标）
                  └──────┬──────┘
                         │
         ┌───────────────┼───────────────┬───────────────┐
         │               │               │               │
    ┌────▼────┐    ┌─────▼─────┐   ┌─────▼─────┐   ┌────▼────┐
    │sharklog │    │ sharkhttp │   │ sharkgrpc │   │  业务   │
    │ 日志系统│    │ Gin HTTP  │   │ gRPC服务  │   │  模块   │
    └────┬────┘    └───────────┘   └─────┬─────┘   └────┬────┘
         │                              │              │
    ┌────▼────┐                    ┌─────▼─────┐   ┌───▼────┐
    │ Kafka   │                    │   Redis   │   │ 数据层  │
    │ (日志)  │                    │(服务发现) │   │ DB/ES  │
    └─────────┘                    └───────────┘   └────────┘
```

## 作者

xcodego

## 许可证

MIT License
