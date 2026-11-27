package sharksql

import (
	"fmt"
	"reflect"
	"strings"
)

// group 保存以 AND 连接的条件及其参数。
type group struct {
	conditions []string
	args       []any
}

// SqlBuilder 按组构建参数化条件：组内用 AND，组间用 OR（通过 Or 开启新组）。
// 值为空（nil、nil 指针、空切片/map，零值不算）的条件会被跳过；
// 列名直接拼入 SQL，必须是可信的固定列名。
type SqlBuilder struct {
	groups []group
}

// NewSql 创建一个空的 SqlBuilder 实例。
func NewSql() *SqlBuilder {
	return &SqlBuilder{
		groups: []group{},
	}
}

// isEmpty 判断 nil、nil 指针、空切片和空 map；零值（0、""、false）不视为空。
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
		// 递归解引用多层指针
		return t.isEmpty(rv.Elem().Interface())
	case reflect.Slice, reflect.Map:
		return rv.Len() == 0
	}
	return false
}

// isSlice 判断值是否为切片或数组。
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

// current 返回最后一个条件组；必要时创建空组。
func (b *SqlBuilder) current() *group {
	if len(b.groups) == 0 {
		b.groups = append(b.groups, group{})
	}
	return &b.groups[len(b.groups)-1]
}

// Eq 添加等值条件：column = ?。
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
func (b *SqlBuilder) Lte(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" <= ?")
	g.args = append(g.args, value)
	return b
}

// EqCol 添加等值条件：column = otherColumn（字段对字段）。
// 常用于 JOIN ON。以下 *Col 方法同理：两个列名直接拼入 SQL、不参数化，必须是可信的固定列名，也不会因空值被跳过。
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

// Between 添加左闭右开区间条件 [lo, hi)：column >= ? AND column < ?。
// lo 或 hi 任一为空值时，整个条件跳过。
func (b *SqlBuilder) Between(column string, lo, hi any) *SqlBuilder {
	if b.isEmpty(lo) || b.isEmpty(hi) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" >= ?", column+" < ?")
	g.args = append(g.args, lo, hi)
	return b
}

// Like 添加模糊匹配条件：column LIKE '%value%'（前后通配符）。
// 为空值时跳过。
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
func (b *SqlBuilder) LikeRight(column string, value any) *SqlBuilder {
	if b.isEmpty(value) {
		return b
	}
	g := b.current()
	g.conditions = append(g.conditions, column+" LIKE ?")
	g.args = append(g.args, fmt.Sprint(value)+"%")
	return b
}

// In 添加 IN 条件：column IN (?)，GORM 会自动展开切片。
// value 为空值或不是切片/数组时跳过。
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
// 跳过规则同 In。
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

// IsNull 添加 IS NULL 条件。
func (b *SqlBuilder) IsNull(column string) *SqlBuilder {
	g := b.current()
	g.conditions = append(g.conditions, column+" IS NULL")
	return b
}

// IsNotNull 添加 IS NOT NULL 条件。
func (b *SqlBuilder) IsNotNull(column string) *SqlBuilder {
	g := b.current()
	g.conditions = append(g.conditions, column+" IS NOT NULL")
	return b
}

// Or 以 OR 方式合并另一个 SqlBuilder：other 的条件组追加到当前组之后，Build 时组间用 OR 连接。
// other 为 nil 或没有条件组时不做修改。
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

// And 以 AND 方式合并另一个 SqlBuilder 到当前（最后一个）条件组。
// other 为 nil 或没有条件组时不做修改；only 单组时条件直接追加，
// 多组（含 OR）时先 Build 再用括号包裹后追加，保证 OR 优先级正确。
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

// Build 生成参数化的条件字符串（不含 WHERE 关键字）及与 ? 一一对应的参数。
// 空组被跳过；组内条件用 AND 连接，多于一个时加括号；组间用 OR 连接。
// 没有任何条件时返回 "" 和 nil。
func (b *SqlBuilder) Build() (string, []any) {
	var sb strings.Builder
	var args []any
	first := true
	for _, group := range b.groups {
		// 跳过空组（条件都因空值被跳过）
		if len(group.conditions) == 0 {
			continue
		}
		if !first {
			sb.WriteString(" OR ")
		}
		if len(group.conditions) > 1 {
			sb.WriteString("(")
		}
		sb.WriteString(strings.Join(group.conditions, " AND "))
		if len(group.conditions) > 1 {
			sb.WriteString(")")
		}
		args = append(args, group.args...)
		first = false
	}
	return sb.String(), args
}
