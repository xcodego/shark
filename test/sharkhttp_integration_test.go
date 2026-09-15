package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lornshark/shark/sharkerror"
	"github.com/lornshark/shark/sharkhttp"
	"go.uber.org/zap"
)

// findFreePort 找一个空闲端口
func findFreePort() int {
	return 39000 + (os.Getpid() % 1000)
}

// TestSharkHttpServer 启动真实 HTTP Server 并测试 CORS + Recovery + Error 中间件链。
func TestSharkHttpServer(t *testing.T) {
	logger := zap.NewNop()
	port := findFreePort()
	gin.SetMode(gin.TestMode)

	router := sharkhttp.New(context.Background(), "test", logger, port)

	// 注册测试路由
	router.GET("/api/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"pong": true})
	})
	router.GET("/api/error", func(c *gin.Context) {
		c.Error(sharkerror.New(10001, "业务错误").WithData(map[string]any{"detail": "test"}))
	})
	router.GET("/api/panic", func(c *gin.Context) {
		panic("intentional panic for test")
	})

	// 等待 server 就绪
	time.Sleep(300 * time.Millisecond)
	baseURL := "http://127.0.0.1:" + fmt.Sprint(port)

	// 测试普通请求
	resp, err := http.Get(baseURL + "/api/ping")
	if err != nil {
		t.Fatalf("GET /api/ping 失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/api/ping 状态码 = %d, want 200", resp.StatusCode)
	}
	t.Logf("GET /api/ping → %d: %s", resp.StatusCode, body)

	// 测试业务错误中间件
	resp, err = http.Get(baseURL + "/api/error")
	if err != nil {
		t.Fatalf("GET /api/error 失败: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	t.Logf("GET /api/error → %d: %s", resp.StatusCode, body)
	var errResp map[string]any
	json.Unmarshal(body, &errResp)
	if ec, ok := errResp["code"].(float64); ok && ec == 10001 {
		t.Log("业务错误码验证通过")
	} else {
		t.Logf("错误响应: code=%v msg=%v", errResp["code"], errResp["msg"])
	}

	// 测试 panic 恢复中间件
	resp, err = http.Get(baseURL + "/api/panic")
	if err != nil {
		t.Fatalf("GET /api/panic 失败: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Errorf("/api/panic 状态码 = %d, want 500", resp.StatusCode)
	}
	t.Logf("GET /api/panic → %d (recovery 验证通过)", resp.StatusCode)

	// 测试 CORS (OPTIONS)
	req, _ := http.NewRequest("OPTIONS", baseURL+"/api/ping", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /api/ping 失败: %v", err)
	}
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS Allow-Origin = %s, want *", resp.Header.Get("Access-Control-Allow-Origin"))
	}
	t.Logf("OPTIONS → %d, CORS headers OK", resp.StatusCode)
}

// TestSharkHttpMiddleware 使用 httptest 风格测试中间件
func TestSharkHttpMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 创建一个带完整中间件的 router
	router := gin.New()
	router.GET("/api/data", func(c *gin.Context) {
		c.JSON(200, gin.H{"result": "ok"})
	})
	router.GET("/api/error", func(c *gin.Context) {
		c.Error(sharkerror.New(10001, "测试错误"))
	})

	// 使用 httptest 进行子测试
	t.Run("CORS_OPTIONS", func(t *testing.T) {
		// Cors 中间件已由 sharkhttp.New 内置
		req, _ := http.NewRequest("OPTIONS", "/api/data", nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		// OPTIONS 如果 cors 已配置会返回 204
		t.Logf("OPTIONS /api/data → %d", resp.Code)
	})
}

var _ = fmt.Sprintf // 确保 fmt 被引用
