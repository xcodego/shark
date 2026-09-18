// Package sharkws 提供挂在 Gin 上的 WebSocket 连接管理。
//
// 设计约定（业务必须遵守，框架不做背压丢弃）：
//
//	所有连接共用 send_channel / recv_channel（各缓冲 10000）。通道满时发送方会阻塞。
//	读循环把消息打进 recv_channel；OnConnect / OnMessage / OnClose 在同一条 recv 协程里同步执行。
//	回调里如果阻塞（慢逻辑、再调 Send 且 send 通道已满），recv 协程卡住 → 通道堆满 →
//	ReadMessage 循环阻塞，连接像假死。
//
//	因此：回调必须尽快返回，重活丢到业务自己的 goroutine；不要在回调里做会堵很久的 Send/Broadcast。
//
//	Close() 会直接 Conn.Close()，与 send 协程的 WriteMessage 可能并发。
//	gorilla 不允许对同一连接并发写/关。业务应避免对同一 conn 边 Close 边 Send；
//	主动 Close 后不要再对该 id 发送。
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

type Connection struct {
	Conn   *websocket.Conn
	Params map[string]string
	Query  url.Values
	Ip     string
}

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

func (s *SharkWS) GetConnects() []int {
	conns := []int{}
	s.connects.Range(func(key, value interface{}) bool {
		conns = append(conns, key.(int))
		return true
	})
	return conns
}

func (s *SharkWS) OnConnect(path string, callback func(conn int)) {
	s.connect_callback.Store(path, callback) // 在 recv 协程同步执行，必须尽快返回
}

func (s *SharkWS) RemoteIp(conn int) string {
	c, ok := s.connects.Load(conn)
	if !ok {
		return ""
	}
	connection := c.(*Connection)
	return connection.Ip
}

func (s *SharkWS) Param(conn int, key string) string {
	c, ok := s.connects.Load(conn)
	if !ok {
		return ""
	}
	connection := c.(*Connection)
	return connection.Params[key]
}

func (s *SharkWS) Query(conn int, key string) string {
	c, ok := s.connects.Load(conn)
	if !ok {
		return ""
	}
	connection := c.(*Connection)

	return connection.Query.Get(key)
}

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

func (s *SharkWS) OnClose(path string, callback func(conn int)) {
	s.close_callback.Store(path, callback) // 被动断开时在 recv 协程执行，必须尽快返回
}

func (s *SharkWS) OnMessage(path string, callback func(conn int, message []byte)) {
	s.message_callback.Store(path, callback) // 在 recv 协程同步执行，必须尽快返回
}

func (s *SharkWS) DefaultMessage(callback func(conn int, path string, message []byte)) {
	if callback == nil {
		return
	}
	s.default_message_callback.Store(callback) // 在 recv 协程同步执行，必须尽快返回
}

func (s *SharkWS) SendText(conn int, text string) {
	_, ok := s.connects.Load(conn)
	if !ok {
		return
	}
	s.send_channel <- []any{conn, websocket.TextMessage, []byte(text)} // 通道满会阻塞调用方
}

func (s *SharkWS) SendBytes(conn int, data []byte) {
	_, ok := s.connects.Load(conn)
	if !ok {
		return
	}
	s.send_channel <- []any{conn, websocket.BinaryMessage, data} // 通道满会阻塞调用方
}

func (s *SharkWS) BroadcastText(text string) {
	conns := s.GetConnects()
	for _, conn := range conns {
		s.SendText(conn, text)
	}
}

func (s *SharkWS) BroadcastBytes(data []byte) {
	conns := s.GetConnects()
	for _, conn := range conns {
		s.SendBytes(conn, data)
	}
}
