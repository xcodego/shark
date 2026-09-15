package sharksql

import (
	"fmt"
	"strings"

	"github.com/lornshark/shark/sharkjson"
	"github.com/spf13/cast"
)

// ========== MySQL JSON 函数封装 ==========

// JsonSearchOne 构建 JSON_SEARCH 条件（搜索 JSON 数组/对象中是否包含某值）。
// 使用 'one' 模式，找到第一个匹配即返回。
//
// 注意：value 会被自动包裹 % 通配符。
//
// 示例：
//
//	// SELECT * FROM users WHERE JSON_SEARCH(tags, 'one', '%vip%') IS NOT NULL
//	db.Where(sharksql.JsonSearchOne("tags", "vip")).Find(&users)
//
//	// 搜索 JSON 对象中嵌套的值
//	db.Where(sharksql.JsonSearchOne("metadata", "active")).Find(&users)
func JsonSearchOne(column string, value any) (string, string) {
	sql := fmt.Sprintf("JSON_SEARCH(%v, 'one', ?) IS NOT NULL", column)
	data := fmt.Sprintf("%%%v%%", cast.ToString(value))
	return sql, data
}

// JsonContains 构建 JSON_CONTAINS 条件（检查 JSON 文档是否包含指定值）。
//
// 示例：
//
//	// SELECT * FROM users WHERE JSON_CONTAINS(roles, '"admin"')
//	db.Where(sharksql.JsonContains("roles", `"admin"`)).Find(&users)
//
//	// 检查是否包含数组元素
//	db.Where(sharksql.JsonContains("tags", `["vip","premium"]`)).Find(&users)
func JsonContains(column string, value any) (string, string) {
	sql := fmt.Sprintf("JSON_CONTAINS(%v, ?)", column)
	data := fmt.Sprintf("%v", value)
	return sql, data
}

// JsonArrayAppendObject 构建 JSON_ARRAY_APPEND 表达式，将对象转 JSON 后追加到数组末尾。
// 如果列值为 NULL，自动初始化为空数组 JSON_ARRAY()。
//
// 参数：
//   - column: JSON 列名
//   - value:  要追加的值（可变参数，支持多个）。字符串直接追加，其他类型通过 sharkjson.ToJsonString 序列化
//
// 示例：
//
//	// UPDATE users SET tags = JSON_ARRAY_APPEND(COALESCE(tags, JSON_ARRAY()), '$', CAST('{"name":"vip"}' AS JSON))
//	db.Model(&User{}).Where("id = ?", 1).
//	    Update("tags", gorm.Expr(sharksql.JsonArrayAppendObject("tags", map[string]any{"name": "vip"})))
//
//	// 追加多个对象
//	db.Model(&User{}).Where("id = ?", 1).
//	    Update("tags", gorm.Expr(sharksql.JsonArrayAppendObject("tags", obj1, obj2)))
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

// JsonArrayAppend 构建 JSON_ARRAY_APPEND 表达式，将原始值追加到数组末尾。
// 与 JsonArrayAppendObject 的区别：不进行 JSON 序列化，直接使用原始值。
//
// 示例：
//
//	// UPDATE logs SET event_ids = JSON_ARRAY_APPEND(COALESCE(event_ids, JSON_ARRAY()), '$', 12345)
//	db.Model(&Log{}).Where("id = ?", 1).
//	    Update("event_ids", gorm.Expr(sharksql.JsonArrayAppend("event_ids", 12345)))
//
//	// 追加多个值
//	db.Model(&Log{}).Update("event_ids", gorm.Expr(
//	    sharksql.JsonArrayAppend("event_ids", 100, 200, 300),
//	))
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

// JsonSetObject 构建 JSON_SET 表达式，将对象转 JSON 后设置到指定路径。
// 如果列值为 NULL，自动初始化为空对象 JSON_OBJECT()。
//
// 参数：
//   - column: JSON 列名
//   - path:   JSON 路径（如 "$.name" 或使用 JsonPath() 构建）
//   - value:  要设置的值（非字符串类型会通过 sharkjson.ToJsonString 序列化）
//
// 示例：
//
//	// UPDATE users SET metadata = JSON_SET(COALESCE(metadata, JSON_OBJECT()), '$.vip_level', CONVERT('{"level":3}',JSON))
//	db.Model(&User{}).Where("id = ?", 1).
//	    Update("metadata", gorm.Expr(sharksql.JsonSetObject("metadata", "$.vip_level",
//	        map[string]any{"level": 3})))
func JsonSetObject(column string, path string, value any) (string, []any) {
	sql := fmt.Sprintf("JSON_SET(COALESCE(%v, JSON_OBJECT()), '%v', CONVERT(?,JSON))", column, path)
	return sql, []any{sharkjson.ToJsonString(value)}
}

// JsonSet 构建 JSON_SET 表达式，将原始值设置到指定路径。
// 与 JsonSetObject 的区别：不进行 JSON 序列化，直接使用原始值。
//
// 示例：
//
//	// UPDATE users SET metadata = JSON_SET(COALESCE(metadata, JSON_OBJECT()), '$.age', 25)
//	db.Model(&User{}).Where("id = ?", 1).
//	    Update("metadata", gorm.Expr(sharksql.JsonSet("metadata", "$.age", 25)))
//
//	// 设置字符串值
//	db.Model(&User{}).Where("id = ?", 1).
//	    Update("metadata", gorm.Expr(sharksql.JsonSet("metadata", "$.city", "北京")))
func JsonSet(column string, path string, value any) (string, []any) {
	sql := fmt.Sprintf("JSON_SET(COALESCE(%v, JSON_OBJECT()), '%v', ?)", column, path)
	return sql, []any{value}
}

// JsonPath 构建 MySQL JSON 路径表达式。
//
// 示例：
//
//	// 构建 $.user.address.city
//	path := sharksql.JsonPath("user", "address", "city")
//	// 结果: "$.user.address.city"
//
//	// 用于 JSON_EXTRACT
//	db.Select(fmt.Sprintf("JSON_EXTRACT(metadata, '%s')", sharksql.JsonPath("name"))).Find(&result)
func JsonPath(path ...string) string {
	jsonPath := "$"
	for _, p := range path {
		jsonPath += fmt.Sprintf(".%v", p)
	}
	return jsonPath
}

// JsonExtract 构建 JSON_EXTRACT 表达式，从 JSON 文档中提取路径对应的值。
// path 参数为 JSON 路径，可使用 JsonPath() 构建或直接传入 "$.xxx" 字符串。
//
// 示例：
//
//	// SELECT JSON_EXTRACT(metadata, '$.name') FROM users
//	db.Select(sharksql.JsonExtract("metadata", "$.name")).Find(&results)
//
//	// 配合 JsonPath 使用
//	db.Select(sharksql.JsonExtract("metadata", sharksql.JsonPath("user", "name"))).Find(&results)
//
//	// 提取多个路径
//	db.Select(sharksql.JsonExtract("metadata", "$.name", "$.age")).Find(&results)
func JsonExtract(column string, path ...string) string {
	if len(path) == 0 {
		return fmt.Sprintf("JSON_EXTRACT(%v, '$')", column)
	}
	if len(path) == 1 {
		return fmt.Sprintf("JSON_EXTRACT(%v, '%v')", column, path[0])
	}
	return fmt.Sprintf("JSON_EXTRACT(%v, '%v')", column, strings.Join(path, "', '"))
}

// JsonUnquote 构建 JSON_UNQUOTE(JSON_EXTRACT(...)) 表达式。
// 提取 JSON 值并去除引号，常用于 WHERE/Having 条件中与字符串比较。
//
// 示例：
//
//	// SELECT * FROM users WHERE JSON_UNQUOTE(JSON_EXTRACT(metadata, '$.city')) = 'NYC'
//	db.Where(fmt.Sprintf("%s = ?", sharksql.JsonUnquote("metadata", "$.city")), "NYC").Find(&users)
func JsonUnquote(column string, path string) string {
	return fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%v, '%v'))", column, path)
}

// JsonRemove 构建 JSON_REMOVE 表达式，从 JSON 文档中删除指定路径。
// 通常配合 Update 使用。
//
// 示例：
//
//	// UPDATE users SET tags = JSON_REMOVE(tags, '$[0]') WHERE id = 1
//	db.Model(&User{}).Where("id = ?", 1).
//	    Update("tags", gorm.Expr(sharksql.JsonRemove("tags", "$[0]")))
//
//	// 删除多个路径
//	db.Model(&User{}).Where("id = ?", 1).
//	    Update("metadata", gorm.Expr(sharksql.JsonRemove("metadata", "$.temp", "$.cache")))
func JsonRemove(column string, path ...string) string {
	if len(path) == 1 {
		return fmt.Sprintf("JSON_REMOVE(%v, '%v')", column, path[0])
	}
	return fmt.Sprintf("JSON_REMOVE(%v, '%v')", column, strings.Join(path, "', '"))
}

// JsonArrayInsert 构建 JSON_ARRAY_INSERT 表达式，在数组指定位置插入值。
// path 为插入位置（如 "$[0]"），value 为要插入的值。
// 如果列值为 NULL，自动初始化为空数组 JSON_ARRAY()。
//
// 示例：
//
//	// UPDATE users SET tags = JSON_ARRAY_INSERT(COALESCE(tags, JSON_ARRAY()), '$[0]', 'vip')
//	db.Model(&User{}).Where("id = ?", 1).
//	    Update("tags", gorm.Expr(sharksql.JsonArrayInsert("tags", "$[0]", "vip")))
//
//	// 支持非字符串类型（自动转 JSON）
//	db.Model(&User{}).Where("id = ?", 1).
//	    Update("tags", gorm.Expr(sharksql.JsonArrayInsert("tags", "$[1]", map[string]any{"name": "vip"})))
func JsonArrayInsert(column string, path string, value any) (string, []any) {
	sql := fmt.Sprintf("JSON_ARRAY_INSERT(COALESCE(%v, JSON_ARRAY()), '%v', CAST(? AS JSON))", column, path)
	v := value
	if _, ok := value.(string); !ok {
		v = sharkjson.ToJsonString(value)
	}
	return sql, []any{v}
}

// JsonLength 构建 JSON_LENGTH 表达式，返回 JSON 文档的长度。
// 数组返回元素个数，对象返回键的数量。
//
// 示例：
//
//	// SELECT JSON_LENGTH(tags) AS tag_count FROM users
//	db.Select(sharksql.JsonLength("tags")).Find(&results)
func JsonLength(column string) string {
	return fmt.Sprintf("JSON_LENGTH(%v)", column)
}

// JsonKeys 构建 JSON_KEYS 表达式，返回 JSON 对象的键名数组。
//
// 示例：
//
//	// SELECT JSON_KEYS(metadata) AS meta_keys FROM users
//	db.Select(sharksql.JsonKeys("metadata")).Find(&results)
func JsonKeys(column string) string {
	return fmt.Sprintf("JSON_KEYS(%v)", column)
}

// JsonType 构建 JSON_TYPE 表达式，返回 JSON 值的类型字符串。
// 返回值如 "OBJECT"、"ARRAY"、"STRING"、"INTEGER" 等。
//
// 示例：
//
//	// SELECT * FROM users WHERE JSON_TYPE(metadata) = 'OBJECT'
//	db.Where(fmt.Sprintf("%s = ?", sharksql.JsonType("metadata")), "OBJECT").Find(&users)
func JsonType(column string) string {
	return fmt.Sprintf("JSON_TYPE(%v)", column)
}
