// Package sharkgrpc 提供基于 Redis 服务发现的 gRPC 客户端/服务端连接管理。
//
// 核心设计：
//  1. 服务发现：服务端启动时将地址注册到 Redis，客户端从 Redis 读取目标地址列表
//  2. 动态地址更新：每个连接后台协程每 5 秒检查 Redis 中地址列表的变化，无缝切换
//  3. 连接复用：通过 singleflight 合并并发请求，避免重复创建同一服务的连接
//  4. 全局连接池：使用 sync.Map 存储所有已建立的 gRPC 连接，按服务名索引
//  5. 负载均衡：客户端使用 round_robin 策略在多个服务端实例间分配请求
//  6. 重试机制：内置重试策略（最多 3 次，指数退避），处理 UNAVAILABLE 和 RESOURCE_EXHAUSTED
//
// 服务发现流程：
//  1. 服务端启动时将自身地址（host:port）注册到 Redis key：{project}:grpc:{serviceName}
//  2. Redis value 为 JSON 数组，如：["10.0.0.1:50051","10.0.0.2:50051"]
//  3. 客户端 GetRpcConnection 时读取 Redis 获取初始地址列表
//  4. 后台协程 updateResolver 每 5 秒轮询 Redis，检测地址变化并更新解析器
//  5. gRPC 内置的 round_robin 负载均衡器自动在选择新地址
//
// 典型使用场景：
//   - 微服务间 gRPC 调用（如 A 服务调用 B 服务的 gRPC 接口）
//   - 滚动更新时客户端自动发现新实例并摘除旧实例
//   - 多实例部署时自动负载均衡
//
// 使用示例（服务端）：
//
//	// 创建 RPC 服务端
//	rpcServer := sharkgrpc.New(ctx, "myproject", rdb, logger, 50051)
//
//	// 注册 gRPC 服务
//	pb.RegisterMyServiceServer(rpcServer.Server, &myServiceImpl{})
//
//	// 将本机地址注册到 Redis（由业务方自行实现）
//	// rdb.Set(ctx, "myproject:grpc:my-service", `["10.0.0.1:50051"]`, 0)
//
// 使用示例（客户端）：
//
//	// 获取到目标服务的连接（自动服务发现 + 负载均衡）
//	conn, err := rpcServer.GetRpcConnection("order-service")
//	if err != nil {
//	    log.Fatalf("获取连接失败: %v", err)
//	}
//	client := pb.NewOrderServiceClient(conn)
//	resp, err := client.CreateOrder(ctx, &pb.CreateOrderReq{...})
package sharkgrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/sync/singleflight"

	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
)

// connections 是全局 gRPC 连接池，以服务名为 key 存储 *connection 实例。
// 使用 sync.Map 确保并发安全，支持多个 RpcServer 实例共享连接。
// 注意：服务名为逻辑名称（如 "order-service"），与 Redis 中的注册名称一致。
var connections sync.Map

// RpcRedis 定义了 gRPC 服务发现所需的 Redis 操作接口。
//
// 使用者可以传入 *redis.Client 或自定义的 mock 实现进行测试。
//
// 方法说明：
//   - Get: 读取服务地址列表（JSON 数组格式）
//   - Set: 写入服务地址列表
type RpcRedis interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
}

// connection 表示一个 gRPC 客户端连接，包含底层连接和动态地址解析器。
//
// 字段说明：
//   - conn: gRPC 客户端连接（复用于多次 RPC 调用）
//   - resolver: 手动解析器，支持运行时动态更新后端地址列表
//   - addrs: 当前生效的地址列表（atomic 存储，用于变更检测）
//
// 注意：该结构体未导出，仅供包内使用。
type connection struct {
	conn     *grpc.ClientConn // gRPC 客户端连接实例
	resolver *manual.Resolver // 手动解析器，通过 UpdateState 动态更新地址
	addrs    atomic.Value     // 原子存储当前地址列表 []string，用于对比是否变更
}

// RpcServer 是 gRPC 服务管理器的核心结构体。
//
// 一个 RpcServer 实例可以同时作为 gRPC 服务端（通过 Server 字段注册服务）
// 和 gRPC 客户端（通过 GetRpcConnection 获取到其他服务的连接）。
//
// 字段说明：
//   - ctx:   上下文，用于控制后台协程生命周期
//   - Server: gRPC 服务端实例（port > 0 时创建）
//   - logger: zap 日志记录器
//   - redis:  Redis 接口，用于服务发现（读写地址列表）
//   - port:   服务端监听端口（0 表示仅作为客户端）
//   - sg:     singleflight 组，用于合并并发的 GetRpcConnection 请求
//   - project: 项目名，用作 Redis key 前缀
//
// 零值 RpcServer 不可直接使用，必须通过 New() 创建。
type RpcServer struct {
	ctx     context.Context    // 上下文，控制后台协程生命周期
	Server  *grpc.Server       // gRPC 服务端实例，通过此字段注册 protobuf 服务
	logger  *zap.Logger        // zap 日志记录器
	redis   RpcRedis           // Redis 接口，用于服务发现
	port    int                // 服务端监听端口（0 表示仅作为客户端）
	sg      singleflight.Group // 合并并发请求，避免重复创建同一服务的连接
	project string             // 项目名，用作 Redis key 前缀
}

// New 创建一个 RpcServer 实例。
//
// 参数：
//   - ctx:     上下文，用于控制后台协程生命周期
//   - project: 项目名，用于构造 Redis key 前缀（如 "myproject:grpc:service-name"）
//   - redis:   Redis 接口（可为 nil，此时仅可作服务端使用，GetRpcConnection 会失败）
//   - logger:  zap 日志记录器（可为 nil）
//   - port:    服务端监听端口（0 表示仅作为客户端，不启动 gRPC Server）
//
// 行为：
//   - 当 port > 0 时，自动创建 grpc.Server 并启动监听（1 秒延迟启动）
//   - 当 port == 0 时，仅作为客户端使用（Server 字段为 nil）
//
// 使用示例：
//
//	// 作为服务端 + 客户端
//	server := sharkgrpc.New(ctx, "myproject", rdb, logger, 50051)
//
//	// 注册 protobuf 服务
//	pb.RegisterEchoServer(server.Server, &EchoServiceImpl{})
//
//	// 获取到其他服务的连接
//	conn, _ := server.GetRpcConnection("user-service")
//	client := pb.NewUserServiceClient(conn)
//
//	// 仅作为客户端
//	clientOnly := sharkgrpc.New(ctx, "myproject", rdb, logger, 0)
//	conn, _ := clientOnly.GetRpcConnection("payment-service")
func New(ctx context.Context, project string, redis RpcRedis, logger *zap.Logger, port int) *RpcServer {
	s := &RpcServer{
		ctx:     ctx,
		logger:  logger,
		redis:   redis,
		port:    port,
		project: project,
	}
	if port > 0 {
		s.Server = grpc.NewServer()
	}
	return s
}

// run 启动 gRPC 服务端监听。
//
// 延迟 1 秒启动以确保其他初始化完成。
// 在指定的端口上创建 TCP 监听，并启动 gRPC Server。
// serve 失败时记录错误日志（调用方需通过其他手段感知服务不可用）。
func (s *RpcServer) Run() {
	if s.port <= 0 {
		return
	}
	go func() {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%v", s.port))
		if err != nil {
			s.logger.Error("failed to listen", zap.Error(err))
		}
		err = s.Server.Serve(listener)
		if err != nil {
			s.logger.Error("failed to serve", zap.Error(err))
		}
	}()
}

// updateResolver 是后台地址更新协程。
//
// 每 5 秒从 Redis 读取目标服务的地址列表，与本地缓存的地址对比：
//   - 若地址列表无变化：跳过更新，等待下一轮
//   - 若地址列表有变化：调用 resolver.UpdateState 将新地址列表推送给 gRPC
//
// 这使客户端能够自动感知服务端的扩缩容、滚动更新等变化，无需重启。
//
// 参数：
//   - name: 服务名（逻辑名称，如 "order-service"）
//
// 工作流程：
//  1. 从 connections 全局连接池中查找对应服务的连接
//  2. 循环：每隔 5 秒从 Redis 读取地址列表
//  3. 对比新旧地址列表（排序后逐元素比较）
//  4. 若地址变化，通过 manual.Resolver.UpdateState 推送新地址
func (s *RpcServer) updateResolver(name string) {
	v, ok := connections.Load(name)
	if !ok {
		return
	}
	c := v.(*connection)
	for {
		time.Sleep(5 * time.Second)
		// 从 Redis 读取最新的服务地址列表
		addr, err := s.redis.Get(s.ctx, s.redisGrpcHost(name)).Result()
		if err != nil {
			s.logger.Error("failed to get grpc address from redis", zap.String("name", name), zap.Error(err))
			continue
		}
		addr = strings.TrimSpace(addr)
		if addr == "" {
			s.logger.Warn("grpc address is empty", zap.String("name", name))
			continue
		}
		// 解析 JSON 格式的地址列表
		addrs := []string{}
		err = json.Unmarshal([]byte(addr), &addrs)
		if err != nil {
			s.logger.Error("failed to unmarshal grpc address", zap.String("name", name), zap.String("addr", addr), zap.Error(err))
			continue
		}
		if len(addrs) == 0 {
			s.logger.Warn("grpc address is empty", zap.String("name", name))
		}
		// 获取上一次存储的地址列表
		var oldAddrs []string
		if v := c.addrs.Load(); v != nil {
			oldAddrs = append([]string(nil), v.([]string)...)
		}
		// 排序后逐元素比较（避免顺序差异导致误判）
		sort.Strings(addrs)
		sort.Strings(oldAddrs)
		if len(addrs) == len(oldAddrs) {
			same := true
			for i := range addrs {
				if addrs[i] != oldAddrs[i] {
					same = false
					break
				}
			}
			if same {
				// 地址列表无变化，跳过本次更新
				continue
			}
		}
		// 地址列表有变化：构建 resolver.Address 并推送
		var resolverAddrs []resolver.Address = make([]resolver.Address, 0, len(addrs))
		for _, addr := range addrs {
			resolverAddrs = append(resolverAddrs, resolver.Address{Addr: addr})
		}
		c.resolver.UpdateState(resolver.State{Addresses: resolverAddrs})
		// 更新本地缓存
		c.addrs.Store(append([]string{}, addrs...))
	}
}

// redisGrpcHost 构造 Redis 中存储 gRPC 服务地址列表的 key。
//
// 格式：{project}:grpc:{name}
//
// 示例：
//
//	// project="myapp", name="order-service"
//	// 返回: "myapp:grpc:order-service"
func (s *RpcServer) redisGrpcHost(name string) string {
	return fmt.Sprintf("%v:grpc:%v", s.project, name)
}

// GetRpcConnection 获取或创建到目标 gRPC 服务的客户端连接。
//
// 使用 singleflight 机制确保同一服务名的并发请求只创建一次连接。
// 已创建的连接存储在全局 connections 池中，后续请求直接复用。
//
// 参数：
//   - name: 目标服务名（逻辑名称，与 Redis 中的注册名称一致）
//
// 返回值：
//   - *grpc.ClientConn: 可复用的 gRPC 客户端连接
//   - error: 获取失败时返回错误（Redis 未初始化 / 地址未配置 / 地址解析失败 / 连接创建失败）
//
// 连接配置：
//   - 传输安全：insecure（内网通信不启用 TLS）
//   - 负载均衡：round_robin（轮询）
//   - 连接重试：指数退避（BaseDelay=100ms, Multiplier=1.6, MaxDelay=3s）
//   - 连接超时：1 秒
//   - RPC 重试：最多 3 次，初始退避 50ms，最大退避 500ms
//   - 可重试状态码：UNAVAILABLE、RESOURCE_EXHAUSTED
//
// 使用示例：
//
//	server := sharkgrpc.New(ctx, "myapp", rdb, logger, 50051)
//
//	// 获取到订单服务的连接（自动服务发现 + 负载均衡）
//	conn, err := server.GetRpcConnection("order-service")
//	if err != nil {
//	    log.Fatalf("无法连接到订单服务: %v", err)
//	}
//	defer conn.Close() // 通常不需要关闭，连接在应用生命周期内复用
//
//	// 创建 gRPC 客户端桩
//	orderClient := pb.NewOrderServiceClient(conn)
//
//	// 发起 RPC 调用（内置重试 + 负载均衡）
//	resp, err := orderClient.CreateOrder(ctx, &pb.CreateOrderReq{
//	    UserId: 12345,
//	    Amount: 99.99,
//	})
//	if err != nil {
//	    log.Printf("创建订单失败: %v", err)
//	}
func (s *RpcServer) GetRpcConnection(name string) (*grpc.ClientConn, error) {
	if s.redis == nil {
		return nil, fmt.Errorf("GetRpcConnection redis未初始化")
	}
	// 使用 singleflight 合并并发请求：同一服务名的并发调用只执行一次连接创建逻辑
	conn, err, _ := s.sg.Do("grpc-conn-"+name, func() (any, error) {
		// 检查全局连接池中是否已有该服务的连接
		v, ok := connections.Load(name)
		if ok {
			// 已存在连接：直接返回复用
			return v.(*connection).conn, nil
		} else {
			// 不存在连接：从 Redis 读取地址列表并创建新连接
			addr, err := s.redis.Get(s.ctx, s.redisGrpcHost(name)).Result()
			if err != nil && err != redis.Nil {
				return nil, err
			}
			addr = strings.TrimSpace(addr)
			if addr == "" {
				// 地址为空：写入空数组占位，避免后续请求反复查询
				s.redis.Set(s.ctx, s.redisGrpcHost(name), "[]", 0)
				return nil, fmt.Errorf("grpc地址未配置")
			}
			// 解析 JSON 格式的地址列表
			addrs := []string{}
			err = json.Unmarshal([]byte(addr), &addrs)
			if err != nil {
				return nil, fmt.Errorf("grpc地址配置错误")
			}
			if len(addrs) == 0 {
				return nil, fmt.Errorf("grpc地址未配置")
			}
			sort.Strings(addrs)
			// 创建连接结构体，使用 manual resolver 支持动态地址更新
			c := &connection{
				resolver: manual.NewBuilderWithScheme("custom"),
			}
			c.addrs.Store(append([]string{}, addrs...))
			// 构建初始解析器地址列表
			var resolverAddrs []resolver.Address = make([]resolver.Address, 0, len(addrs))
			for _, addr := range addrs {
				resolverAddrs = append(resolverAddrs, resolver.Address{Addr: addr})
			}
			c.resolver.UpdateState(resolver.State{Addresses: resolverAddrs})
			// 创建 gRPC 连接，配置负载均衡和重试策略
			conn, err := grpc.NewClient(
				"custom:///svc",
				grpc.WithResolvers(c.resolver),
				grpc.WithTransportCredentials(insecure.NewCredentials()), // 内网通信不启用 TLS
				grpc.WithConnectParams(grpc.ConnectParams{
					Backoff: backoff.Config{
						BaseDelay:  100 * time.Millisecond, // 首次重试延迟 100ms
						Multiplier: 1.6,                    // 退避乘数
						MaxDelay:   3 * time.Second,        // 最大退避延迟 3s
					},
					MinConnectTimeout: 1 * time.Second, // 最小连接超时 1s
				}),
				grpc.WithDefaultServiceConfig(`{
  "loadBalancingPolicy":"round_robin",
  "methodConfig":[{
    "name":[{"service":"your.service"}],
    "retryPolicy":{
      "MaxAttempts":3,
      "InitialBackoff":"0.05s",
      "MaxBackoff":"0.5s",
      "BackoffMultiplier":1.6,
      "RetryableStatusCodes":[
        "UNAVAILABLE",
        "RESOURCE_EXHAUSTED"
      ]
    }
  }]
}`),
			)
			c.conn = conn
			// 将连接存入全局连接池
			connections.Store(name, c)
			// 启动后台协程定期更新地址列表
			go s.updateResolver(name)
			return conn, err
		}
	})
	if err != nil {
		return nil, err
	}
	return conn.(*grpc.ClientConn), nil
}
