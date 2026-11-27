// Package sharkws 提供挂在 Gin 上的 WebSocket 连接管理。
//
// 所有连接共用带缓冲的收发队列。连接、断开和消息回调在同一接收协程中同步执行，
// 队列满或回调阻塞会暂停读取。回调应尽快返回，耗时任务放到业务 goroutine。
// 不要对同一连接并发调用 Close 和 Send。
package sharkws

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// upgrader 将 HTTP 连接升级为 WebSocket，CheckOrigin 始终放行。
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Server 管理 Gin 路由上的 WebSocket 连接和消息回调。
type Server struct {
	index                    atomic.Int64  // 连接 ID 计数，Listen 时从 1 递增
	router                   *gin.Engine   // 注册路由的 Gin 引擎
	connects                 sync.Map      // 连接 ID → *Connection
	connect_callback         sync.Map      // path → func(conn int)，连接建立回调
	close_callback           sync.Map      // path → func(conn int)，仅被动断开时触发
	message_callback         sync.Map      // path → func(conn int, message []byte)
	default_message_callback atomic.Value  // func(conn int, path string, message []byte)，path 无专属回调时使用
	send_channel             chan []any    // 发送队列，元素为 [conn, messageType, data]；满则 Send 阻塞
	recv_channel             chan []any    // 接收队列，元素见 NewServer 的接收协程；满则对应连接的读循环阻塞
	looping                  bool          // 保留字段，当前未使用
	done                     chan struct{} // CloseAll 时关闭，通知收发协程退出、阻塞的入队立即返回
	closeOnce                sync.Once
}

// Connection 保存一条 WebSocket 连接及其握手信息。
type Connection struct {
	Conn   *websocket.Conn   // 底层连接
	Params map[string]string // Gin 路由参数
	Query  url.Values        // URL 查询参数
	Ip     string            // 客户端 IP
}

// NewServer 创建 Server 并启动发送、接收两个协程。收发队列容量均为 10000。
//
// 接收协程按入队顺序同步执行回调，事件格式为：
//   - [1, path, conn]：连接建立，触发 OnConnect
//   - [2, path, conn]：被动断开，触发 OnClose
//   - [3, path, conn, message]：收到消息，优先触发 OnMessage，否则 DefaultMessage
func NewServer(router *gin.Engine) *Server {
	ws := &Server{
		router:       router,
		send_channel: make(chan []any, 10000),
		recv_channel: make(chan []any, 10000),
		done:         make(chan struct{}),
	}
	// 发送协程：连接已关闭则跳过；CloseAll 后退出，队列中未发送的消息丢弃
	go func(s *Server) {
		for {
			var msg []any
			select {
			case <-s.done:
				return
			case msg = <-s.send_channel:
			}
			idx := msg[0].(int)
			msgType := msg[1].(int)
			data := msg[2].([]byte)
			c, ok := s.connects.Load(idx)
			if !ok {
				continue
			}
			connection := c.(*Connection)
			connection.Conn.WriteMessage(msgType, data)
		}
	}(ws)
	// 接收协程：回调在本协程同步执行，阻塞会拖住全部连接的消息处理
	// CloseAll 后退出，队列中未处理的事件丢弃；正在执行的回调会先执行完
	go func(s *Server) {
		for {
			var msg []any
			select {
			case <-s.done:
				return
			case msg = <-s.recv_channel:
			}
			switch msg[0].(int) {
			case 1: // connect
				path := msg[1].(string)
				idx := msg[2].(int)
				v, ok := s.connect_callback.Load(path)
				if ok {
					callback := v.(func(conn int))
					callback(idx)
				}
			case 2: // close
				path := msg[1].(string)
				idx := msg[2].(int)
				v, ok := s.close_callback.Load(path)
				if ok {
					callback := v.(func(conn int))
					callback(idx)
				}
			case 3: // message
				path := msg[1].(string)
				idx := msg[2].(int)
				message := msg[3].([]byte)
				v, ok := s.message_callback.Load(path)
				if ok {
					callback := v.(func(conn int, message []byte))
					callback(idx, message)
				} else if cb, ok := s.default_message_callback.Load().(func(conn int, path string, message []byte)); ok && cb != nil {
					cb(idx, path, message)
				}
			}
		}
	}(ws)
	return ws
}

// Listen 将 path 注册为 GET WebSocket 路由。
//
// 建连后分配递增 ID 并投递连接事件。之后循环读消息并投递；
// 读失败时，若连接仍在表中则移除并投递断开事件。主动 Close 已移除的连接不会再次触发 OnClose。
// 客户端 IP 依次取 X-Forwarded-For 的第一段、X-Real-Ip、RemoteAddr。
func (s *Server) Listen(path string) {
	s.router.GET(path, func(c *gin.Context) {
		wsconn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		defer wsconn.Close()
		idx := int(s.index.Add(1))
		params := make(map[string]string)
		for _, p := range c.Params {
			params[p.Key] = p.Value
		}
		query := c.Request.URL.Query()
		ip := c.Request.Header.Get("X-Forwarded-For")
		if ip == "" {
			ip = c.Request.Header.Get("X-Real-Ip")
		}
		if ip == "" {
			ip, _, _ = net.SplitHostPort(c.Request.RemoteAddr)
		} else {
			ip = strings.Split(ip, ",")[0]
		}
		ip = strings.TrimSpace(ip)
		conn := &Connection{
			Conn:   wsconn,
			Params: params,
			Query:  query,
			Ip:     ip,
		}
		s.connects.Store(idx, conn)
		// 入队失败说明 Server 已 CloseAll：移除连接并结束，由 defer 关闭 wsconn
		if !s.enqueue(s.recv_channel, []any{1, path, idx}) { // 1: 连接建立；队列满则阻塞本连接
			s.connects.Delete(idx)
			return
		}
		for {
			_, message, err := wsconn.ReadMessage()
			if err != nil {
				break
			}
			if !s.enqueue(s.recv_channel, []any{3, path, idx, message}) { // 3: 收到消息
				s.connects.Delete(idx)
				return
			}
		}
		_, ok := s.connects.Load(idx)
		if ok {
			s.connects.Delete(idx)
			s.enqueue(s.recv_channel, []any{2, path, idx}) // 2: 被动断开
		}
	})
}

// enqueue 向队列投递一个事件；队列满时阻塞。Server 已 CloseAll 时放弃投递并返回 false。
func (s *Server) enqueue(ch chan []any, v []any) bool {
	select {
	case <-s.done:
		return false
	default:
	}
	select {
	case ch <- v:
		return true
	case <-s.done:
		return false
	}
}

// CloseAll 关闭全部连接并停止收发协程，可重复调用。
//
// 不触发 OnClose，队列中未处理的接收事件和未发送的消息会被丢弃，正在执行的回调会先执行完。
// 之后 SendText、SendBytes、Broadcast* 不再有效果，也不会阻塞。
// 同样不要与 Send 并发调用。路由仍挂在 Gin 上，应先停止 HTTP 服务再调用。
func (s *Server) CloseAll() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.connects.Range(func(key, value any) bool {
			s.connects.Delete(key)
			value.(*Connection).Conn.Close()
			return true
		})
	})
}

// GetConnects 返回当前连接 ID，顺序不固定。
func (s *Server) GetConnects() []int {
	conns := []int{}
	s.connects.Range(func(key, value any) bool {
		conns = append(conns, key.(int))
		return true
	})
	return conns
}

// OnConnect 设置 path 的连接建立回调，重复设置会覆盖。
// 回调在接收协程中同步执行，应尽快返回。
func (s *Server) OnConnect(path string, callback func(conn int)) {
	s.connect_callback.Store(path, callback)
}

// RemoteIp 返回客户端 IP；连接不存在时返回空字符串。
func (s *Server) RemoteIp(conn int) string {
	c, ok := s.connects.Load(conn)
	if !ok {
		return ""
	}
	connection := c.(*Connection)
	return connection.Ip
}

// Param 返回路由参数；连接或参数不存在时返回空字符串。
func (s *Server) Param(conn int, key string) string {
	c, ok := s.connects.Load(conn)
	if !ok {
		return ""
	}
	connection := c.(*Connection)
	return connection.Params[key]
}

// Query 返回查询参数；连接或参数不存在时返回空字符串。
func (s *Server) Query(conn int, key string) string {
	c, ok := s.connects.Load(conn)
	if !ok {
		return ""
	}
	connection := c.(*Connection)

	return connection.Query.Get(key)
}

// Close 关闭并移除连接，不触发 OnClose。
// 关闭后不要再 Send。不要与 Send 并发调用，发送协程的 WriteMessage 与 Close 会竞态。
func (s *Server) Close(conn int) {
	c, ok := s.connects.Load(conn)
	if !ok {
		return
	}
	s.connects.Delete(conn)
	connection := c.(*Connection)
	connection.Conn.Close()
}

// OnClose 设置 path 的被动断开回调，重复设置会覆盖。主动 Close 不触发。
// 回调在接收协程中同步执行，应尽快返回。
func (s *Server) OnClose(path string, callback func(conn int)) {
	s.close_callback.Store(path, callback)
}

// OnMessage 设置 path 的消息回调，重复设置会覆盖。设置后该 path 不再走 DefaultMessage。
// 回调在接收协程中同步执行，应尽快返回；在其中 Send 且发送队列已满时，会阻塞全部连接的消息处理。
func (s *Server) OnMessage(path string, callback func(conn int, message []byte)) {
	s.message_callback.Store(path, callback)
}

// DefaultMessage 设置默认消息回调，仅在该 path 未设置 OnMessage 时使用。
// callback 为 nil 时忽略，不会清除已有回调。回调在接收协程中同步执行，应尽快返回。
func (s *Server) DefaultMessage(callback func(conn int, path string, message []byte)) {
	if callback == nil {
		return
	}
	s.default_message_callback.Store(callback)
}

// SendText 将文本消息放入发送队列。连接不存在时直接返回；队列满时阻塞调用方。
func (s *Server) SendText(conn int, text string) {
	_, ok := s.connects.Load(conn)
	if !ok {
		return
	}
	s.enqueue(s.send_channel, []any{conn, websocket.TextMessage, []byte(text)})
}

// SendBytes 将二进制消息放入发送队列。连接不存在时直接返回；队列满时阻塞调用方。
// data 在发送完成前不要修改。
func (s *Server) SendBytes(conn int, data []byte) {
	_, ok := s.connects.Load(conn)
	if !ok {
		return
	}
	s.enqueue(s.send_channel, []any{conn, websocket.BinaryMessage, data})
}

// BroadcastText 向调用时已存在的全部连接发送文本。
// 队列满时阻塞；发送过程中新建的连接不会收到。
func (s *Server) BroadcastText(text string) {
	conns := s.GetConnects()
	for _, conn := range conns {
		s.SendText(conn, text)
	}
}

// BroadcastBytes 向调用时已存在的全部连接发送二进制数据。
// 队列满时阻塞；发送过程中新建的连接不会收到。data 在全部发送完成前不要修改。
func (s *Server) BroadcastBytes(data []byte) {
	conns := s.GetConnects()
	for _, conn := range conns {
		s.SendBytes(conn, data)
	}
}
