// Package sharkutils 提供了一些通用的工具函数。
//
// 包括：
//   - 随机数/随机字符串生成
//   - MD5 哈希计算
//   - 客户端 IP 获取（支持代理穿透）
//   - bcrypt 密码加密与验证
package sharkutils

import (
	"crypto/md5"
	"encoding/hex"
	crand "math/rand/v2"
	"net"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// RandNum 生成 [min, max) 区间内的随机整数。
//
// 使用 Go 1.22+ 的 math/rand/v2 包，无需手动设置种子。
// 当 min >= max 时直接返回 min。
//
// 使用示例:
//
//	n := sharkutils.RandNum(1, 100)   // 返回 1~99 之间的随机数
//	n := sharkutils.RandNum(0, 10)    // 返回 0~9 之间的随机数
//	n := sharkutils.RandNum(5, 5)     // min>=max，直接返回 5
//
// 参数:
//   - min: 最小值（包含）
//   - max: 最大值（不包含）
//
// 返回:
//   - [min, max) 之间的随机整数
func RandNum(min int, max int) int {
	if min >= max {
		return min
	}
	return crand.IntN(max-min) + min
}

// Md5 计算字节数据的 MD5 哈希值并返回十六进制字符串。
//
// 使用示例:
//
//	hash := sharkutils.Md5([]byte("hello"))
//	// hash = "5d41402abc4b2a76b9719d911017c592"
//	hash := sharkutils.Md5([]byte(""))
//	// hash = "d41d8cd98f00b204e9800998ecf8427e"
//
// 参数:
//   - data: 待哈希的字节数据
//
// 返回:
//   - 32 位小写十六进制 MD5 哈希字符串
func Md5(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// GetClientIp 从 HTTP 请求中提取客户端真实 IP 地址。
//
// 检查顺序（优先级从高到低）:
//  1. X-Forwarded-For 头（取第一个 IP，即最原始客户端）
//  2. X-Real-Ip 头
//  3. RemoteAddr（直连 IP）
//
// 适用于部署在 Nginx/负载均衡器后方的服务。
//
// 使用示例:
//
//	// Gin Handler 中
//	func Handler(c *gin.Context) {
//	    ip := sharkutils.GetClientIp(c.Request)
//	    log.Printf("客户端IP: %s", ip)
//	}
//
//	// 标准库 HTTP Handler 中
//	func Handler(w http.ResponseWriter, r *http.Request) {
//	    ip := sharkutils.GetClientIp(r)
//	}
//
// 参数:
//   - request: HTTP 请求对象
//
// 返回:
//   - 客户端真实 IP 地址字符串
func GetClientIp(request *http.Request) string {
	ip := request.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = request.Header.Get("X-Real-Ip")
	}
	if ip == "" {
		// 直接从 RemoteAddr 解析
		ip, _, _ = net.SplitHostPort(request.RemoteAddr)
	} else {
		// X-Forwarded-For 可能包含多个 IP（经过多层代理），取第一个
		ip = strings.Split(ip, ",")[0]
	}
	return ip
}

// BcryptHash 使用 bcrypt 算法对密码进行哈希加密。
//
// 使用 bcrypt.DefaultCost（10）作为加密轮数，平衡安全性和性能。
// 相同密码每次加密结果不同（随机盐值）。
//
// 使用示例:
//
//	// 用户注册时加密密码
//	hashed := sharkutils.BcryptHash("myPassword123")
//	// hashed = "$2a$10$..." 存入数据库
//
//	// 每次加密结果不同（随机盐值）
//	h1 := sharkutils.BcryptHash("samePassword")
//	h2 := sharkutils.BcryptHash("samePassword")
//	// h1 != h2，但都能通过 BcryptCheck 验证
//
// 参数:
//   - password: 明文密码
//
// 返回:
//   - bcrypt 哈希字符串（可直接存入数据库）
func BcryptHash(password string) string {
	bytes, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes)
}

// BcryptCheck 验证明文密码是否与 bcrypt 哈希匹配。
//
// 使用示例:
//
//	// 用户登录时验证密码
//	hashed := sharkutils.BcryptHash("myPassword123")        // 先存入数据库
//	matched := sharkutils.BcryptCheck(hashed, "myPassword123") // true
//	matched := sharkutils.BcryptCheck(hashed, "wrongPassword") // false
//
//	// 典型登录流程
//	func Login(inputPassword string, dbHash string) bool {
//	    return sharkutils.BcryptCheck(dbHash, inputPassword)
//	}
//
// 参数:
//   - hashedPassword: bcrypt 加密后的哈希字符串
//   - password: 待验证的明文密码
//
// 返回:
//   - true: 密码匹配
//   - false: 密码不匹配
func BcryptCheck(hashedPassword string, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}
