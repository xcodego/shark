package sharksql

import (
	"fmt"
)

// Pagination 是分页请求参数，取值约束由 PageQuery 负责修正。
type Pagination struct {
	Page     int `json:"page"`      // 页码，从 1 开始
	PageSize int `json:"page_size"` // 每页条数
}

// Eq 构建等于条件（=）。
func Eq(column string, value any) (string, any) {
	return column + " = ?", value
}

// Neq 构建不等于条件（<>）。
func Neq(column string, value any) (string, any) {
	return column + " <> ?", value
}

// Gt 构建大于条件（>）。
func Gt(column string, value any) (string, any) {
	return column + " > ?", value
}

// Gte 构建大于等于条件（>=）。
func Gte(column string, value any) (string, any) {
	return column + " >= ?", value
}

// Lt 构建小于条件（<）。
func Lt(column string, value any) (string, any) {
	return column + " < ?", value
}

// Lte 构建小于等于条件（<=）。
func Lte(column string, value any) (string, any) {
	return column + " <= ?", value
}

// Like 构建模糊匹配条件（LIKE %value%），返回的参数已被 % 包裹。
func Like(column string, value any) (string, any) {
	return column + " LIKE ?", "%" + fmt.Sprint(value) + "%"
}

// NotLike 构建反向模糊匹配条件（NOT LIKE %value%）。
func NotLike(column string, value any) (string, any) {
	return column + " NOT LIKE ?", "%" + fmt.Sprint(value) + "%"
}

// In 构建 IN 条件，value 应为切片（GORM 自动展开）。
func In(column string, value any) (string, any) {
	return column + " IN (?)", value
}

// NotIn 构建 NOT IN 条件。
func NotIn(column string, value any) (string, any) {
	return column + " NOT IN (?)", value
}

// IsNull 构建 IS NULL 条件。
func IsNull(column string) string {
	return column + " IS NULL"
}

// IsNotNull 构建 IS NOT NULL 条件。
func IsNotNull(column string) string {
	return column + " IS NOT NULL"
}

// Add 构建字段加法表达式（column + ?），用于 UPDATE 的 SET 子句。Sub/Mul/Div 同理。
func Add(column string, value any) (string, any) {
	return column + " + ?", value
}

// Sub 构建字段减法表达式（column - value）。
func Sub(column string, value any) (string, any) {
	return column + " - ?", value
}

// Mul 构建字段乘法表达式（column * value）。
func Mul(column string, value any) (string, any) {
	return column + " * ?", value
}

// Div 构建字段除法表达式（column / value）。
func Div(column string, value any) (string, any) {
	return column + " / ?", value
}

// Asc 构建升序排序表达式。
func Asc(column string) string {
	return column + " ASC"
}

// Desc 构建降序排序表达式。
func Desc(column string) string {
	return column + " DESC"
}

// Between 构建左闭右开区间条件 [lo, hi)：column >= ? AND column < ?。注意不是 SQL 的闭区间 BETWEEN。
// 与 FromTo 等价。
func Between(column string, lo any, hi any) (string, any, any) {
	return column + " >= ? AND " + column + " < ?", lo, hi
}

// FromTo 构建左闭右开区间条件 [from, to)，与 Between 等价。
func FromTo(column string, from any, to any) (string, any, any) {
	return column + " >= ? AND " + column + " < ?", from, to
}
