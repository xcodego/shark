package sharksql

import (
	"fmt"
	"strings"
)

// ========== 聚合函数构建器（SELECT 子句）==========

// Count 构建 COUNT 聚合表达式。
// 用于 SELECT 子句中的行计数。
//
// 示例：
//
//	// SELECT count(id) FROM users
//	db.Select(sharksql.Count("id")).Find(&result)
//
//	// SELECT count(*) FROM users
//	db.Select(sharksql.Count("*")).Find(&result)
//
//	// SELECT count(DISTINCT user_id) FROM orders
//	db.Select(sharksql.Count("DISTINCT user_id")).Find(&result)
func Count(column string) string {
	return fmt.Sprintf("count(%v)", column)
}

// Sum 构建 SUM 聚合表达式。
// 多个字段以逗号分隔，不追加别名。
//
// 示例：
//
//	// SELECT sum(bet_amount), sum(win_amount) FROM orders
//	db.Select(sharksql.Sum("bet_amount", "win_amount")).Find(&result)
func Sum(column ...string) string {
	sql := ""
	for i := 0; i < len(column); i++ {
		sql += fmt.Sprintf("sum(%v), ", column[i])
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// SumAs 构建 SUM 聚合表达式，支持自定义别名。
// 参数需成对出现：字段名, 别名, 字段名, 别名 ...
//
// 示例：
//
//	// SELECT sum(bet_amount) AS total_bet, sum(win_amount) AS total_win FROM orders
//	db.Select(sharksql.SumAs("bet_amount", "total_bet", "win_amount", "total_win")).Find(&result)
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

// CountAs 构建 COUNT 聚合表达式，支持自定义别名。
// 参数需成对出现：字段名, 别名, 字段名, 别名 ...
//
// 示例：
//
//	// SELECT count(id) AS total_count, count(DISTINCT user_id) AS unique_users FROM orders
//	db.Select(sharksql.CountAs("id", "total_count", "user_id", "unique_users")).Find(&result)
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

// Avg 构建 AVG 聚合表达式。
// 多个字段以逗号分隔，不追加别名。
//
// 示例：
//
//	// SELECT avg(score) FROM exams
//	db.Select(sharksql.Avg("score")).Find(&result)
//
//	// 多字段平均
//	db.Select(sharksql.Avg("math_score", "english_score")).Find(&result)
func Avg(columns ...string) string {
	sql := ""
	for i := 0; i < len(columns); i++ {
		sql += fmt.Sprintf("avg(%v), ", columns[i])
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// AvgAs 构建 AVG 聚合表达式，支持自定义别名。
// 参数需成对出现：字段名, 别名, 字段名, 别名 ...
//
// 示例：
//
//	// SELECT avg(math_score) AS avg_math, avg(english_score) AS avg_english FROM exams
//	db.Select(sharksql.AvgAs("math_score", "avg_math", "english_score", "avg_english")).Find(&result)
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

// Max 构建 MAX 聚合表达式。
// 多个字段以逗号分隔，不追加别名。
//
// 示例：
//
//	// SELECT max(score) FROM exams
//	db.Select(sharksql.Max("score")).Find(&result)
//
//	// 多字段最大值
//	db.Select(sharksql.Max("high_temp", "low_temp")).Find(&result)
func Max(columns ...string) string {
	sql := ""
	for i := 0; i < len(columns); i++ {
		sql += fmt.Sprintf("max(%v), ", columns[i])
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// MaxAs 构建 MAX 聚合表达式，支持自定义别名。
// 参数需成对出现：字段名, 别名, 字段名, 别名 ...
//
// 示例：
//
//	// SELECT max(high_temp) AS max_high, max(low_temp) AS max_low FROM weather
//	db.Select(sharksql.MaxAs("high_temp", "max_high", "low_temp", "max_low")).Find(&result)
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

// Min 构建 MIN 聚合表达式。
// 多个字段以逗号分隔，不追加别名。
//
// 示例：
//
//	// SELECT min(price) FROM products
//	db.Select(sharksql.Min("price")).Find(&result)
func Min(columns ...string) string {
	sql := ""
	for i := 0; i < len(columns); i++ {
		sql += fmt.Sprintf("min(%v), ", columns[i])
	}
	sql = strings.TrimSuffix(sql, ", ")
	return sql
}

// MinAs 构建 MIN 聚合表达式，支持自定义别名。
// 参数需成对出现：字段名, 别名, 字段名, 别名 ...
//
// 示例：
//
//	// SELECT min(price) AS min_price, min(discount) AS min_discount FROM products
//	db.Select(sharksql.MinAs("price", "min_price", "discount", "min_discount")).Find(&result)
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
