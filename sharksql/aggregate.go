package sharksql

import (
	"fmt"
	"strings"
)

// Count 构建 count(column) 聚合表达式。
func Count(column string) string {
	return fmt.Sprintf("count(%v)", column)
}

// Sum 构建 SUM 聚合表达式，多个字段以逗号分隔，不加别名。Avg/Max/Min 同理。
func Sum(column ...string) string {
	sql := ""
	for i := 0; i < len(column); i++ {
		sql += fmt.Sprintf("sum(%v), ", column[i])
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// SumAs 构建带别名的 SUM 表达式，参数为 字段名, 别名 成对出现；参数个数为奇数时返回空串。CountAs/AvgAs/MaxAs/MinAs 同理。
func SumAs(column ...string) string {
	if len(column)%2 != 0 {
		return ""
	}
	sql := ""
	for i := 0; i < len(column); i += 2 {
		if i+1 < len(column) {
			sql += fmt.Sprintf("sum(%v) as %v, ", column[i], column[i+1])
		}
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// CountAs 构建带别名的 COUNT 表达式，规则同 SumAs。
func CountAs(columns ...string) string {
	if len(columns)%2 != 0 {
		return ""
	}
	sql := ""
	for i := 0; i < len(columns); i += 2 {
		if i+1 < len(columns) {
			sql += fmt.Sprintf("count(%v) as %v, ", columns[i], columns[i+1])
		}
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// Avg 构建 AVG 聚合表达式，规则同 Sum。
func Avg(columns ...string) string {
	sql := ""
	for i := 0; i < len(columns); i++ {
		sql += fmt.Sprintf("avg(%v), ", columns[i])
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// AvgAs 构建带别名的 AVG 表达式，规则同 SumAs。
func AvgAs(columns ...string) string {
	if len(columns)%2 != 0 {
		return ""
	}
	sql := ""
	for i := 0; i < len(columns); i += 2 {
		if i+1 < len(columns) {
			sql += fmt.Sprintf("avg(%v) as %v, ", columns[i], columns[i+1])
		}
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// Max 构建 MAX 聚合表达式，规则同 Sum。
func Max(columns ...string) string {
	sql := ""
	for i := 0; i < len(columns); i++ {
		sql += fmt.Sprintf("max(%v), ", columns[i])
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// MaxAs 构建带别名的 MAX 表达式，规则同 SumAs。
func MaxAs(columns ...string) string {
	if len(columns)%2 != 0 {
		return ""
	}
	sql := ""
	for i := 0; i < len(columns); i += 2 {
		if i+1 < len(columns) {
			sql += fmt.Sprintf("max(%v) as %v, ", columns[i], columns[i+1])
		}
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// Min 构建 MIN 聚合表达式，规则同 Sum。
func Min(columns ...string) string {
	sql := ""
	for i := 0; i < len(columns); i++ {
		sql += fmt.Sprintf("min(%v), ", columns[i])
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// MinAs 构建带别名的 MIN 表达式，规则同 SumAs。
func MinAs(columns ...string) string {
	if len(columns)%2 != 0 {
		return ""
	}
	sql := ""
	for i := 0; i < len(columns); i += 2 {
		if i+1 < len(columns) {
			sql += fmt.Sprintf("min(%v) as %v, ", columns[i], columns[i+1])
		}
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}
