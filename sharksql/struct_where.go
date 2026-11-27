package sharksql

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/bytedance/sonic"
)

// Where 根据结构体字段的 sql tag 构建参数化条件，条件以 AND 连接。
// 仅处理基础类型（含 decimal）的指针和切片字段，nil 指针、空切片及其他类型会被跳过；
// sql tag 内容直接拼入 SQL，必须是代码中固定的模板。
//
// sql tag 模板对照表（LIKEL 为前缀匹配 value%，LIKER 为后缀匹配 %value）：
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
func Where(req any) (string, []any) {
	v := reflect.ValueOf(req)
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

		if fieldVal.Kind() == reflect.Ptr {
			if fieldVal.IsNil() {
				continue // 指针为 nil，忽略
			}
			// 只接受基础类型（decimal.Decimal 视为基础类型）
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
			// 切片元素同样只接受基础类型
			elemType := fieldVal.Type().Elem()
			if !isBasicKind(elemType.Kind()) && !isDecimalType(elemType) {
				continue // 非基础类型切片（[]struct 等），忽略
			}
			cond, arg := buildCondition(sqlTag, fieldVal.Interface())
			if cond != "" {
				conditions = append(conditions, cond)
				args = append(args, arg)
			}
		}
		// 非指针、非切片字段不参与条件构建
	}

	if len(conditions) == 0 {
		return "", nil
	}

	return strings.Join(conditions, " AND "), args
}

// isBasicKind 判断是否为 bool、int/uint/float 系列或 string。
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

// isDecimal 判断值是否为 decimal.Decimal（通过 jsonDecimal 接口识别，实现了 String() 的类型都会命中）。
func isDecimal(rv reflect.Value) bool {
	if !rv.IsValid() {
		return false
	}
	iface := rv.Interface()
	_, ok := iface.(jsonDecimal)
	return ok
}

// isDecimalType 判断类型是否为 decimal.Decimal，用于切片元素检查。
func isDecimalType(t reflect.Type) bool {
	if t == nil {
		return false
	}
	// decimal.Decimal 是 struct，无法按 Kind 区分，故构造零值后用接口断言判断
	if t.Kind() != reflect.Struct {
		return false
	}
	v := reflect.New(t).Elem()
	if !v.IsValid() {
		return false
	}
	return isDecimal(v)
}

// buildCondition 根据 sql tag 模板和值构造条件片段与参数，关键字大小写不敏感。
// IN (?) / NOT IN (?) 要求值为切片/数组，否则返回空条件；
// LIKE / NOT LIKE 参数加 %value%，LIKEL 改写为 LIKE 并加 value%，LIKER 改写为 LIKE 并加 %value；
// 其余模板（=、<>、>、>=、<、<= 等）原样返回，参数不变。
func buildCondition(sqlTag string, value any) (string, any) {
	upper := strings.ToUpper(sqlTag)

	// IN / NOT IN
	if strings.Contains(upper, "IN (?)") {
		rv := reflect.ValueOf(value)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return "", nil // 不是切片，忽略
		}
		return sqlTag, value
	}

	// NOT LIKEL：改写为 NOT LIKE，参数 value%
	if idx := indexIgnoreCase(upper, " NOT LIKEL ?"); idx != -1 {
		cond := sqlTag[:idx] + " NOT LIKE ?"
		return cond, fmt.Sprint(value) + "%"
	}

	// NOT LIKER：改写为 NOT LIKE，参数 %value
	if idx := indexIgnoreCase(upper, " NOT LIKER ?"); idx != -1 {
		cond := sqlTag[:idx] + " NOT LIKE ?"
		return cond, "%" + fmt.Sprint(value)
	}

	// NOT LIKE：参数 %value%
	if strings.Contains(upper, " NOT LIKE ?") {
		return sqlTag, "%" + fmt.Sprint(value) + "%"
	}

	// LIKEL：改写为 LIKE，参数 value%
	if idx := indexIgnoreCase(upper, " LIKEL ?"); idx != -1 {
		cond := sqlTag[:idx] + " LIKE ?"
		return cond, fmt.Sprint(value) + "%"
	}

	// LIKER：改写为 LIKE，参数 %value
	if idx := indexIgnoreCase(upper, " LIKER ?"); idx != -1 {
		cond := sqlTag[:idx] + " LIKE ?"
		return cond, "%" + fmt.Sprint(value)
	}

	// LIKE：参数 %value%
	if strings.Contains(upper, " LIKE ?") {
		return sqlTag, "%" + fmt.Sprint(value) + "%"
	}

	return sqlTag, value
}

// indexIgnoreCase 在已转大写的 s 中查找 substr（会先转大写），返回首次出现的索引，未找到返回 -1。
func indexIgnoreCase(s string, substr string) int {
	return strings.Index(s, strings.ToUpper(substr))
}

// ToUpdateData 根据 json tag 将 req 中非 nil 的指针字段转换为 UPDATE 数据（列名为 json tag 名）；
// struct、切片、数组、map 会序列化为 JSON 字符串，无可更新字段时返回 nil。
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

// resolveValue 将复合类型序列化为 JSON 字符串，decimal.Decimal 和基础类型保留原值。
func resolveValue(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	iface := rv.Interface()
	// decimal.Decimal 保留原值
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

// jsonDecimal 用于识别 decimal.Decimal（避免直接依赖 decimal 包）。
type jsonDecimal interface {
	String() string
}

// toJSON 使用 sonic 将值序列化为 JSON 字符串，失败返回 nil。
func toJSON(v any) any {
	b, err := sonic.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}
