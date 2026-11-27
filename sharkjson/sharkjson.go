// Package sharkjson 提供 JSON 序列化与反序列化工具函数。
// 解析或序列化失败时返回 nil 或空字符串。
package sharkjson

import "github.com/bytedance/sonic"

// ParseJsonBytes 将 JSON 字节切片反序列化为指定类型的指针。
// 输入为空或解析失败时返回 nil。
func ParseJsonBytes[T any](value []byte) *T {
	if len(value) == 0 {
		return nil
	}
	var result T
	if err := sonic.Unmarshal(value, &result); err != nil {
		return nil
	}
	return &result
}

// ParseJsonString 将 JSON 字符串反序列化为指定类型的指针。
// 输入为空或解析失败时返回 nil。
func ParseJsonString[T any](value string) *T {
	if value == "" {
		return nil
	}
	return ParseJsonBytes[T]([]byte(value))
}

// ToJsonBytes 将任意值序列化为 JSON 字节切片。
// 输入为 nil 或序列化失败时返回 nil。
func ToJsonBytes(v any) []byte {
	if v == nil {
		return nil
	}
	data, err := sonic.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// ToJsonString 将任意值序列化为 JSON 字符串。
// 输入为 nil 或序列化失败时返回空字符串。
func ToJsonString(v any) string {
	return string(ToJsonBytes(v))
}
