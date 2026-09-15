package sharksql

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/bytedance/sonic"
)

// Where 根据结构体的 sql tag 生成参数化 SQL WHERE 条件。
//
// 设计理念：通过反射读取 struct 的 sql tag，自动将非 nil 字段转为参数化条件。
// nil 指针字段自动跳过，无需写一堆 if xx != nil 判空，极大减少样板代码。
//
// 字段处理规则：
//   - 只处理"基础类型的指针"（*int / *string / *float64 等）和"基础类型的切片"（[]int / []string 等）
//   - 非基础类型的指针（*struct / *map 等，*decimal.Decimal 除外）→ 忽略，不生成条件
//   - 非基础类型的切片（[]struct 等，[]decimal.Decimal 除外）→ 忽略，不生成条件
//   - *decimal.Decimal 和 []decimal.Decimal 均视为基础类型，正常参与条件构建
//   - 非指针、非切片的值类型字段（int / string / struct 等）→ 忽略
//   - 指针为 nil 时忽略该字段（等价于"不筛选此条件"）
//   - 切片 len=0 时忽略该字段（等价于"不筛选此条件"）
//   - 没有 sql tag 的字段忽略
//   - IN / NOT IN 条件必须是基础类型切片，否则忽略
//   - 多个条件自动以 AND 连接
//
// 基础类型定义：bool, int/int8/int16/int32/int64, uint/uint8/uint16/uint32/uint64,
// float32/float64, string, 以及 decimal.Decimal（shopspring/decimal）。
// 其他所有类型（struct / map / 自定义类型等，除 decimal.Decimal 外）均视为非基础类型，不会参与条件构建。
//
// sql tag 模板对照表（tag 格式 → 结构体字段类型 → 生成的 SQL → 参数处理）：
//
//	┌─────────────────────────────────────┬──────────────────────────┬──────────────────┐
//	│ sql tag 模板                        │ 结构体字段类型           │ 生成的 SQL/参数   │
//	├─────────────────────────────────────┼──────────────────────────┼──────────────────┤
//	│ `sql:"status = ?"`                  │ *int                     │ status = ?       │
//	│ `sql:"status <> ?"`                 │ *int                     │ status <> ?      │
//	│ `sql:"age > ?"`                     │ *int                     │ age > ?          │
//	│ `sql:"amount >= ?"`                 │ *float64                 │ amount >= ?      │
//	│ `sql:"price < ?"`                   │ *int                     │ price < ?        │
//	│ `sql:"stock <= ?"`                  │ *int                     │ stock <= ?       │
//	│ `sql:"name LIKE ?"`                 │ *string                  │ 参数自动 %value% │
//	│ `sql:"name NOT LIKE ?"`             │ *string                  │ 参数自动 %value% │
//	│ `sql:"phone LIKEL ?"`               │ *string                  │ 参数自动 value%  │
//	│ `sql:"email LIKER ?"`               │ *string                  │ 参数自动 %value  │
//	│ `sql:"status IN (?)"`               │ []int                    │ 原切片传递       │
//	│ `sql:"id NOT IN (?)"`               │ []int64                  │ 原切片传递       │
//	│ `sql:"amount = ?"`                  │ *decimal.Decimal         │ amount = ?       │
//	│ `sql:"status IN (?)"`               │ []decimal.Decimal        │ 原切片传递       │
//	└─────────────────────────────────────┴──────────────────────────┴──────────────────┘
//
// 注意：*decimal.Decimal 和 []decimal.Decimal 均为基础类型，会正常生成条件。
// *YourStruct、*map[string]any 等非基础类型不会生成条件，
// 即使 sql tag 写的是 "column = ?" 或 "column IN (?)"，也只会被忽略。
//
// 使用示例：
//
//	// 1. 定义请求结构体（sql tag + json tag 共存）
//	type ListUsersReq struct {
//	    sharksql.Pagination          // 嵌入分页参数（Page/PageSize）
//	    Status *int    `json:"status" sql:"status = ?"`
//	    Name   *string `json:"name"   sql:"name LIKE ?"`
//	    City   *string `json:"city"   sql:"city = ?"`
//	    MinAge *int    `json:"-"      sql:"age >= ?"` // json:"-" 不暴露给前端
//	}
//
//	// 2. 前端传入 JSON → 反序列化 → Where 生成条件
//	// 前端传：{"status":1, "name":"张三"}
//	req := ListUsersReq{
//	    Pagination: sharksql.Pagination{Page: 1, PageSize: 20},
//	    Status:     intPtr(1),
//	    Name:       strPtr("张三"),
//	    City:       nil, // 不筛选城市 → 自动跳过
//	}
//	sql, args := sharksql.Where(req)
//	// sql:  "status = ? AND name LIKE ?"
//	// args: [1, %张三%]
//
//	// 3. 配合 GORM 查询
//	db.Where(sql, args...).
//	    Limit(req.PageSize).Offset((req.Page-1)*req.PageSize).
//	    Order("created_at DESC").
//	    Find(&users)
//
//	// 4. 配合 SharkTable 更简洁（一步到位）
//	table := sharkdb.NewTableWithReq(db.Table("users"), req)
//	table.Gorm().Find(&users)
func Where(req any) (string, []any) {
	v := reflect.ValueOf(req)
	// 如果是指针，解引用到实际值
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "", nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return "", nil
	}

	t := v.Type()
	var conditions []string
	var args []any

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		sqlTag := field.Tag.Get("sql")
		if sqlTag == "" {
			continue
		}

		fieldVal := v.Field(i)

		// 判断是否为指针类型
		if fieldVal.Kind() == reflect.Ptr {
			if fieldVal.IsNil() {
				continue // 指针为 nil，忽略
			}
			// 解引用指针，检查是否为基本类型（decimal.Decimal 属于基础类型）
			elem := fieldVal.Elem()
			if !isBasicKind(elem.Kind()) && !isDecimal(elem) {
				continue // 非基础类型（*struct 等），忽略
			}
			actualVal := elem.Interface()

			cond, arg := buildCondition(sqlTag, actualVal)
			if cond != "" {
				conditions = append(conditions, cond)
				args = append(args, arg)
			}
		} else if fieldVal.Kind() == reflect.Slice {
			if fieldVal.Len() == 0 {
				continue // 空切片，忽略
			}
			// 检查切片元素是否为基本类型（decimal.Decimal 属于基础类型）
			elemType := fieldVal.Type().Elem()
			if !isBasicKind(elemType.Kind()) && !isDecimalType(elemType) {
				continue // 非基础类型切片（[]struct 等），忽略
			}
			// IN 条件才处理切片
			cond, arg := buildCondition(sqlTag, fieldVal.Interface())
			if cond != "" {
				conditions = append(conditions, cond)
				args = append(args, arg)
			}
		}
		// 非指针、非切片字段：忽略
	}

	if len(conditions) == 0 {
		return "", nil
	}

	return strings.Join(conditions, " AND "), args
}

// isBasicKind 判断 reflect.Kind 是否为基本类型。
// 基本类型包括：bool, int 系列, uint 系列, float 系列, string。
// 其他所有类型（struct / map / slice / array / interface / ptr 等）均返回 false。
func isBasicKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return true
	default:
		return false
	}
}

// isDecimal 判断 reflect.Value 是否为 decimal.Decimal 类型。
// decimal.Decimal（shopspring/decimal）被视为基础类型，参与 Where 条件构建。
func isDecimal(rv reflect.Value) bool {
	if !rv.IsValid() {
		return false
	}
	iface := rv.Interface()
	_, ok := iface.(jsonDecimal)
	return ok
}

// isDecimalType 判断 reflect.Type 是否为 decimal.Decimal 类型。
// 用于切片元素类型检查。
func isDecimalType(t reflect.Type) bool {
	if t == nil {
		return false
	}
	// decimal.Decimal 作为 reflect.Struct，无法简单通过 Kind 区分。
	// 通过尝试创建一个零值实例并类型断言。
	if t.Kind() != reflect.Struct {
		return false
	}
	v := reflect.New(t).Elem()
	if !v.IsValid() {
		return false
	}
	return isDecimal(v)
}

// buildCondition 根据 sql tag 模板和值构造 SQL 条件片段。
// 支持的模板：
//   - "column = ?"        → column = ?
//   - "column <> ?"       → column <> ?
//   - "column > ?"        → column > ?
//   - "column >= ?"       → column >= ?
//   - "column < ?"        → column < ?
//   - "column <= ?"       → column <= ?
//   - "column IN (?)"     → column IN (?) (值必须是切片)
//   - "column NOT IN (?)" → column NOT IN (?) (值必须是切片)
//   - "column LIKE ?"     → column LIKE ? (参数带 %value%)
//   - "column NOT LIKE ?" → column NOT LIKE ? (参数带 %value%)
//   - "column LIKEL ?"    → column LIKE ? (参数带 value%)
//   - "column NOT LIKEL ?"→ column NOT LIKE ? (参数带 value%)
//   - "column LIKER ?"    → column LIKE ? (参数带 %value)
//   - "column NOT LIKER ?"→ column NOT LIKE ? (参数带 %value)
func buildCondition(sqlTag string, value any) (string, any) {
	// 转为大写做大小写不敏感匹配
	upper := strings.ToUpper(sqlTag)

	// 处理 IN / NOT IN（必须在 LIKE 之前，因为 "IN" 可能出现在 LIKE 中）
	if strings.Contains(upper, "IN (?)") {
		rv := reflect.ValueOf(value)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return "", nil // 不是切片，忽略
		}
		return sqlTag, value
	}

	// NOT LIKEL → NOT LIKE (大小写不敏感) 参数: value%
	if idx := indexIgnoreCase(upper, " NOT LIKEL ?"); idx != -1 {
		cond := sqlTag[:idx] + " NOT LIKE ?"
		return cond, fmt.Sprint(value) + "%"
	}

	// NOT LIKER → NOT LIKE (大小写不敏感) 参数: %value
	if idx := indexIgnoreCase(upper, " NOT LIKER ?"); idx != -1 {
		cond := sqlTag[:idx] + " NOT LIKE ?"
		return cond, "%" + fmt.Sprint(value)
	}

	// NOT LIKE (大小写不敏感) 参数: %value%
	if strings.Contains(upper, " NOT LIKE ?") {
		return sqlTag, "%" + fmt.Sprint(value) + "%"
	}

	// LIKEL → LIKE (大小写不敏感) 参数: value%
	if idx := indexIgnoreCase(upper, " LIKEL ?"); idx != -1 {
		cond := sqlTag[:idx] + " LIKE ?"
		return cond, fmt.Sprint(value) + "%"
	}

	// LIKER → LIKE (大小写不敏感) 参数: %value
	if idx := indexIgnoreCase(upper, " LIKER ?"); idx != -1 {
		cond := sqlTag[:idx] + " LIKE ?"
		return cond, "%" + fmt.Sprint(value)
	}

	// LIKE (大小写不敏感) 参数: %value%
	if strings.Contains(upper, " LIKE ?") {
		return sqlTag, "%" + fmt.Sprint(value) + "%"
	}

	// 默认：直接使用 sql tag 作为条件，值原样传递
	return sqlTag, value
}

// indexIgnoreCase 在 s 中大小写不敏感搜索 substr，返回首次出现的索引，未找到返回 -1。
// 参数 s 必须已经是大写形式。
func indexIgnoreCase(s string, substr string) int {
	return strings.Index(s, strings.ToUpper(substr))
}

// ToUpdate 根据结构体字段生成 UPDATE SET 列的 map[string]any。
//
// 规则：
//   - 只处理指针字段（*T, *[]T, *map[K]V），其他类型忽略
//   - 列名取自 json tag（json:"-" 的字段会被忽略）
//   - 指针 nil 忽略
//   - 值如果是复合类型，用 sonic 序列化为 JSON 字符串
//   - 基本类型直接传值，不序列化
//
// 基本类型（直接传值，不序列化为 JSON）：
//
//	bool, int/int8/int16/int32/int64,
//	uint/uint8/uint16/uint32/uint64,
//	float32/float64, string,
//	decimal.Decimal（shopspring/decimal）
//
// 复合类型（序列化为 JSON 字符串）：
//
//	除上述基本类型之外的所有类型，包括但不限于：
//	struct、map、slice、array、指针、interface、自定义类型等。
//	例如 *[]UserInfo → `[{"name":"Alice"}]`
//	     *map[string]any → `{"key":"value"}`
//	     *YourStruct     → `{"field":"value"}`
//
// 使用示例：
//
//	type UpdateReq struct {
//	    Name   *string         `json:"name"`   // 基本类型 → "张三"
//	    Age    *int            `json:"age"`    // 基本类型 → 25
//	    Price  *decimal.Decimal `json:"price"` // 基本类型 → 19.99 (原值)
//	    Tags   *[]string       `json:"tags"`   // 复合类型 → `["a","b"]`
//	    Meta   *map[string]any  `json:"meta"`  // 复合类型 → `{"k":"v"}`
//	    Ignore int             `json:"ignore"` // 非指针，忽略
//	}
func ToUpdateData(req any) map[string]any {
	v := reflect.ValueOf(req)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}

	t := v.Type()
	result := make(map[string]any)

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		column := field.Tag.Get("json")
		if column == "" {
			continue
		}
		sp := strings.Index(column, ",")
		if sp != -1 {
			column = column[:sp]
		}
		fieldVal := v.Field(i)
		if fieldVal.Kind() != reflect.Ptr {
			continue
		}
		if fieldVal.IsNil() {
			continue
		}

		elem := fieldVal.Elem()
		val := resolveValue(elem)
		if val != nil {
			result[column] = val
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// resolveValue 将 reflect.Value 转为实际可用值。
// 如果是复合类型（struct/slice/map/array），序列化为 JSON 字符串。
// decimal.Decimal 不算复合类型。
// 注意：调用前已经解完 *T 指针，此处 elem.Kind() 不应是 Ptr。
func resolveValue(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	iface := rv.Interface()
	// 先检查是否是 decimal.Decimal
	if _, ok := iface.(jsonDecimal); ok {
		return iface
	}
	switch rv.Kind() {
	case reflect.Struct:
		return toJSON(iface)
	case reflect.Slice, reflect.Array, reflect.Map:
		return toJSON(iface)
	default:
		return iface
	}
}

// jsonDecimal 是 decimal.Decimal 的一个简写别名，用于类型检测（无需直接 import decimal 包）。
type jsonDecimal interface {
	String() string
}

// toJSON 用 sonic 将值序列化为 JSON 字符串。失败返回 nil。
func toJSON(v any) any {
	b, err := sonic.Marshal(v)
	if err != nil {
		return nil
	}
	// 去掉可能的换行，sonic 默认不换行但保险
	return string(b)
}
