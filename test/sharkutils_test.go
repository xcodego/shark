package test

import (
	"net/http"
	"testing"

	"github.com/lornshark/shark/sharkutils"
)

func TestRandNum(t *testing.T) {
	for i := 0; i < 100; i++ {
		n := sharkutils.RandNum(1, 100)
		if n < 1 || n >= 100 {
			t.Errorf("RandNum(1,100) = %d, 应在 [1,100) 范围内", n)
		}
	}
}

func TestRandNumMinEqMax(t *testing.T) {
	n := sharkutils.RandNum(5, 5)
	if n != 5 {
		t.Errorf("RandNum(5,5) = %d, want 5", n)
	}
}

func TestMd5(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "5d41402abc4b2a76b9719d911017c592"},
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
		{"shark", "da6776e7ec7eaa7a6f3df5c6b149127e"},
	}
	for _, tt := range tests {
		hash := sharkutils.Md5([]byte(tt.input))
		if hash != tt.expected {
			t.Errorf("Md5(%q) = %s, want %s", tt.input, hash, tt.expected)
		}
	}
}

func TestGetClientIp(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xri        string
		expected   string
	}{
		{"直接连接", "192.168.1.1:12345", "", "", "192.168.1.1"},
		{"Nginx 代理 X-Forwarded-For", "10.0.0.1:80", "1.2.3.4", "", "1.2.3.4"},
		{"多层代理", "10.0.0.1:80", "1.2.3.4, 5.6.7.8", "", "1.2.3.4"},
		{"X-Real-Ip", "10.0.0.1:80", "", "9.8.7.6", "9.8.7.6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-Ip", tt.xri)
			}
			ip := sharkutils.GetClientIp(req)
			if ip != tt.expected {
				t.Errorf("GetClientIp = %s, want %s", ip, tt.expected)
			}
		})
	}
}

func TestBcryptHashAndCheck(t *testing.T) {
	password := "mySecurePassword123"
	hashed := sharkutils.BcryptHash(password)
	if hashed == "" {
		t.Fatal("加密后不应为空")
	}
	if len(hashed) < 20 {
		t.Errorf("哈希长度过短: %d", len(hashed))
	}

	// 验证匹配
	if !sharkutils.BcryptCheck(hashed, password) {
		t.Error("正确密码应该验证通过")
	}

	// 验证错误密码
	if sharkutils.BcryptCheck(hashed, "wrongPassword") {
		t.Error("错误密码不应该验证通过")
	}
}

func TestBcryptHashRandomSalt(t *testing.T) {
	pwd := "samePassword"
	h1 := sharkutils.BcryptHash(pwd)
	h2 := sharkutils.BcryptHash(pwd)
	if h1 == h2 {
		t.Error("相同密码的两次加密结果应该不同（随机盐值）")
	}
	// 但都能验证通过
	if !sharkutils.BcryptCheck(h1, pwd) || !sharkutils.BcryptCheck(h2, pwd) {
		t.Error("随机盐值的哈希都应能验证通过")
	}
}
