// Package sharkgrpc 提供 gRPC 服务监听和基于 Redis 的客户端服务发现。
//
// 服务端只负责监听，不向 Redis 注册自身（进程不知道外部可达地址）；
// 服务地址列表由外部写入 Redis，客户端通过 GetRpcConnection 读取并定时刷新。
package sharkgrpc

import (
	"context"
	"encoding/json"
	"errors"
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

// connections 按服务名缓存 *connection，进程内全局共享。
var connections sync.Map

// RpcRedis 定义 gRPC 服务发现所需的 Redis 操作。
type RpcRedis interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
}

// stopTimeout 是 Stop 等待在途请求处理完的最长时间，超时后强制停止。
const stopTimeout = 10 * time.Second

// connection 保存 gRPC 客户端连接及其地址解析器。
type connection struct {
	conn      *grpc.ClientConn
	resolver  *manual.Resolver
	addrs     atomic.Value  // 当前已生效的有序地址列表([]string)，用于判断地址是否变化
	owner     *Server       // 创建该连接的 Server，Close 只关闭自己创建的连接
	done      chan struct{} // 连接关闭时关闭，通知 updateResolver 退出
	closeOnce sync.Once
}

// close 关闭连接并通知地址刷新协程退出，可重复调用。
func (c *connection) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			c.conn.Close()
		}
	})
}

// Server 管理 gRPC 服务端及客户端连接。
type Server struct {
	ctx context.Context
	// Server 是底层 gRPC 服务端，供业务注册服务；port 不大于 0 时为 nil。
	Server  *grpc.Server
	logger  *zap.Logger
	redis   RpcRedis
	port    int
	sg      singleflight.Group
	project string
	closed  atomic.Bool // Close 之后为 true，GetRpcConnection 不再创建连接
}

// New 创建 gRPC 服务管理器；port 大于 0 时才创建服务端，否则仅可作为客户端使用。
func New(ctx context.Context, project string, redis RpcRedis, logger *zap.Logger, port int) *Server {
	s := &Server{
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

// Run 异步启动 gRPC 服务端监听；port 不大于 0 时不做任何事，监听失败只记录日志。
func (s *Server) Run() {
	if s.port <= 0 {
		return
	}
	go func() {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%v", s.port))
		if err != nil {
			if s.logger != nil {
				s.logger.Error("failed to listen", zap.Error(err))
			}
			return
		}
		err = s.Server.Serve(listener)
		// Stop 先于 Serve 执行时会返回 ErrServerStopped，属于正常关闭。
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) && s.logger != nil {
			s.logger.Error("failed to serve", zap.Error(err))
		}
	}()
}

// Stop 优雅停止 gRPC 服务端：不再接受新请求，等待在途请求处理完，最长 10 秒，超时后强制停止。
// port 不大于 0 时没有服务端，什么也不做。可重复调用。客户端连接不受影响，需要时调用 Close。
func (s *Server) Stop() {
	if s.Server == nil {
		return
	}
	stopped := make(chan struct{})
	go func() {
		s.Server.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(stopTimeout):
		if s.logger != nil {
			s.logger.Warn("grpc graceful stop timeout, force stop")
		}
		s.Server.Stop()
		<-stopped
	}
}

// Close 先 Stop 服务端，再关闭本 Server 通过 GetRpcConnection 创建的全部客户端连接，
// 并停止对应的地址刷新协程。可重复调用；关闭后 GetRpcConnection 返回错误。
func (s *Server) Close() {
	s.closed.Store(true)
	s.Stop()
	connections.Range(func(key, value any) bool {
		c := value.(*connection)
		if c.owner == s {
			connections.Delete(key)
			c.close()
		}
		return true
	})
}

// updateResolver 每 5 秒从 Redis 拉取地址列表，有变化时更新解析器；
// 随连接创建而启动，ctx 取消或连接被关闭时退出。
func (s *Server) updateResolver(name string) {
	v, ok := connections.Load(name)
	if !ok {
		return
	}
	c := v.(*connection)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-c.done:
			return
		case <-ticker.C:
		}
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
		addrs := []string{}
		err = json.Unmarshal([]byte(addr), &addrs)
		if err != nil {
			s.logger.Error("failed to unmarshal grpc address", zap.String("name", name), zap.String("addr", addr), zap.Error(err))
			continue
		}
		if len(addrs) == 0 {
			s.logger.Warn("grpc address is empty", zap.String("name", name))
		}
		// 排序后比较，地址集合未变化则不触发 UpdateState。
		var oldAddrs []string
		if v := c.addrs.Load(); v != nil {
			oldAddrs = append([]string(nil), v.([]string)...)
		}
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
				continue
			}
		}
		var resolverAddrs []resolver.Address = make([]resolver.Address, 0, len(addrs))
		for _, addr := range addrs {
			resolverAddrs = append(resolverAddrs, resolver.Address{Addr: addr})
		}
		c.resolver.UpdateState(resolver.State{Addresses: resolverAddrs})
		c.addrs.Store(append([]string{}, addrs...))
	}
}

// redisGrpcHost 返回服务地址列表的 Redis key：{project}:grpc:{name}，值为 JSON 字符串数组。
func (s *Server) redisGrpcHost(name string) string {
	return fmt.Sprintf("%v:grpc:%v", s.project, name)
}

// GetRpcConnection 获取或创建目标服务的 gRPC 连接，同名服务复用同一连接。
// 首次创建时从 Redis 读取地址；key 为空时写入 "[]" 作为初始化占位（供外部填充地址），并返回错误。
// 连接使用 round_robin 负载均衡和指数退避重连，并后台定时刷新地址。
func (s *Server) GetRpcConnection(name string) (*grpc.ClientConn, error) {
	if s.redis == nil {
		return nil, fmt.Errorf("GetRpcConnection redis未初始化")
	}
	if s.closed.Load() {
		return nil, fmt.Errorf("GetRpcConnection server已关闭")
	}
	conn, err, _ := s.sg.Do("grpc-conn-"+name, func() (any, error) {
		v, ok := connections.Load(name)
		if ok {
			return v.(*connection).conn, nil
		} else {
			addr, err := s.redis.Get(s.ctx, s.redisGrpcHost(name)).Result()
			if err != nil && err != redis.Nil {
				return nil, err
			}
			addr = strings.TrimSpace(addr)
			if addr == "" {
				// 初始化占位：让外部知道该 key 需要填充地址，不是自注册。
				s.redis.Set(s.ctx, s.redisGrpcHost(name), "[]", 0)
				return nil, fmt.Errorf("grpc地址未配置")
			}
			addrs := []string{}
			err = json.Unmarshal([]byte(addr), &addrs)
			if err != nil {
				return nil, fmt.Errorf("grpc地址配置错误")
			}
			if len(addrs) == 0 {
				return nil, fmt.Errorf("grpc地址未配置")
			}
			sort.Strings(addrs)
			c := &connection{
				resolver: manual.NewBuilderWithScheme("custom"),
				owner:    s,
				done:     make(chan struct{}),
			}
			c.addrs.Store(append([]string{}, addrs...))
			var resolverAddrs []resolver.Address = make([]resolver.Address, 0, len(addrs))
			for _, addr := range addrs {
				resolverAddrs = append(resolverAddrs, resolver.Address{Addr: addr})
			}
			c.resolver.UpdateState(resolver.State{Addresses: resolverAddrs})
			conn, err := grpc.NewClient(
				"custom:///svc",
				grpc.WithResolvers(c.resolver),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithConnectParams(grpc.ConnectParams{
					Backoff: backoff.Config{
						BaseDelay:  100 * time.Millisecond,
						Multiplier: 1.6,
						MaxDelay:   3 * time.Second,
					},
					MinConnectTimeout: 1 * time.Second,
				}),
				// 重试策略仅对 your.service 生效，业务需按实际服务名调整。
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
			connections.Store(name, c)
			go s.updateResolver(name)
			return conn, err
		}
	})
	if err != nil {
		return nil, err
	}
	return conn.(*grpc.ClientConn), nil
}
