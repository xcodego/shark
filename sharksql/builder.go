package sharksql

import (
	"fmt"
	"reflect"
	"strings"
)

// group 是一个条件组，同一组内的条件以 AND 连接。
//
// 字段说明：
//   - conditions: SQL 条件表达式切片（如 "name = ?", "age >= ?"），不含参数值
//   - args:       与条件表达式对应的参数值切片，顺序与 conditions 一一对应
//
// 同一 group 内的条件在 Build 时用 AND 连接，多个条件时自动包裹括号。
// 不同 group 之间在 Build 时用 OR 连接。
type group struct {
	conditions []string // SQL 条件表达式（参数化占位符 ? 形式）
	args       []any    // 条件参数值
}

// SqlBuilder 是动态 SQL 条件构建器，提供流式 API 构建参数化的 WHERE/ON 子句。
//
// 核心设计规则：
//   - 同一 group 内的条件以 AND 连接。
//   - 不同 group 之间以 OR 连接。
//   - 空值（nil / 空切片 / 空 map）自动跳过，无需手动判空。
//   - IN/NOT IN 的 value 必须是切片/数组，否则自动跳过。
//   - 链式调用：除 Build() 外，所有方法返回 *SqlBuilder 自身，支持链式组合。
//   - 参数化查询：所有值通过 ? 占位符传递，天然防止 SQL 注入。
//
// 两类条件方法：
//   - 字段 vs 值：Eq / Neq / Gt / Gte / Lt / Lte 等，值通过 ? 占位符参数化。
//     适用于 WHERE 子句和 ON 子句中与常量比较的场景。
//   - 字段 vs 字段：EqCol / NeqCol / GtCol / GteCol / LtCol / LteCol 等，
//     直接拼接列名，不经过参数化。适用于 JOIN ON 子句中表字段关联的场景。
//
// 使用示例：
//
//	// 1. 简单等值查询
//	b := sharksql.NewSql().Eq("name", "张三")
//	sql, args := b.Build()
//	// sql:  name = ?
//	// args: [张三]
//
//	// 2. 多条件 AND 查询（同一 group）
//	b := sharksql.NewSql().Eq("status", 1).Gte("age", 18).Lt("age", 60)
//	sql, args := b.Build()
//	// sql:  (status = ? AND age >= ? AND age < ?)
//	// args: [1, 18, 60]
//
//	// 3. OR 查询（不同 group）
//	b := sharksql.NewSql().Eq("created_by", uid).Or(sharksql.NewSql().Eq("assignee", uid))
//	sql, args := b.Build()
//	// sql:  (created_by = ?) OR (assignee = ?)
//	// args: [uid, uid]
//
//	// 4. (a = ?) AND (b = ? OR c = ?)
//	b := sharksql.NewSql().Eq("a", 1)
//	sub := sharksql.NewSql().Eq("b", 2).Or(sharksql.NewSql().Eq("c", 3))
//	b.And(sub)
//	sql, args := b.Build()
//	// sql:  (a = ? AND (b = ? OR c = ?))
//	// args: [1, 2, 3]
//
//	// 5. 真实业务：查询"待处理或处理中，且为本人相关"的任务
//	b := sharksql.NewSql().
//	    Eq("deleted", 0).
//	    And(sharksql.NewSql().
//	        Eq("status", "pending").Or(sharksql.NewSql().Eq("status", "in_progress"))).
//	    And(sharksql.NewSql().
//	        Eq("created_by", uid).Or(sharksql.NewSql().Eq("assignee", uid)))
//	sql, args := b.Build()
//	// sql:  (deleted = ? AND (status = ? OR status = ?) AND (created_by = ? OR assignee = ?))
//	// args: [0, pending, in_progress, uid, uid]
//
//	// 6. 区间查询 Between（左闭右开）
//	b := sharksql.NewSql().Between("created_at", startTime, endTime)
//	// sql:  (created_at >= ? AND created_at < ?)
//
//	// 7. 模糊搜索 + 空值自动跳过
//	b := sharksql.NewSql().Eq("type", "").Like("title", "订单").LikeRight("code", "ORD")
//	sql, args := b.Build()
//	// type 为空被跳过 → sql:  (title LIKE ? AND code LIKE ?)
//	// args: [%订单%, ORD%]
//
//	// 8. 配合 GORM 使用
//	b := sharksql.NewSql().Eq("status", 1).Like("name", "张")
//	sql, args := b.Build()
//	db.Where(sql, args...).Find(&users)
//
//	// 9. 与 sharksql 条件函数配合
//	db.Where(sharksql.Eq("org_id", orgID)).Where(
//	    sharksql.NewSql().
//	        Like("tags", "紧急").
//	        Or(sharksql.NewSql().Like("tags", "重要")).
//	        Build(),
//	).Find(&tasks)
//
//	// 10. JOIN ON 子句：字段对字段的比较
//	db.Joins("LEFT JOIN orders o ON u.id = o.user_id").
//	    Where(sharksql.NewSql().EqCol("u.id", "o.user_id").Eq("o.status", "paid").Build()).
//	    Find(&results)
//	// sql:  u.id = o.user_id AND o.status = ?
//	// args: [paid]
//
//	// 11. 多字段 ON 条件
//	onB := sharksql.NewSql().
//	    EqCol("u.id", "o.user_id").
//	    EqCol("u.org_id", "o.org_id").
//	    Eq("o.deleted", 0)
//	sql, args := onB.Build()
//	// sql:  (u.id = o.user_id AND u.org_id = o.org_id AND o.deleted = ?)
//	// args: [0]
type SqlBuilder struct {
	groups []group // 条件组切片，每组内 AND 连接，组间 OR 连接
}

// NewSql 创建一个空的 SqlBuilder 实例。
//
// 使用示例：
//
//	// 创建 SqlBuilder 并链式构建条件
//	b := sharksql.NewSql().
//	    Eq("status", 1).
//	    Gte("age", 18).
//	    Like("name", "张")
//	sql, args := b.Build()
//	db.Where(sql, args...).Find(&users)
func NewSql() *SqlBuilder {
	return &SqlBuilder{
		groups: []group{},
	}
}

// isEmpty 递归判断值是否为"空值"，支持以下类型：
//   - nil
//   - nil 指针（*T = nil）
//   - nil interface
//   - 空切片（len = 0）
//   - 空 map（len = 0）
//
// 对所有值类型参数（如 int=0, string=""）返回 false —— 零值不等于空值。
// 字符串 "" 不被视为空，若想跳过空字符串，调用方应自行提前判断。
func (t *SqlBuilder) isEmpty(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return true
		}
		// 递归解引用（处理 **T 等多层指针）
		return t.isEmpty(rv.Elem().Interface())
	case reflect.Slice, reflect.Map:
		return rv.Len() == 0
	}
	return false
}

// isSlice 判断值是否为切片或数组类型。
// 用于 IN/NOT IN 的条件校验：只有切片/数组才能展开为 IN (?) 语句。
func (t *SqlBuilder) isSlice(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		return true
	default:
		return false
	}
}

// current 返回当前（最后一个）group 的指针。
// 若 groups 为空，自动追加一个空 group。
//
// 所有链式条件方法（Eq/Gte/Like 等）都通过 current() 获取目标 group，
// 因此新条件默认追加到当前 group（即 AND 连接）。
func (b *SqlBuilder) current() *group {
	if len(b.groups) == 0 {
		b.groups = append(b.groups, group{})
	}
	return &b.groups[len(b.groups)-1]
}

// ========== 比较运算符（字段 vs 值，参数化）==========

// Eq 添加等值条件：column = ?。
//
// 参数：
//   - column: 列名
//   - value:  匹配值。为空值（nil/空切片/空map）时自动跳过
//
// 返回：*SqlBuilder 自身，支持链式调用
//
// 示例：
//
//	// 单条件
//	b.Eq("status", 1)
//	// SQL: status = ?
//
//	// 空值跳过
//	b.Eq("name", nil).Eq("status", 1)
//	// SQL: status = ?  （name 条件被跳过）
//
//	// 链式组合
//	b.Eq("status", 1).Eq("deleted", 0).Gte("age", 18)
//	// SQL: (status = ? AND deleted = ? AND age >= ?)
func (b *SqlBuilder) Eq(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" = ?")
	g.args = append(g.args, value)
	return b
}

// Neq 添加不等条件：column <> ?。
//
// 参数：同 Eq
// 返回：*SqlBuilder 自身
//
// 示例：
//
//	b.Neq("status", 0)
//	// SQL: status <> ?
//
//	// 排除已删除的记录
//	b.Eq("org_id", orgID).Neq("deleted", 1)
//	// SQL: (org_id = ? AND deleted <> ?)
func (b *SqlBuilder) Neq(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" <> ?")
	g.args = append(g.args, value)
	return b
}

// Gt 添加大于条件：column > ?。
//
// 示例：
//
//	// 查询 18 岁以上的用户
//	b.Gt("age", 18)
//	// SQL: age > ?
//
//	// 查询最近 7 天的记录
//	sevenDaysAgo := time.Now().Add(-7 * 24 * time.Hour)
//	b.Gt("created_at", sevenDaysAgo)
//	// SQL: created_at > ?
func (b *SqlBuilder) Gt(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" > ?")
	g.args = append(g.args, value)
	return b
}

// Gte 添加大于等于条件：column >= ?。
//
// 示例：
//
//	// 金额 >= 100 的订单
//	b.Gte("amount", 100)
//	// SQL: amount >= ?
//
//	// 及格线（60 分及以上）
//	b.Gte("score", 60)
//	// SQL: score >= ?
func (b *SqlBuilder) Gte(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" >= ?")
	g.args = append(g.args, value)
	return b
}

// Lt 添加小于条件：column < ?。
//
// 示例：
//
//	b.Lt("price", 5000)
//	// SQL: price < ?
//
//	// 查询库存不足的商品
//	b.Lt("stock", 10)
//	// SQL: stock < ?
func (b *SqlBuilder) Lt(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" < ?")
	g.args = append(g.args, value)
	return b
}

// Lte 添加小于等于条件：column <= ?。
//
// 示例：
//
//	// 价格 <= 100 的商品
//	b.Lte("price", 100)
//	// SQL: price <= ?
//
//	// 查询过期任务
//	b.Lte("deadline", time.Now())
//	// SQL: deadline <= ?
func (b *SqlBuilder) Lte(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" <= ?")
	g.args = append(g.args, value)
	return b
}

// ========== 比较运算符（字段 vs 字段，直接拼接列名）==========
//
// 以下方法用于 JOIN ON 子句中表字段关联的场景。
// 与 Eq/Neq/Gt 等不同，这些方法直接将两个列名拼入 SQL 片段，
// 不经过 ? 占位符参数化（列名是硬编码的，不是用户输入，不存在注入风险）。
//
// 使用示例：
//
//	// LEFT JOIN orders o ON u.id = o.user_id AND o.deleted = 0
//	onB := sharksql.NewSql().EqCol("u.id", "o.user_id").Eq("o.deleted", 0)
//	sql, args := onB.Build()
//	// sql:  (u.id = o.user_id AND o.deleted = ?)
//	// args: [0]
//	db.Joins("LEFT JOIN orders o ON " + sql, args...)

// EqCol 添加等值条件：column = otherColumn（字段对字段）。
//
// 示例：
//
//	// ON u.id = o.user_id
//	b.EqCol("u.id", "o.user_id")
//	// SQL: u.id = o.user_id (无参数化)
func (b *SqlBuilder) EqCol(column string, otherColumn string) *SqlBuilder {
	g := b.current()
	g.conditions = append(g.conditions, column+" = "+otherColumn)
	return b
}

// NeqCol 添加不等条件：column <> otherColumn（字段对字段）。
func (b *SqlBuilder) NeqCol(column string, otherColumn string) *SqlBuilder {
	g := b.current()
	g.conditions = append(g.conditions, column+" <> "+otherColumn)
	return b
}

// GtCol 添加大于条件：column > otherColumn（字段对字段）。
func (b *SqlBuilder) GtCol(column string, otherColumn string) *SqlBuilder {
	g := b.current()
	g.conditions = append(g.conditions, column+" > "+otherColumn)
	return b
}

// GteCol 添加大于等于条件：column >= otherColumn（字段对字段）。
func (b *SqlBuilder) GteCol(column string, otherColumn string) *SqlBuilder {
	g := b.current()
	g.conditions = append(g.conditions, column+" >= "+otherColumn)
	return b
}

// LtCol 添加小于条件：column < otherColumn（字段对字段）。
func (b *SqlBuilder) LtCol(column string, otherColumn string) *SqlBuilder {
	g := b.current()
	g.conditions = append(g.conditions, column+" < "+otherColumn)
	return b
}

// LteCol 添加小于等于条件：column <= otherColumn（字段对字段）。
func (b *SqlBuilder) LteCol(column string, otherColumn string) *SqlBuilder {
	g := b.current()
	g.conditions = append(g.conditions, column+" <= "+otherColumn)
	return b
}

// ========== 范围查询 ==========

// Between 添加左闭右开区间条件 [lo, hi)：column >= ? AND column < ?。
//
// lo 或 hi 任一为空值时，整个条件跳过。
//
// 示例：
//
//	// 年龄在 [18, 60) 区间的用户
//	b.Between("age", 18, 60)
//	// SQL: age >= ? AND age < ?
//
//	// 本月订单
//	startOfMonth := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
//	endOfMonth := startOfMonth.AddDate(0, 1, 0)
//	b.Between("created_at", startOfMonth, endOfMonth)
//	// SQL: created_at >= ? AND created_at < ?
//
//	// 任一值为 nil 时跳过
//	b.Between("age", 18, nil) // 整个条件被跳过
func (b *SqlBuilder) Between(column string, lo, hi any) *SqlBuilder {
	if b.isEmpty(lo) || b.isEmpty(hi) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" >= ?", column+" < ?")
	g.args = append(g.args, lo, hi)
	return b
}

// ========== 模糊匹配运算符 ==========

// Like 添加模糊匹配条件：column LIKE '%value%'（前后通配符）。
// 为空值时跳过。
//
// 示例：
//
//	// 搜索名字中包含"张"的用户
//	b.Like("name", "张")
//	// SQL: name LIKE ?
//	// args: [%张%]
//
//	// 多字段模糊搜索
//	keyword := "订单"
//	b.Like("title", keyword).Or(sharksql.NewSql().Like("description", keyword))
//	// SQL: (title LIKE ?) OR (description LIKE ?)
func (b *SqlBuilder) Like(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" LIKE ?")
	g.args = append(g.args, "%"+fmt.Sprint(value)+"%")
	return b
}

// NotLike 添加反向模糊匹配条件：column NOT LIKE '%value%'。
// 为空值时跳过。
//
// 示例：
//
//	// 排除名称中包含"test"的记录
//	b.NotLike("name", "test")
//	// SQL: name NOT LIKE ?
//	// args: [%test%]
func (b *SqlBuilder) NotLike(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" NOT LIKE ?")
	g.args = append(g.args, "%"+fmt.Sprint(value)+"%")
	return b
}

// LikeLeft 添加后缀匹配条件：column LIKE '%value'，匹配以 value 结尾的字符串。
// 为空值时跳过。
//
// 示例：
//
//	// 查询 QQ 邮箱用户
//	b.LikeLeft("email", "@qq.com")
//	// SQL: email LIKE ?
//	// args: [%@qq.com]
//
//	// 查询以特定后缀结尾的文件名
//	b.LikeLeft("filename", ".pdf")
//	// SQL: filename LIKE ?
//	// args: [%.pdf]
func (b *SqlBuilder) LikeLeft(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" LIKE ?")
	g.args = append(g.args, "%"+fmt.Sprint(value))
	return b
}

// LikeRight 添加前缀匹配条件：column LIKE 'value%'，匹配以 value 开头的字符串。
// 为空值时跳过。
//
// 示例：
//
//	// 查询 138 开头的手机号
//	b.LikeRight("phone", "138")
//	// SQL: phone LIKE ?
//	// args: [138%]
//
//	// 查询以 ORD 开头的订单编号
//	b.LikeRight("order_code", "ORD")
//	// SQL: order_code LIKE ?
//	// args: [ORD%]
func (b *SqlBuilder) LikeRight(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" LIKE ?")
	g.args = append(g.args, fmt.Sprint(value)+"%")
	return b
}

// ========== 集合运算符 ==========

// In 添加 IN 条件：column IN (?, ?, ...)。
//
// 参数：
//   - column: 列名
//   - value:  必须是切片/数组（如 []int{1,2,3}），GORM 会自动展开
//
// 条件跳过：
//   - value 为空值（nil / 空切片）
//   - value 不是切片/数组类型
//
// 示例：
//
//	// 查询状态为 1/2/3 的记录
//	b.In("status", []int{1, 2, 3})
//	// SQL: status IN ?
//
//	// 查询指定城市的用户
//	b.In("city", []string{"北京", "上海", "深圳"})
//	// SQL: city IN ?
//
//	// 空切片自动跳过
//	b.In("status", []int{}) // 跳过
func (b *SqlBuilder) In(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	if !b.isSlice(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" IN (?)")
	g.args = append(g.args, value)
	return b
}

// NotIn 添加 NOT IN 条件：column NOT IN (?, ?, ...)。
//
// 跳过规则同 In。
//
// 示例：
//
//	// 排除 id 为 100/200 的记录
//	b.NotIn("id", []int64{100, 200})
//	// SQL: id NOT IN ?
//
//	// 排除黑名单用户
//	b.NotIn("user_id", blacklist)
//	// SQL: user_id NOT IN ?
func (b *SqlBuilder) NotIn(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	if !b.isSlice(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" NOT IN (?)")
	g.args = append(g.args, value)
	return b
}

// ========== NULL 运算符 ==========

// IsNull 添加 IS NULL 条件。
//
// 示例：
//
//	// 查询已软删除的记录
//	b.IsNull("deleted_at")
//	// SQL: deleted_at IS NULL
//
//	// 查询未设置邮箱的用户
//	b.Eq("status", 1).IsNull("email")
//	// SQL: (status = ? AND email IS NULL)
func (b *SqlBuilder) IsNull(column string) *SqlBuilder {
	g := b.current()
	g.conditions = append(g.conditions, column+" IS NULL")
	return b
}

// IsNotNull 添加 IS NOT NULL 条件。
//
// 示例：
//
//	// 查询已绑定手机号的用户
//	b.IsNotNull("phone")
//	// SQL: phone IS NOT NULL
//
//	// 查询有备注的记录
//	b.IsNotNull("remark").Eq("status", 1)
//	// SQL: (remark IS NOT NULL AND status = ?)
func (b *SqlBuilder) IsNotNull(column string) *SqlBuilder {
	g := b.current()
	g.conditions = append(g.conditions, column+" IS NOT NULL")
	return b
}

// ========== 逻辑组合运算符 ==========

// Or 以 OR 方式合并另一个 SqlBuilder 的全部条件。
//
// 合并规则：
//   - other 为 nil 或无有效条件时跳过，不做任何修改。
//   - other.groups 直接追加到当前 SqlBuilder 的 groups 末尾。
//   - Build 时，不同 group 之间自动用 OR 连接。
//
// 示例：
//
//	// 查询"我创建的"或"指派给我的"任务
//	b := sharksql.NewSql().Eq("created_by", uid)
//	b.Or(sharksql.NewSql().Eq("assignee", uid))
//	sql, args := b.Build()
//	// SQL:  (created_by = ?) OR (assignee = ?)
//
//	// 多层 OR：查询状态为待处理/处理中/已完成的任务
//	b := sharksql.NewSql().Eq("status", "pending")
//	b.Or(sharksql.NewSql().Eq("status", "in_progress"))
//	b.Or(sharksql.NewSql().Eq("status", "done"))
//	// SQL:  (status = ?) OR (status = ?) OR (status = ?)
func (b *SqlBuilder) Or(other *SqlBuilder) *SqlBuilder {
	if other == nil {
		return b
	}
	if len(other.groups) == 0 {
		return b
	}
	b.groups = append(b.groups, other.groups...)
	return b
}

// And 以 AND 方式合并另一个 SqlBuilder 的全部条件。
//
// 合并规则：
//   - other 为 nil 或无有效条件时跳过。
//   - 若 other 只有单个 group（纯 AND 条件）：条件直接追加到当前 group，不产生括号。
//   - 若 other 有多个 group（含 OR 子句）：先调用 other.Build() 生成子表达式，再用括号包裹后追加。
//
// 与 Or 的区别：
//   - Or 将 other.groups 追加到 groups 末尾（组间 OR）。
//   - And 将 other 的所有条件合并到当前 group（组内 AND），如果含 OR 则自动加括号。
//
// 示例：
//
//	// 单 group（纯 AND）：条件直接扁平化合并
//	b := sharksql.NewSql().Eq("a", 1)
//	other := sharksql.NewSql().Eq("b", 2).Eq("c", 3)
//	b.And(other)
//	// SQL:  (a = ? AND b = ? AND c = ?)  ← 不加括号，扁平化
//
//	// 多 group（含 OR）：自动加括号嵌套
//	b := sharksql.NewSql().Eq("deleted", 0)
//	sub := sharksql.NewSql().Eq("status", "pending").Or(sharksql.NewSql().Eq("status", "in_progress"))
//	b.And(sub)
//	// SQL:  (deleted = ? AND (status = ? OR status = ?))
//
//	// 复杂业务：未删除 + 本人相关 + 特定状态
//	b := sharksql.NewSql().Eq("deleted", 0)
//	b.And(sharksql.NewSql().
//	    Eq("created_by", uid).Or(sharksql.NewSql().Eq("assignee", uid)))
//	b.And(sharksql.NewSql().
//	    Eq("status", "pending").Or(sharksql.NewSql().Eq("status", "in_progress")))
//	// SQL:  (deleted = ? AND (created_by = ? OR assignee = ?) AND (status = ? OR status = ?))
func (b *SqlBuilder) And(other *SqlBuilder) *SqlBuilder {
	if other == nil {
		return b
	}
	if len(other.groups) == 0 {
		return b
	}
	g := b.current()
	// 单 group：扁平化合并，不产生额外括号
	if len(other.groups) == 1 {
		g.conditions = append(g.conditions, other.groups[0].conditions...)
		g.args = append(g.args, other.groups[0].args...)
		return b
	}
	// 多 group（含 OR）：先构建子表达式，再用括号包裹
	subSQL, subArgs := other.Build()
	if subSQL == "" {
		return b
	}
	g.conditions = append(g.conditions, "("+subSQL+")")
	g.args = append(g.args, subArgs...)
	return b
}

// Build 生成最终的 WHERE 子句字符串和参数列表。
//
// 构建规则：
//  1. 遍历所有 group，跳过空 group
//  2. 同一 group 内的条件用 AND 连接；多个条件时自动包裹括号
//  3. 不同 group 之间用 OR 连接
//  4. 若所有条件均为空，返回空字符串和 nil 切片
//
// 返回值：
//   - string: 参数化的 WHERE 子句（可直接拼接到 "WHERE" 后）
//   - []any:  与占位符 ? 一一对应的参数值切片
//
// 与 GORM 集成示例：
//
//	// 方式一：直接传递给 Where
//	b := sharksql.NewSql().Eq("status", 1).Like("name", "张")
//	sql, args := b.Build()
//	db.Where(sql, args...).Find(&users)
//
//	// 方式二：在已有查询基础上追加条件
//	db := gormDB.Model(&Task{}).Where("org_id = ?", orgID)
//	b := sharksql.NewSql().Eq("status", "pending").Gte("priority", 3)
//	sql, args := b.Build()
//	db.Where(sql, args...).Find(&tasks)
//
//	// 方式三：组合多个 SqlBuilder
//	baseB := sharksql.NewSql().Eq("org_id", orgID).Eq("deleted", 0)
//	filterB := sharksql.NewSql().Like("title", keyword).Or(
//	    sharksql.NewSql().Like("description", keyword))
//	baseB.And(filterB)
//	sql, args := baseB.Build()
//	db.Where(sql, args...).Find(&results)
//
//	// 方式四：JOIN ON 子句
//	onB := sharksql.NewSql().EqCol("u.id", "o.user_id").Eq("o.deleted", 0)
//	onSQL, onArgs := onB.Build()
//	db.Joins("LEFT JOIN orders o ON "+onSQL, onArgs...).Find(&results)
//
// Build 输出示例：
//
//	// 无有效条件
//	b := sharksql.NewSql().Eq("name", "") // 注意："" 不是空值，不会被跳过
//	// 若所有条件都被跳过 → sql: "", args: nil
//
//	// 单条件
//	b := sharksql.NewSql().Eq("id", 1)
//	// sql: "id = ?", args: [1]
//
//	// 多条件 AND
//	b := sharksql.NewSql().Eq("a", 1).Gte("b", 10).Lt("b", 20)
//	// sql: "(a = ? AND b >= ? AND b < ?)", args: [1, 10, 20]
//
//	// AND + OR 嵌套
//	b := sharksql.NewSql().Eq("x", 1).Or(sharksql.NewSql().Eq("y", 2))
//	// sql: "(x = ?) OR (y = ?)", args: [1, 2]
//
//	// 字段对字段（无参数化）
//	b := sharksql.NewSql().EqCol("u.id", "o.user_id")
//	// sql: "u.id = o.user_id", args: nil
func (b *SqlBuilder) Build() (string, []any) {
	var sb strings.Builder
	var args []any
	first := true
	for _, group := range b.groups {
		// 跳过空 group（所有条件都因空值被跳过）
		if len(group.conditions) == 0 {
			continue
		}
		// 不同 group 之间用 OR 连接（第一个 group 不加前缀）
		if !first {
			sb.WriteString(" OR ")
		}
		// 同一 group 内条件数 > 1 时加括号
		if len(group.conditions) > 1 {
			sb.WriteString("(")
		}
		// 同一 group 内的条件用 AND 连接
		sb.WriteString(strings.Join(group.conditions, " AND "))
		if len(group.conditions) > 1 {
			sb.WriteString(")")
		}
		args = append(args, group.args...)
		first = false
	}
	return sb.String(), args
}
