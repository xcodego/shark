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
	connect_callback         sync.Map                                    // 连接建立回调
	close_callback           sync.Map                                    // 连接关闭回调,主动关闭,不会触发回调
	message_callback         sync.Map                                    // 消息回调
	default_message_callback func(conn int, path string, message []byte) // 默认消息回调
	send_channel             chan []any
	recv_channel             chan []any
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
				} else if s.default_message_callback != nil {
					s.default_message_callback(idx, path, message)
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
		s.recv_channel <- []any{1, path, idx}
		for {
			_, message, err := wsconn.ReadMessage()
			if err != nil {
				break
			}
			s.recv_channel <- []any{3, path, idx, message}
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
	s.connect_callback.Store(path, callback)
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
	connection.Conn.Close()
}

func (s *SharkWS) OnClose(path string, callback func(conn int)) {
	s.close_callback.Store(path, callback)
}

func (s *SharkWS) OnMessage(path string, callback func(conn int, message []byte)) {
	s.message_callback.Store(path, callback)
}

func (s *SharkWS) DefaultMessage(callback func(conn int, path string, message []byte)) {
	s.default_message_callback = callback
}

func (s *SharkWS) SendText(conn int, text string) {
	_, ok := s.connects.Load(conn)
	if !ok {
		return
	}
	s.send_channel <- []any{conn, websocket.TextMessage, []byte(text)}
}

func (s *SharkWS) SendBytes(conn int, data []byte) {
	_, ok := s.connects.Load(conn)
	if !ok {
		return
	}
	s.send_channel <- []any{conn, websocket.BinaryMessage, data}
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
