package sharksql

import (
	"fmt"
	"strings"

	"github.com/spf13/cast"
	"github.com/xcodego/shark/sharkjson"
)

// JsonSearchOne 构建 JSON_SEARCH(…, 'one', ?) IS NOT NULL 条件，返回 (sql, 参数)。
// 参数为 value 转字符串并包裹 % 通配符。
func JsonSearchOne(column string, value any) (string, string) {
	sql := fmt.Sprintf("JSON_SEARCH(%v, 'one', ?) IS NOT NULL", column)
	data := fmt.Sprintf("%%%v%%", cast.ToString(value))
	return sql, data
}

// JsonContains 构建 JSON_CONTAINS(column, ?) 条件，返回 (sql, 参数)；value 应为 JSON 文本（字符串需自带引号）。
func JsonContains(column string, value any) (string, string) {
	sql := fmt.Sprintf("JSON_CONTAINS(%v, ?)", column)
	data := fmt.Sprintf("%v", value)
	return sql, data
}

// JsonArrayAppendObject 构建 JSON_ARRAY_APPEND 表达式，依次将 value 追加到数组末尾，返回 (sql, 参数)。
// 列值为 NULL 时先初始化为空数组；字符串视为已序列化的 JSON 文本，其他类型用 sharkjson.ToJsonString 序列化。
func JsonArrayAppendObject(column string, value ...any) (string, []any) {
	sql := fmt.Sprintf("JSON_ARRAY_APPEND(COALESCE(%v, JSON_ARRAY())", column)
	args := []any{}
	for _, v := range value {
		switch v.(type) {
		case string:
			sql += ",'$', CAST(? AS JSON)"
			args = append(args, v)
		default:
			sql += ",'$', CAST(? AS JSON)"
			args = append(args, sharkjson.ToJsonString(v))
		}
	}
	sql += ")"
	return sql, args
}

// JsonArrayAppend 构建 JSON_ARRAY_APPEND 表达式，将原始值依次追加到数组末尾（列值为 NULL 时先初始化为空数组）。
// 与 JsonArrayAppendObject 的区别：不做 JSON 序列化。
func JsonArrayAppend(column string, value ...any) (string, []any) {
	sql := fmt.Sprintf("JSON_ARRAY_APPEND(COALESCE(%v, JSON_ARRAY())", column)
	args := []any{}
	for _, v := range value {
		sql += ",'$', ?"
		args = append(args, v)
	}
	sql += ")"
	return sql, args
}

// JsonSetObject 构建 JSON_SET 表达式，将 value 通过 sharkjson.ToJsonString 序列化后设置到 path。
// 列值为 NULL 时先初始化为空对象；path 如 "$.name"，可用 JsonPath 构建。
func JsonSetObject(column string, path string, value any) (string, []any) {
	sql := fmt.Sprintf("JSON_SET(COALESCE(%v, JSON_OBJECT()), '%v', CONVERT(?,JSON))", column, path)
	return sql, []any{sharkjson.ToJsonString(value)}
}

// JsonSet 构建 JSON_SET 表达式，将原始值设置到 path（列值为 NULL 时先初始化为空对象）。
// 与 JsonSetObject 的区别：不做 JSON 序列化。
func JsonSet(column string, path string, value any) (string, []any) {
	sql := fmt.Sprintf("JSON_SET(COALESCE(%v, JSON_OBJECT()), '%v', ?)", column, path)
	return sql, []any{value}
}

// JsonPath 构建 MySQL JSON 路径表达式。
func JsonPath(path ...string) string {
	jsonPath := "$"
	for _, p := range path {
		jsonPath += fmt.Sprintf(".%v", p)
	}
	return jsonPath
}

// JsonExtract 构建 JSON_EXTRACT 表达式；path 为 JSON 路径（如 "$.xxx"，可用 JsonPath 构建），不传则取 "$"。
func JsonExtract(column string, path ...string) string {
	if len(path) == 0 {
		return fmt.Sprintf("JSON_EXTRACT(%v, '$')", column)
	}
	if len(path) == 1 {
		return fmt.Sprintf("JSON_EXTRACT(%v, '%v')", column, path[0])
	}
	return fmt.Sprintf("JSON_EXTRACT(%v, '%v')", column, strings.Join(path, "', '"))
}

// JsonUnquote 构建 JSON_UNQUOTE(JSON_EXTRACT(...)) 表达式，提取值并去掉引号，便于与字符串比较。
func JsonUnquote(column string, path string) string {
	return fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%v, '%v'))", column, path)
}

// JsonRemove 构建 JSON_REMOVE 表达式，删除指定路径，通常配合 Update 使用。
func JsonRemove(column string, path ...string) string {
	if len(path) == 1 {
		return fmt.Sprintf("JSON_REMOVE(%v, '%v')", column, path[0])
	}
	return fmt.Sprintf("JSON_REMOVE(%v, '%v')", column, strings.Join(path, "', '"))
}

// JsonArrayInsert 构建 JSON_ARRAY_INSERT 表达式，在 path（如 "$[0]"）位置插入 value。
// 列值为 NULL 时先初始化为空数组；字符串视为 JSON 文本，其他类型用 sharkjson.ToJsonString 序列化。
func JsonArrayInsert(column string, path string, value any) (string, []any) {
	sql := fmt.Sprintf("JSON_ARRAY_INSERT(COALESCE(%v, JSON_ARRAY()), '%v', CAST(? AS JSON))", column, path)
	v := value
	if _, ok := value.(string); !ok {
		v = sharkjson.ToJsonString(value)
	}
	return sql, []any{v}
}

// JsonLength 构建 JSON_LENGTH 表达式（数组为元素个数，对象为键个数）。
func JsonLength(column string) string {
	return fmt.Sprintf("JSON_LENGTH(%v)", column)
}

// JsonKeys 构建 JSON_KEYS 表达式，返回 JSON 对象的键名数组。
func JsonKeys(column string) string {
	return fmt.Sprintf("JSON_KEYS(%v)", column)
}

// JsonType 构建 JSON_TYPE 表达式（结果如 "OBJECT"、"ARRAY"、"STRING"）。
func JsonType(column string) string {
	return fmt.Sprintf("JSON_TYPE(%v)", column)
}
