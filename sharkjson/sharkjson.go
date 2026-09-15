// Package sharkjson 提供了高性能的 JSON 序列化与反序列化工具函数。
//
// 本包基于字节跳动的 sonic 库（github.com/bytedance/sonic），
// sonic 通过 JIT（即时编译）和 SIMD 指令集加速，在性能上远超标准库 encoding/json。
// 所有函数均采用泛型设计，返回指针便于区分零值和缺失值。
//
// 核心功能:
//   - ParseJsonBytes / ParseJsonString: 将 JSON 反序列化为指定类型
//   - ToJsonBytes / ToJsonString: 将对象序列化为 JSON
//
// 错误处理策略:
//
//	解析/序列化失败时不返回 error，而是返回 nil（指针）或 nil（切片），
//	这是一种"静默失败"的设计，适合对 JSON 健壮性要求较高的场景。
//	如果调用方需要详细的错误信息，建议直接使用 sonic 原始 API。
package sharkjson

import "github.com/bytedance/sonic"

// ParseJsonBytes 将 JSON 字节切片反序列化为指定类型的指针。
//
// 类型参数:
//   - T: 反序列化的目标类型，支持任意结构体、map、slice 等。
//
// 参数:
//   - value: JSON 格式的字节切片。如果为空（len == 0），返回 nil。
//
// 返回值:
//   - 反序列化成功时返回 *T 指针。
//   - 反序列化失败时返回 nil（例如 JSON 格式错误、类型不匹配等）。
//
// 使用示例:
//
//	type User struct {
//	    Name string `json:"name"`
//	    Age  int    `json:"age"`
//	}
//	user := sharkjson.ParseJsonBytes[User]([]byte(`{"name":"Alice","age":30}`))
//	if user != nil {
//	    fmt.Println(user.Name) // "Alice"
//	}
func ParseJsonBytes[T any](value []byte) *T {
	// 空字节切片直接返回 nil，避免不必要的反序列化
	if len(value) == 0 {
		return nil
	}
	var result T
	// 使用 sonic.Unmarshal 进行高性能反序列化
	if err := sonic.Unmarshal(value, &result); err != nil {
		// 反序列化失败时返回 nil，不抛出错误
		// 调用方通过检查返回值是否为 nil 来判断是否成功
		return nil
	}
	return &result
}

// ParseJsonString 将 JSON 字符串反序列化为指定类型的指针。
//
// 这是 ParseJsonBytes 的字符串版本，内部将字符串转为 []byte 后调用 ParseJsonBytes。
// 注意：此转换会产生一次内存分配（string -> []byte），如果性能敏感且已有 []byte，
// 请直接使用 ParseJsonBytes。
//
// 类型参数:
//   - T: 反序列化的目标类型。
//
// 参数:
//   - value: JSON 格式的字符串。如果为空字符串，返回 nil。
//
// 返回值:
//   - 反序列化成功时返回 *T 指针。
//   - 反序列化失败时返回 nil。
//
// 使用示例:
//
//	user := sharkjson.ParseJsonString[User](`{"name":"Bob","age":25}`)
func ParseJsonString[T any](value string) *T {
	if value == "" {
		return nil
	}
	// 将字符串转为字节切片后调用 ParseJsonBytes
	return ParseJsonBytes[T]([]byte(value))
}

// ToJsonBytes 将任意值序列化为 JSON 字节切片。
//
// 参数:
//   - v: 需要序列化的值，支持任意类型。如果为 nil，返回 nil。
//
// 返回值:
//   - 序列化成功时返回 JSON 字节切片。
//   - 序列化失败时返回 nil（例如包含无法序列化的类型如 channel、func 等）。
//
// 使用示例:
//
//	data := sharkjson.ToJsonBytes(map[string]int{"a": 1, "b": 2})
//	// data = []byte(`{"a":1,"b":2}`)
func ToJsonBytes(v any) []byte {
	if v == nil {
		return nil
	}
	// 使用 sonic.Marshal 进行高性能序列化
	data, err := sonic.Marshal(v)
	if err != nil {
		// 序列化失败时返回 nil
		// 常见失败原因：包含无法 JSON 化的类型（如 channel、func、complex 等）
		return nil
	}
	return data
}

// ToJsonString 将任意值序列化为 JSON 字符串。
//
// 这是 ToJsonBytes 的字符串版本，内部调用 ToJsonBytes 后转换为 string。
// 注意：[]byte -> string 转换是零拷贝的（Go 编译器优化），不会产生额外的内存分配。
//
// 参数:
//   - v: 需要序列化的值。如果为 nil，返回空字符串。
//
// 返回值:
//   - 序列化成功时返回 JSON 字符串。
//   - 序列化失败时返回空字符串。
//
// 使用示例:
//
//	jsonStr := sharkjson.ToJsonString([]string{"apple", "banana"})
//	// jsonStr = `["apple","banana"]`
func ToJsonString(v any) string {
	// 直接调用 ToJsonBytes 并转换为字符串
	// nil 的 []byte 转为 string 结果是 ""，与预期一致
	return string(ToJsonBytes(v))
}
