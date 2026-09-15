# Shark

一个轻量级、模块化的 Go 微服务开发框架，内置了十余种基础设施组件的连接管理和实用工具。

## 项目简介

Shark 封装了微服务开发中常见的中间件和工具库，提供统一的配置加载（`config.yaml`）、连接管理和最佳实践。各子包均可独立使用，无需依赖完整的框架上下文。

**模块路径：** `github.com/lornshark/shark`

**Go 版本要求：** 1.26+

## 特性

- **统一的配置管理** — 所有组件通过 `config.yaml` 统一配置，支持 json/yaml/mapstructure 标签 + 环境变量覆盖
- **双通道日志** — 控制台（Console Encoder）+ Kafka（JSON Encoder），通过 `zapcore.NewTee` 合并，Snowflake 生成每条日志唯一 ID
- **内置服务发现** — gRPC 基于 Redis 的服务注册与发现，支持动态地址更新、round_robin 负载均衡和指数退避重试
- **高性能 ID 生成** — 改进型 Snowflake 算法，41位时间戳 + 19位序列号，每秒 52 万个 ID，int64 类型前端安全
- **SQL 条件构建器** — 流式 API 构建参数化 WHERE/JOIN ON 子句，空值自动跳过，防 SQL 注入，支持 AND/OR 任意嵌套、字段对字段比较
- **Keyset 游标分页** — 泛型表扫描器，深分页性能不受数据量影响，支持双向翻页（Next/Prev）和 Excel 流式导出
- **高精度数值** — 基于 shopspring/decimal 的类型归一化（10+ 种），Round 消浮点误差 + Truncate 截断到指定位数
- **批量消费者** — Kafka/RabbitMQ 批量拉取 + 管道缓冲 + 自动提交 offset，支持重试和优雅停止
- **定时器** — Redis ZSet 轻量级单机定时器，误差 ±1s，ants 协程池异步执行回调，支持默认回调
- **泛型工具** — PageQuery 分页、TableScan 扫描、WithTimeout 超时控制、DrainChannelN 管道排空、Pointer 指针安全
- **多层缓存防击穿** — 基于 singleflight + seeker 链的泛型缓存穿透防护（本地缓存 → Redis → DB 多级回退）
- **RBAC 权限树** — 多叉树权限模型，支持父子角色继承、权限裁剪、Sync 生成编辑树、Flatten/Permissions 高效查表

## 快速开始

### 安装

```bash
go get github.com/lornshark/shark
```

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

import "github.com/lornshark/shark/sharkapp"

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

## 核心模块详解

### 应用启动 (`sharkapp`)

`App` 是所有中间件客户端的聚合体，由 `New` 工厂函数根据 `Options` 自动初始化所有已配置的中间件。

```go
package main

import (
    "github.com/lornshark/shark/sharkapp"
    "github.com/lornshark/shark/sharkdb"
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
import sd "github.com/lornshark/shark/sharkdecimal"

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
import "github.com/lornshark/shark/sharkcache"

// seeker 链：本地缓存 → Redis → DB（任一命中即返回）
cache := sharkcache.New[User](
    func(args ...any) (*User, error) { return localCache.Get(args[0].(int64)) },
    func(args ...any) (*User, error) { return redis.GetUser(args[0].(int64)) },
    func(args ...any) (*User, error) { return db.FindUser(args[0].(int64)) },
)

user, err := cache.Get(userId)  // singleflight 防击穿 + 多级回退
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
import "github.com/lornshark/shark/sharkerror"

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
timer := sharktimer.NewTimer(ctx, "myproject", "order-timer", "inst-1", rdb)

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
                  │   App.Hunt  │  ← 启动业务模块 + 优雅关闭
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

Lornshark

## 许可证

MIT License