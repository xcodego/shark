package sharksql

import (
	"fmt"
)

// Pagination 定义分页请求参数。
//
// 字段说明：
//   - Page:     页码，从 1 开始（Page < 1 时自动修正为 1）
//   - PageSize: 每页条数（PageSize < 10 时自动修正为 10）
//
// 使用示例：
//
//	type ListUsersReq struct {
//	    sharksql.Pagination          // 嵌入分页参数
//	    Name     string `json:"name"`  // 业务筛选字段
//	    Status   int    `json:"status"`
//	}
//
//	req := ListUsersReq{
//	    Pagination: sharksql.Pagination{Page: 1, PageSize: 20},
//	    Status:     1,
//	}
type Pagination struct {
	Page     int `json:"page"`      // 页码，从 1 开始
	PageSize int `json:"page_size"` // 每页条数
}

// ========== 比较运算符（WHERE 条件构建）==========

// Eq 构建等于条件（=）。
//
// 示例：
//
//	// SELECT * FROM users WHERE status = 1
//	db.Where(sharksql.Eq("status", 1)).Find(&users)
//
//	// 多条件配合
//	db.Where(
//	    sharksql.Eq("status", 1),
//	    sharksql.Eq("deleted", 0),
//	).Find(&users)
func Eq(column string, value any) (string, any) {
	return column + " = ?", value
}

// Neq 构建不等于条件（<>）。
//
// 示例：
//
//	// SELECT * FROM users WHERE status <> 0
//	db.Where(sharksql.Neq("status", 0)).Find(&users)
func Neq(column string, value any) (string, any) {
	return column + " <> ?", value
}

// Gt 构建大于条件（>）。
//
// 示例：
//
//	// SELECT * FROM users WHERE age > 18
//	db.Where(sharksql.Gt("age", 18)).Find(&users)
//
//	// 大于指定时间
//	db.Where(sharksql.Gt("created_at", time.Now().Add(-24*time.Hour))).Find(&users)
func Gt(column string, value any) (string, any) {
	return column + " > ?", value
}

// Gte 构建大于等于条件（>=）。
//
// 示例：
//
//	// SELECT * FROM orders WHERE amount >= 100
//	db.Where(sharksql.Gte("amount", 100)).Find(&orders)
func Gte(column string, value any) (string, any) {
	return column + " >= ?", value
}

// Lt 构建小于条件（<）。
//
// 示例：
//
//	// SELECT * FROM users WHERE age < 60
//	db.Where(sharksql.Lt("age", 60)).Find(&users)
func Lt(column string, value any) (string, any) {
	return column + " < ?", value
}

// Lte 构建小于等于条件（<=）。
//
// 示例：
//
//	// SELECT * FROM products WHERE price <= 5000
//	db.Where(sharksql.Lte("price", 5000)).Find(&products)
func Lte(column string, value any) (string, any) {
	return column + " <= ?", value
}

// ========== 模糊匹配运算符 ==========

// Like 构建模糊匹配条件（LIKE %value%）。
//
// 注意：value 会自动被 % 包裹，无需手动添加通配符。
//
// 示例：
//
//	// SELECT * FROM users WHERE name LIKE '%张三%'
//	db.Where(sharksql.Like("name", "张三")).Find(&users)
//
//	// 多字段模糊搜索
//	db.Where(
//	    sharksql.Or(
//	        sharksql.Like("name", keyword),
//	        sharksql.Like("email", keyword),
//	    ),
//	).Find(&users)
func Like(column string, value any) (string, any) {
	return column + " LIKE ?", "%" + fmt.Sprint(value) + "%"
}

// NotLike 构建反向模糊匹配条件（NOT LIKE %value%）。
//
// 示例：
//
//	// SELECT * FROM users WHERE name NOT LIKE '%test%'
//	db.Where(sharksql.NotLike("name", "test")).Find(&users)
func NotLike(column string, value any) (string, any) {
	return column + " NOT LIKE ?", "%" + fmt.Sprint(value) + "%"
}

// ========== 集合运算符 ==========

// In 构建 IN 条件。
//
// 注意：GORM 会自动展开切片参数，value 应传入切片类型。
//
// 示例：
//
//	// SELECT * FROM users WHERE status IN (1, 2, 3)
//	db.Where(sharksql.In("status", []int{1, 2, 3})).Find(&users)
//
//	// 结合字符串切片
//	db.Where(sharksql.In("city", []string{"北京", "上海", "深圳"})).Find(&users)
func In(column string, value any) (string, any) {
	return column + " IN (?)", value
}

// NotIn 构建 NOT IN 条件。
//
// 示例：
//
//	// SELECT * FROM users WHERE status NOT IN (4, 5)
//	db.Where(sharksql.NotIn("status", []int{4, 5})).Find(&users)
func NotIn(column string, value any) (string, any) {
	return column + " NOT IN (?)", value
}

// ========== NULL 运算符 ==========

// IsNull 构建 IS NULL 条件。
//
// 示例：
//
//	// SELECT * FROM users WHERE deleted_at IS NULL
//	db.Where(sharksql.IsNull("deleted_at")).Find(&users)
func IsNull(column string) string {
	return column + " IS NULL"
}

// IsNotNull 构建 IS NOT NULL 条件。
//
// 示例：
//
//	// SELECT * FROM users WHERE email IS NOT NULL
//	db.Where(sharksql.IsNotNull("email")).Find(&users)
func IsNotNull(column string) string {
	return column + " IS NOT NULL"
}

// ========== 算术运算符（用于 UPDATE SET 子句）==========

// Add 构建字段加法表达式（column + value）。
// 通常配合 Update 的 SET 子句使用。
//
// 示例：
//
//	// UPDATE accounts SET balance = balance + 100 WHERE id = 1
//	db.Model(&Account{}).Where("id = ?", 1).
//	    Update("balance", gorm.Expr(sharksql.Add("balance", 100)))
func Add(column string, value any) (string, any) {
	return column + " + ?", value
}

// Sub 构建字段减法表达式（column - value）。
//
// 示例：
//
//	// UPDATE accounts SET balance = balance - 50 WHERE id = 1
//	db.Model(&Account{}).Where("id = ?", 1).
//	    Update("balance", gorm.Expr(sharksql.Sub("balance", 50)))
func Sub(column string, value any) (string, any) {
	return column + " - ?", value
}

// Mul 构建字段乘法表达式（column * value）。
//
// 示例：
//
//	// UPDATE products SET price = price * 1.1 WHERE category = 'electronics'
//	db.Model(&Product{}).Where("category = ?", "electronics").
//	    Update("price", gorm.Expr(sharksql.Mul("price", 1.1)))
func Mul(column string, value any) (string, any) {
	return column + " * ?", value
}

// Div 构建字段除法表达式（column / value）。
//
// 示例：
//
//	// UPDATE scores SET avg_score = total_score / count WHERE count > 0
//	db.Model(&Score{}).Where("count > ?", 0).
//	    Update("avg_score", gorm.Expr(sharksql.Div("total_score", "count")))
func Div(column string, value any) (string, any) {
	return column + " / ?", value
}

// ========== 排序构建器（ORDER BY）==========

// Asc 构建升序排序表达式。
//
// 示例：
//
//	// SELECT * FROM users ORDER BY created_at ASC
//	db.Order(sharksql.Asc("created_at")).Find(&users)
//
//	// 多字段排序
//	db.Order(
//	    sharksql.Asc("status"),
//	    sharksql.Desc("created_at"),
//	).Find(&users)
func Asc(column string) string {
	return column + " ASC"
}

// Desc 构建降序排序表达式。
//
// 示例：
//
//	// SELECT * FROM orders ORDER BY amount DESC
//	db.Order(sharksql.Desc("amount")).Find(&orders)
func Desc(column string) string {
	return column + " DESC"
}

// ========== 范围查询 ==========

// Between 构建左闭右开区间条件 [lo, hi)：column >= ? AND column < ?。
// 与 FromTo 等价，语义更清晰。
//
// 示例：
//
//	// SELECT * FROM orders WHERE amount >= ? AND amount < ?
//	db.Where(sharksql.Between("amount", 100, 500)).Find(&orders)
func Between(column string, lo any, hi any) (string, any, any) {
	return column + " >= ? AND " + column + " < ?", lo, hi
}

// FromTo 构建左闭右开区间条件 [from, to)。
// 等价于: column >= from AND column < to
//
// 示例：
//
//	// SELECT * FROM orders WHERE created_at >= '2025-01-01' AND created_at < '2025-02-01'
//	db.Where(sharksql.FromTo("created_at", "2025-01-01", "2025-02-01")).Find(&orders)
//
//	// 数值范围查询
//	db.Where(sharksql.FromTo("age", 18, 60)).Find(&users)
func FromTo(column string, from any, to any) (string, any, any) {
	return column + " >= ? AND " + column + " < ?", from, to
}
