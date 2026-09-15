package sharkdb

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/lornshark/shark/sharksql"
	"gorm.io/gorm"
)

// SharkTable 是 gorm.DB 的便捷包装器。
//
// 它在 gorm 基础上提供了链式调用的条件构建方法，
// 自动跳过空值（避免写一堆 if xx != "" 判空），
// 并支持通过 sharksql.SqlBuilder 构建复杂 OR 查询。
//
// 与 sharksql.SqlBuilder 的区别：
//   - SqlBuilder 是纯 SQL 条件构建器，返回 SQL 字符串和参数，需要自行拼接到 gorm 中。
//   - SharkTable 直接封装 gorm.DB，调用方法后立即生效，适合简单的单表 CRUD。
//
// 使用示例：
//
//	table := NewTable(db.Table("users"))
//	var users []User
//	err := table.
//	    Eq("status", 1).
//	    Like("name", "张").
//	    FromTo("created_at", start, end).
//	    Asc("id").
//	    Gorm().Find(&users).Error
type SharkTable struct {
	db *gorm.DB
}

// NewTable 创建一个 SharkTable 实例，
// db 通常由 db.Table("table_name") 获得。
//
// 示例：
//
//	table := NewTable(app.Db.Table("users"))
func NewTable(db *gorm.DB) *SharkTable {
	return &SharkTable{db: db}
}

func NewTableWithReq(db *gorm.DB, req any) *SharkTable {
	sql, args := sharksql.Where(req)
	db = db.Where(sql, args...)
	return &SharkTable{db: db}
}

// isEmpty 判断值是否为空（nil / 空指针 / 空切片 / 空 map）。
func (t *SharkTable) isEmpty(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	// 限制最大解引用深度，防止多级指针无限递归
	const maxDepth = 4
	for depth := 0; depth < maxDepth; depth++ {
		switch rv.Kind() {
		case reflect.Ptr, reflect.Interface:
			if rv.IsNil() {
				return true
			}
			rv = rv.Elem()
		case reflect.Slice, reflect.Map:
			return rv.Len() == 0
		default:
			return false
		}
	}
	return false
}

// Gorm 返回底层的 gorm.DB，用于执行最终的查询操作（Find、Count 等）。
//
// 示例：
//
//	var users []User
//	err := table.Eq("status", 1).Gorm().Find(&users).Error
//
//	var count int64
//	err := table.Eq("deleted", 0).Gorm().Count(&count).Error
func (t *SharkTable) Gorm() *gorm.DB {
	return t.db
}

// SelectWithTiflash 设置查询使用 TiFlash 引擎。
//
// 第一个参数为 SELECT 的列名，后续参数为对应值。
// 底层等价于 SELECT /*+ read_from_storage(tiflash[table_name]) */ col1, col2 ...
//
// 注意：使用此方法只能查询单表，不能查询关联表。
//
// 示例：
//
//	table.SelectWithTiflash("id", "name", "created_at")
//	// → SELECT /*+ read_from_storage(tiflash[users]) */ id, name, created_at
func (t *SharkTable) SelectWithTiflash(columns ...any) *SharkTable {
	if len(columns) == 0 {
		return t
	}
	query := fmt.Sprintf("/*+ read_from_storage(tiflash[%v]) */ %v", t.db.Statement.Table, columns[0])
	t.db = t.db.Select(query, columns[1:]...)
	return t
}

// Select 设置查询字段，直接透传给 gorm 的 Select。
//
// 示例：
//
//	table.Select("id, name").Select("email")
//	// → SELECT id, name, email
//
//	table.Select("SUM(amount) as total")
func (t *SharkTable) Select(query any, args ...any) *SharkTable {
	t.db = t.db.Select(query, args...)
	return t
}

// Distinct 添加 DISTINCT 去重。
// columns 为空时自动跳过。
//
// 示例：
//
//	table.Distinct("status")              // SELECT DISTINCT status
//	table.Distinct("user_id", "org_id")   // SELECT DISTINCT(user_id, org_id)
func (t *SharkTable) Distinct(columns ...string) *SharkTable {
	if len(columns) == 0 {
		return t
	}
	if len(columns) == 1 {
		t.db = t.db.Distinct(columns[0])
	} else {
		t.db = t.db.Distinct("(" + strings.Join(columns, ", ") + ")")
	}
	return t
}

// Eq 添加等值条件：column = ?。
// value 为空（nil/空串/空切片）时自动跳过。
//
// 示例：
//
//	table.Eq("status", 1)           // status = 1
//	table.Eq("name", "")            // 跳过，不添加条件
//	table.Eq("status", status)      // status 变量为 nil 时自动跳过
func (t *SharkTable) Eq(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		t.db = t.db.Where(column+" = ?", value)
	}
	return t
}

// Ne 添加不等于条件：column <> ?。
// value 为空时自动跳过。
//
// 示例：
//
//	table.Ne("status", 0)  // status <> 0
func (t *SharkTable) Ne(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		t.db = t.db.Where(column+" <> ?", value)
	}
	return t
}

// Gt 添加大于条件：column > ?。
// value 为空时自动跳过。
//
// 示例：
//
//	table.Gt("age", 18)  // age > 18
func (t *SharkTable) Gt(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		t.db = t.db.Where(column+" > ?", value)
	}
	return t
}

// Gte 添加大于等于条件：column >= ?。
// value 为空时自动跳过。
//
// 示例：
//
//	table.Gte("score", 60)  // score >= 60
func (t *SharkTable) Gte(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		t.db = t.db.Where(column+" >= ?", value)
	}
	return t
}

// Lt 添加小于条件：column < ?。
// value 为空时自动跳过。
//
// 示例：
//
//	table.Lt("price", 100)  // price < 100
func (t *SharkTable) Lt(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		t.db = t.db.Where(column+" < ?", value)
	}
	return t
}

// Le 添加小于等于条件：column <= ?。
// value 为空时自动跳过。
//
// 示例：
//
//	table.Le("stock", 50)  // stock <= 50
func (t *SharkTable) Le(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		t.db = t.db.Where(column+" <= ?", value)
	}
	return t
}

// FromTo 添加左闭右开区间条件：[from, to)，即 column >= ? AND column < ?。
// from 或 to 为空时自动跳过。
//
// 示例：
//
//	table.FromTo("created_at", startTime, endTime)
//	// → created_at >= ? AND created_at < ?
func (t *SharkTable) FromTo(column string, from any, to any) *SharkTable {
	if !t.isEmpty(from) && !t.isEmpty(to) {
		t.db = t.db.Where(column+" >= ? AND "+column+" < ?", from, to)
	}
	return t
}

// Between 添加左闭右开区间条件：[from, to)，即 column >= ? AND column < ?。
// from 或 to 为空时自动跳过。
//
// 示例：
//
//	table.Between("age", 18, 60)
//	// → age >= ? AND age < ?
func (t *SharkTable) Between(column string, from any, to any) *SharkTable {
	if !t.isEmpty(from) && !t.isEmpty(to) {
		t.db = t.db.Where(column+" >= ? AND "+column+" < ?", from, to)
	}
	return t
}

// Like 添加模糊匹配条件：column LIKE '%value%'（前后通配）。
// value 为空时自动跳过。支持指针类型自动解引用。
//
// 示例：
//
//	table.Like("name", "张")       // name LIKE '%张%'
//	table.Like("title", &keyword) // keyword 为 *string，nil 时自动跳过
func (t *SharkTable) Like(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		v := reflect.ValueOf(value)
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return t
			}
			value = v.Elem().Interface()
		}
		t.db = t.db.Where(column+" LIKE ?", fmt.Sprintf("%%%v%%", value))
	}
	return t
}

// NotLike 添加反向模糊匹配条件：column NOT LIKE '%value%'。
// value 为空时自动跳过。支持指针类型自动解引用。
//
// 示例：
//
//	table.NotLike("name", "test")  // name NOT LIKE '%test%'
func (t *SharkTable) NotLike(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		v := reflect.ValueOf(value)
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return t
			}
			value = v.Elem().Interface()
		}
		t.db = t.db.Where(column+" NOT LIKE ?", fmt.Sprintf("%%%v%%", value))
	}
	return t
}

// LikeLeft 添加后缀匹配条件：column LIKE '%value'，匹配以 value 结尾的字符串。
// value 为空时自动跳过。支持指针类型自动解引用。
//
// 示例：
//
//	table.LikeLeft("email", "@qq.com")  // email LIKE '%@qq.com'
func (t *SharkTable) LikeLeft(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		v := reflect.ValueOf(value)
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return t
			}
			value = v.Elem().Interface()
		}
		t.db = t.db.Where(column+" LIKE ?", fmt.Sprintf("%%%v", value))
	}
	return t
}

// LikeRight 添加前缀匹配条件：column LIKE 'value%'，匹配以 value 开头的字符串。
// value 为空时自动跳过。支持指针类型自动解引用。
//
// 示例：
//
//	table.LikeRight("phone", "138")  // phone LIKE '138%'
func (t *SharkTable) LikeRight(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		v := reflect.ValueOf(value)
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return t
			}
			value = v.Elem().Interface()
		}
		t.db = t.db.Where(column+" LIKE ?", fmt.Sprintf("%v%%", value))
	}
	return t
}

// In 添加 IN 条件：column IN (?, ?, ...)。
// value 应为切片或数组，为空时自动跳过。
//
// 示例：
//
//	table.In("status", []int{1, 2, 3})     // status IN (1, 2, 3)
//	table.In("id", []int64{100, 200, 300}) // id IN (100, 200, 300)
func (t *SharkTable) In(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		t.db = t.db.Where(column+" IN (?)", value)
	}
	return t
}

// NotIn 添加 NOT IN 条件：column NOT IN (?, ?, ...)。
// value 应为切片或数组，为空时自动跳过。
//
// 示例：
//
//	table.NotIn("id", []int64{1, 2})  // id NOT IN (1, 2)
func (t *SharkTable) NotIn(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		t.db = t.db.Where(column+" NOT IN (?)", value)
	}
	return t
}

// IsNull 添加 IS NULL 条件。
//
// 示例：
//
//	table.IsNull("deleted_at")  // deleted_at IS NULL
func (t *SharkTable) IsNull(column string) *SharkTable {
	t.db = t.db.Where(column + " IS NULL")
	return t
}

// IsNotNull 添加 IS NOT NULL 条件。
//
// 示例：
//
//	table.IsNotNull("email")  // email IS NOT NULL
func (t *SharkTable) IsNotNull(column string) *SharkTable {
	t.db = t.db.Where(column + " IS NOT NULL")
	return t
}

// Asc 添加升序排序。
// column 为空时自动跳过。
//
// 示例：
//
//	table.Asc("create_time").Asc("id")
//	// → ORDER BY create_time ASC, id ASC
func (t *SharkTable) Asc(column string) *SharkTable {
	if !t.isEmpty(column) {
		t.db = t.db.Order(column + " ASC")
	}
	return t
}

// Desc 添加降序排序。
// column 为空时自动跳过。
//
// 示例：
//
//	table.Desc("score").Desc("id")
//	// → ORDER BY score DESC, id DESC
func (t *SharkTable) Desc(column string) *SharkTable {
	if !t.isEmpty(column) {
		t.db = t.db.Order(column + " DESC")
	}
	return t
}

// ========== 字段对字段算术方法 ==========

// AddCol 添加字段对字段加法表达式：column + otherColumn。
// 用于 SELECT 子句。
//
// 示例：
//
//	table.Select(table.AddCol("principal", "interest"))
//	// → SELECT principal + interest
func (t *SharkTable) AddCol(column string, otherColumn string) string {
	return sharksql.AddCol(column, otherColumn)
}

// AddColAs 添加字段对字段加法表达式并指定别名：(column + otherColumn) as alias。
// 用于 SELECT 子句。
//
// 示例：
//
//	table.Select(table.AddColAs("base_salary", "bonus", "total"))
//	// → SELECT (base_salary + bonus) as total
func (t *SharkTable) AddColAs(column string, otherColumn string, as string) string {
	return sharksql.AddColAs(column, otherColumn, as)
}

// SubCol 添加字段对字段减法表达式：column - otherColumn。
// 用于 SELECT 子句。
//
// 示例：
//
//	table.Select(table.SubCol("revenue", "cost"))
//	// → SELECT revenue - cost
func (t *SharkTable) SubCol(column string, otherColumn string) string {
	return sharksql.SubCol(column, otherColumn)
}

// SubColAs 添加字段对字段减法表达式并指定别名：(column - otherColumn) as alias。
// 用于 SELECT 子句。
//
// 示例：
//
//	table.Select(table.SubColAs("revenue", "cost", "profit"))
//	// → SELECT (revenue - cost) as profit
func (t *SharkTable) SubColAs(column string, otherColumn string, as string) string {
	return sharksql.SubColAs(column, otherColumn, as)
}

// MulCol 添加字段对字段乘法表达式：column * otherColumn。
// 用于 SELECT 子句。
//
// 示例：
//
//	table.Select(table.MulCol("unit_price", "amount"))
//	// → SELECT unit_price * amount
func (t *SharkTable) MulCol(column string, otherColumn string) string {
	return sharksql.MulCol(column, otherColumn)
}

// MulColAs 添加字段对字段乘法表达式并指定别名：(column * otherColumn) as alias。
// 用于 SELECT 子句。
//
// 示例：
//
//	table.Select(table.MulColAs("price", "quantity", "total_amount"))
//	// → SELECT (price * quantity) as total_amount
func (t *SharkTable) MulColAs(column string, otherColumn string, as string) string {
	return sharksql.MulColAs(column, otherColumn, as)
}

// DivCol 添加字段对字段除法表达式：column / otherColumn。
// 用于 SELECT 子句。
//
// 示例：
//
//	table.Select(table.DivCol("total_score", "count"))
//	// → SELECT total_score / count
func (t *SharkTable) DivCol(column string, otherColumn string) string {
	return sharksql.DivCol(column, otherColumn)
}

// DivColAs 添加字段对字段除法表达式并指定别名：(column / otherColumn) as alias。
// 用于 SELECT 子句。
//
// 示例：
//
//	table.Select(table.DivColAs("total_score", "count", "avg_score"))
//	// → SELECT (total_score / count) as avg_score
func (t *SharkTable) DivColAs(column string, otherColumn string, as string) string {
	return sharksql.DivColAs(column, otherColumn, as)
}

// As 为表达式添加别名：(expression) as alias。
// 可与任意字段表达式配合使用。
//
// 示例：
//
//	table.Select(table.As(sharksql.Count("id"), "total_count"))
//	// → SELECT (count(id)) as total_count
func (t *SharkTable) As(expression string, alias string) string {
	return sharksql.As(expression, alias)
}

// Coalesce 构建 COALESCE 表达式：COALESCE(args...)。
// 参数个数可变。
//
// 示例：
//
//	table.Select(table.Coalesce("nickname", "'匿名用户'"))
//	// → SELECT COALESCE(nickname, '匿名用户')
func (t *SharkTable) Coalesce(args ...any) string {
	return sharksql.Coalesce(args...)
}

// CoalesceAs 构建 COALESCE 表达式并指定别名：COALESCE(column, args...) as alias。
// 最后一个参数为 alias。
//
// 示例：
//
//	table.Select(table.CoalesceAs("nickname", "'匿名用户'", "display_name"))
//	// → SELECT COALESCE(nickname, '匿名用户') as display_name
func (t *SharkTable) CoalesceAs(column string, args ...any) string {
	return sharksql.CoalesceAs(column, args...)
}

// IfNull 构建 IFNULL 表达式：IFNULL(column, defaultValue)。
// MySQL 特有函数，功能与 COALESCE 类似但只接受两个参数。
//
// 示例：
//
//	table.Select(table.IfNull("remark", "'无备注'"))
//	// → SELECT IFNULL(remark, '无备注')
func (t *SharkTable) IfNull(column string, defaultValue string) string {
	return sharksql.IfNull(column, defaultValue)
}

// IfNullAs 构建 IFNULL 表达式并指定别名：IFNULL(column, defaultValue) as alias。
//
// 示例：
//
//	table.Select(table.IfNullAs("remark", "'无备注'", "remark_text"))
//	// → SELECT IFNULL(remark, '无备注') as remark_text
func (t *SharkTable) IfNullAs(column string, defaultValue string, alias string) string {
	return sharksql.IfNullAs(column, defaultValue, alias)
}

// Case 构建参数化 CASE 表达式：CASE column WHEN ? THEN ? ... END，返回 (sql, args) 元组。
//
// 示例：
//
//	sql, args := table.Case("status", 0, "待支付", 1, "已支付", "未知")
//	// sql:  CASE status WHEN ? THEN ? WHEN ? THEN ? ELSE ? END
//	// args: [0, 待支付, 1, 已支付, 未知]
func (t *SharkTable) Case(column string, pairs ...any) string {
	return sharksql.Case(column, pairs...)
}

// CaseAs 构建 CASE 表达式并指定别名，返回 string。
func (t *SharkTable) CaseAs(column string, alias string, pairs ...any) string {
	return sharksql.CaseAs(column, alias, pairs...)
}

// When 构建 WHEN ... THEN ... 表达式片段。
func (t *SharkTable) When(args ...any) string {
	return sharksql.When(args...)
}

// WhenAs 构建 WHEN ... THEN ... 表达式片段并指定别名。
func (t *SharkTable) WhenAs(alias string, args ...any) string {
	return sharksql.WhenAs(alias, args...)
}

// ========== JSON 方法 ==========

// JsonExtract 添加 JSON_EXTRACT 到 SELECT。
// 用于从 JSON 列中提取字段。
//
// 示例：
//
//	table.Select(table.JsonExtract("metadata", "$.name"))
//	// → SELECT JSON_EXTRACT(metadata, '$.name')
func (t *SharkTable) JsonExtract(column string, path ...string) string {
	return sharksql.JsonExtract(column, path...)
}

// JsonUnquote 添加 JSON_UNQUOTE(JSON_EXTRACT(...)) 到 SELECT。
func (t *SharkTable) JsonUnquote(column string, path string) string {
	return sharksql.JsonUnquote(column, path)
}

// JsonSearchOne 添加 JSON_SEARCH 条件。
// value 为空时自动跳过。
//
// 示例：
//
//	table.JsonSearchOne("tags", "vip")
//	// → WHERE JSON_SEARCH(tags, 'one', ?) IS NOT NULL
func (t *SharkTable) JsonSearchOne(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		sql, data := sharksql.JsonSearchOne(column, value)
		t.db = t.db.Where(sql, data)
	}
	return t
}

// JsonContains 添加 JSON_CONTAINS 条件。
// value 为空时自动跳过。
//
// 示例：
//
//	table.JsonContains("roles", `"admin"`)
//	// → WHERE JSON_CONTAINS(roles, ?)
func (t *SharkTable) JsonContains(column string, value any) *SharkTable {
	if !t.isEmpty(value) {
		sql, data := sharksql.JsonContains(column, value)
		t.db = t.db.Where(sql, data)
	}
	return t
}

// JsonLength 添加 JSON_LENGTH 到 SELECT。
func (t *SharkTable) JsonLength(column string) string {
	return sharksql.JsonLength(column)
}

// JsonKeys 添加 JSON_KEYS 到 SELECT。
func (t *SharkTable) JsonKeys(column string) string {
	return sharksql.JsonKeys(column)
}

// JsonType 添加 JSON_TYPE 到 SELECT。
func (t *SharkTable) JsonType(column string) string {
	return sharksql.JsonType(column)
}

// Group 添加分组条件，多个字段以逗号拼接。
//
// 示例：
//
//	table.Group("category", "status")
//	// → GROUP BY category, status
func (t *SharkTable) Group(columns ...string) *SharkTable {
	if len(columns) == 0 {
		return t
	}
	t.db = t.db.Group(strings.Join(columns, ", "))
	return t
}

// Or 以 OR 方式添加 sharksql.SqlBuilder 构建的条件。
// builder 为 nil 或 Build 为空时自动跳过。
// 支持传入多个 builder，之间以 OR 连接。
//
// 示例：
//
//	// WHERE (name LIKE '%张%') OR (phone LIKE '%138%')
//	b1 := sharksql.NewSql().Like("name", "张")
//	b2 := sharksql.NewSql().Like("phone", "138")
//	table.Or(b1, b2)
//
//	// AND 嵌套 OR：查询待处理或处理中的工单
//	// WHERE (deleted = 0) AND ((status = 'pending') OR (status = 'in_progress'))
//	b := sharksql.NewSql().Eq("status", "pending").Or(sharksql.NewSql().Eq("status", "in_progress"))
//	table.Eq("deleted", 0).Or(b)
func (t *SharkTable) Or(builder ...*sharksql.SqlBuilder) *SharkTable {
	if len(builder) == 0 {
		return t
	}
	var orSQL []string
	var args []any
	for _, b := range builder {
		if b == nil {
			continue
		}
		sql, a := b.Build()
		if sql == "" {
			continue
		}
		orSQL = append(orSQL, sql)
		args = append(args, a...)
	}
	if len(orSQL) == 0 {
		return t
	}
	s := strings.Join(orSQL, " OR ")
	t.db = t.db.Where(s, args...)
	return t
}

func (t *SharkTable) ToFindSql() string {
	tx := t.db.Session(&gorm.Session{DryRun: true}).Find(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

func (t *SharkTable) ToCountSql() string {
	tx := t.db.Session(&gorm.Session{DryRun: true}).Count(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

func (t *SharkTable) ToDeleteSql() string {
	tx := t.db.Session(&gorm.Session{DryRun: true}).Delete(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

func (t *SharkTable) ToUpdateSql(values any) string {
	tx := t.db.Session(&gorm.Session{DryRun: true}).Updates(values)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

func (t *SharkTable) ToInsertSql(values any) string {
	tx := t.db.Session(&gorm.Session{DryRun: true}).Create(values)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

func (t *SharkTable) ToTakeSql() string {
	tx := t.db.Session(&gorm.Session{DryRun: true}).Take(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}
