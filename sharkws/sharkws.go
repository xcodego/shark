// Package sharkws 提供挂在 Gin 上的 WebSocket 连接管理。
//
// 连接共用带缓冲的收发队列，回调在同一接收协程中同步运行；队列满或回调阻塞会暂停读取。
// 回调应尽快返回，耗时任务放到业务 goroutine。避免对同一连接并发调用 Close 和 Send。
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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// SharkWS 管理 Gin 路由上的 WebSocket 连接和消息回调。
type SharkWS struct {
	index                    atomic.Int64
	router                   *gin.Engine
	connects                 sync.Map
	connect_callback         sync.Map     // 连接建立回调
	close_callback           sync.Map     // 连接关闭回调,主动关闭,不会触发回调
	message_callback         sync.Map     // 消息回调
	default_message_callback atomic.Value // func(conn int, path string, message []byte)；与 recv 协程并发安全
	send_channel             chan []any   // 全局发送队列，满则 Send 阻塞；业务勿在回调里长时间堵这里
	recv_channel             chan []any   // 全局接收队列，满则读循环阻塞；回调必须快返回
	looping                  bool
}

// Connection 保存 WebSocket 连接、路由参数、查询参数和客户端 IP。
type Connection struct {
	Conn   *websocket.Conn
	Params map[string]string
	Query  url.Values
	Ip     string
}

// NewSharkWS 创建 WebSocket 管理器并启动收发队列处理协程。
func NewSharkWS(router *gin.Engine) *SharkWS {
	ws := &SharkWS{
		router:       router,
		send_channel: make(chan []any, 10000),
		recv_channel: make(chan []any, 10000),
	}
	// 启动发送协程
	go func(s *SharkWS) {
		for msg := range s.send_channel {
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
	// 启动接收协程
	go func(s *SharkWS) {
		for msg := range s.recv_channel {
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

// Listen 将 path 注册为 WebSocket 路由。
func (s *SharkWS) Listen(path string) {
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
		s.recv_channel <- []any{1, path, idx} // 通道满会阻塞本连接读循环，回调必须快
		for {
			_, message, err := wsconn.ReadMessage()
			if err != nil {
				break
			}
			s.recv_channel <- []any{3, path, idx, message} // 同上，勿在 OnMessage 里做重活
		}
		_, ok := s.connects.Load(idx)
		if ok {
			s.connects.Delete(idx)
			s.recv_channel <- []any{2, path, idx}
		}
	})
}

// GetConnects 返回当前连接 ID 列表。
func (s *SharkWS) GetConnects() []int {
	conns := []int{}
	s.connects.Range(func(key, value any) bool {
		conns = append(conns, key.(int))
		return true
	})
	return conns
}

// OnConnect 设置 path 对应的连接建立回调。
func (s *SharkWS) OnConnect(path string, callback func(conn int)) {
	s.connect_callback.Store(path, callback) // 在 recv 协程同步执行，必须尽快返回
}

// RemoteIp 返回客户端 IP；连接不存在时返回空字符串。
func (s *SharkWS) RemoteIp(conn int) string {
	c, ok := s.connects.Load(conn)
	if !ok {
		return ""
	}
	connection := c.(*Connection)
	return connection.Ip
}

// Param 返回路由参数；连接或参数不存在时返回空字符串。
func (s *SharkWS) Param(conn int, key string) string {
	c, ok := s.connects.Load(conn)
	if !ok {
		return ""
	}
	connection := c.(*Connection)
	return connection.Params[key]
}

// Query 返回查询参数；连接或参数不存在时返回空字符串。
func (s *SharkWS) Query(conn int, key string) string {
	c, ok := s.connects.Load(conn)
	if !ok {
		return ""
	}
	connection := c.(*Connection)

	return connection.Query.Get(key)
}

// Close 关闭并移除指定连接；关闭后不要再向该连接发送消息。
func (s *SharkWS) Close(conn int) {
	c, ok := s.connects.Load(conn)
	if !ok {
		return
	}
	s.connects.Delete(conn)
	connection := c.(*Connection)
	// 与 send 协程 WriteMessage 可能并发；业务不要边 Close 边 Send。
	connection.Conn.Close()
}

// OnClose 设置 path 的断开回调；主动调用 Close 不会触发该回调。
func (s *SharkWS) OnClose(path string, callback func(conn int)) {
	s.close_callback.Store(path, callback) // 被动断开时在 recv 协程执行，必须尽快返回
}

// OnMessage 设置 path 的消息回调。
func (s *SharkWS) OnMessage(path string, callback func(conn int, message []byte)) {
	s.message_callback.Store(path, callback) // 在 recv 协程同步执行，必须尽快返回
}

// DefaultMessage 设置未配置 path 专属回调时使用的默认消息回调。
func (s *SharkWS) DefaultMessage(callback func(conn int, path string, message []byte)) {
	if callback == nil {
		return
	}
	s.default_message_callback.Store(callback) // 在 recv 协程同步执行，必须尽快返回
}

// SendText 将文本消息加入发送队列；队列满时会阻塞。
func (s *SharkWS) SendText(conn int, text string) {
	_, ok := s.connects.Load(conn)
	if !ok {
		return
	}
	s.send_channel <- []any{conn, websocket.TextMessage, []byte(text)} // 通道满会阻塞调用方
}

// SendBytes 将二进制消息加入发送队列；队列满时会阻塞。
func (s *SharkWS) SendBytes(conn int, data []byte) {
	_, ok := s.connects.Load(conn)
	if !ok {
		return
	}
	s.send_channel <- []any{conn, websocket.BinaryMessage, data} // 通道满会阻塞调用方
}

// BroadcastText 向当前所有连接广播文本消息。
func (s *SharkWS) BroadcastText(text string) {
	conns := s.GetConnects()
	for _, conn := range conns {
		s.SendText(conn, text)
	}
}

// BroadcastBytes 向当前所有连接广播二进制消息。
func (s *SharkWS) BroadcastBytes(data []byte) {
	conns := s.GetConnects()
	for _, conn := range conns {
		s.SendBytes(conn, data)
	}
}
