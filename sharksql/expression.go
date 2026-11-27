package sharksql

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// AddCol 构建字段对字段加法表达式：column + otherColumn。
// 用于 SELECT 子句中将两个字段值相加。
func AddCol(column string, otherColumn string) string {
	return column + " + " + otherColumn
}

// AddColAs 构建字段对字段加法表达式并指定别名：(column + otherColumn) as alias。
// 用于 SELECT 子句中将两个字段值相加并指定别名。
func AddColAs(column string, otherColumn string, as string) string {
	return fmt.Sprintf("(%v + %v) as %v", column, otherColumn, as)
}

// SubCol 构建字段对字段减法表达式：column - otherColumn。
// 用于 SELECT 子句中将两个字段值相减。
func SubCol(column string, otherColumn string) string {
	return column + " - " + otherColumn
}

// SubColAs 构建字段对字段减法表达式并指定别名：(column - otherColumn) as alias。
// 用于 SELECT 子句中将两个字段值相减并指定别名。
func SubColAs(column string, otherColumn string, as string) string {
	return fmt.Sprintf("(%v - %v) as %v", column, otherColumn, as)
}

// MulCol 构建字段对字段乘法表达式：column * otherColumn。
// 用于 SELECT 子句中将两个字段值相乘。
func MulCol(column string, otherColumn string) string {
	return column + " * " + otherColumn
}

// MulColAs 构建字段对字段乘法表达式并指定别名：(column * otherColumn) as alias。
// 用于 SELECT 子句中将两个字段值相乘并指定别名。
func MulColAs(column string, otherColumn string, as string) string {
	return fmt.Sprintf("(%v * %v) as %v", column, otherColumn, as)
}

// DivCol 构建字段对字段除法表达式：column / otherColumn。
// 用于 SELECT 子句中将两个字段值相除。
func DivCol(column string, otherColumn string) string {
	return column + " / " + otherColumn
}

// DivColAs 构建字段对字段除法表达式并指定别名：(column / otherColumn) as alias。
// 用于 SELECT 子句中将两个字段值相除并指定别名。
func DivColAs(column string, otherColumn string, as string) string {
	return fmt.Sprintf("(%v / %v) as %v", column, otherColumn, as)
}

// As 为表达式添加别名：(expression) as alias。
// 可与任意字段表达式配合使用（AddCol/SubCol/MulCol/DivCol/聚合函数等）。
func As(expression string, alias string) string {
	return fmt.Sprintf("(%v) as %v", expression, alias)
}

// Paren 为列名或表达式添加括号：(column)。
func Paren(column string) string {
	return fmt.Sprintf("(%v)", column)
}

// Coalesce 构建 COALESCE(v1, v2, ...) 表达式，参数通过 fmt.Sprint 直接拼接。
func Coalesce(args ...any) string {
	ss := make([]string, len(args))
	for i, v := range args {
		ss[i] = fmt.Sprint(v)
	}
	return "COALESCE(" + strings.Join(ss, ", ") + ")"
}

// CoalesceAs 构建 COALESCE(column, args[:n-1]...) as alias，args 的最后一个元素为别名；args 为空时原样返回 column。
func CoalesceAs(column string, args ...any) string {
	if len(args) == 0 {
		return column
	}
	alias := fmt.Sprint(args[len(args)-1])
	values := make([]string, 1, len(args))
	values[0] = column
	for i := 0; i < len(args)-1; i++ {
		values = append(values, fmt.Sprint(args[i]))
	}
	return "COALESCE(" + strings.Join(values, ", ") + ") as " + alias
}

// IfNull 构建 IFNULL 表达式：IFNULL(column, defaultValue)。
// MySQL 特有函数，功能与 COALESCE 类似但只接受两个参数。
func IfNull(column string, value any) string {
	return fmt.Sprintf("IFNULL(%v, %v)", column, value)
}

// IfNullAs 构建 IFNULL 表达式并指定别名：IFNULL(column, defaultValue) as alias。
func IfNullAs(column string, value any, alias string) string {
	return fmt.Sprintf("IFNULL(%v, %v) as %v", column, value, alias)
}

// Case 构建 CASE 表达式：CASE column WHEN ... THEN ... [ELSE ...] END。
// pairs 为 WHEN/THEN 值对；个数为奇数时最后一个为 ELSE 值。
// 所有值通过 fmt.Sprint 直接拼入 SQL（非参数化），字符串值需自带引号。
func Case(column string, pairs ...any) string {
	var sb strings.Builder
	sb.WriteString("CASE ")
	sb.WriteString(column)

	total := len(pairs)
	hasElse := total%2 != 0
	pairCount := total
	if hasElse {
		pairCount = total - 1
	}

	for i := 0; i < pairCount; i += 2 {
		sb.WriteString(" WHEN ")
		sb.WriteString(fmt.Sprint(pairs[i]))
		sb.WriteString(" THEN ")
		sb.WriteString(fmt.Sprint(pairs[i+1]))
	}

	if hasElse {
		sb.WriteString(" ELSE ")
		sb.WriteString(fmt.Sprint(pairs[total-1]))
	}

	sb.WriteString(" END")
	return sb.String()
}

// CaseAs 构建带别名的 CASE 表达式：CASE ... END as alias，规则同 Case。
func CaseAs(column string, alias string, pairs ...any) string {
	return Case(column, pairs...) + " as " + alias
}

// When 构建 WHEN ... THEN ... 表达式片段；参数个数为奇数时最后一个为 ELSE 值。值同样直接拼入 SQL。
func When(args ...any) string {
	total := len(args)
	hasElse := total%2 != 0
	pairCount := total
	if hasElse {
		pairCount = total - 1
	}

	var sb strings.Builder
	for i := 0; i < pairCount; i += 2 {
		sb.WriteString("WHEN ")
		sb.WriteString(fmt.Sprint(args[i]))
		sb.WriteString(" THEN ")
		sb.WriteString(fmt.Sprint(args[i+1]))
		sb.WriteString(" ")
	}

	if hasElse {
		sb.WriteString("ELSE ")
		sb.WriteString(fmt.Sprint(args[total-1]))
	}

	return strings.TrimSpace(sb.String())
}

// WhenAs 构建带别名的 WHEN ... THEN ... 片段，规则同 When。
func WhenAs(alias string, args ...any) string {
	return When(args...) + " as " + alias
}

// Distinct 构建 DISTINCT col1, col2 表达式，columns 为空返回空串。
func Distinct(columns ...string) string {
	if len(columns) == 0 {
		return ""
	}
	if len(columns) == 1 {
		return "DISTINCT " + columns[0]
	}
	return "DISTINCT " + strings.Join(columns, ", ")
}

// Column 构建 "table.column" 格式的限定列名。
// 用于多表 JOIN 查询时消除列名歧义。
func Column(table string, column string) string {
	return fmt.Sprintf("%v.%v", table, column)
}

// ColumnAs 构建 "table.column AS alias" 格式的带别名的限定列名。
func ColumnAs(table string, column string, as string) string {
	return fmt.Sprintf("%v.%v as %v", table, column, as)
}

// BitOr 构建列与值的按位或表达式，返回 (sql 片段, 参数值)。
// sql 片段中使用 ? 占位符，参数值需作为 gorm 参数传入。
func BitOr(column string, value any) (string, any) {
	return column + " | ?", value
}

// BitAnd 构建列与值的按位与表达式，返回 (sql 片段, 参数值)。
// 常用于检查某个二进制标志位是否被置位。
func BitAnd(column string, value any) (string, any) {
	return column + " & ?", value
}

// BitXor 构建列与值的按位异或表达式，返回 (sql 片段, 参数值)。
func BitXor(column string, value any) (string, any) {
	return column + " ^ ?", value
}

// BitNot 构建列按位取反表达式：~column。
func BitNot(column string) string {
	return "~" + column
}

// BitShiftLeft 构建列按位左移表达式，返回 (sql 片段, 参数值)。
func BitShiftLeft(column string, value any) (string, any) {
	return column + " << ?", value
}

// BitShiftRight 构建列按位右移表达式，返回 (sql 片段, 参数值)。
func BitShiftRight(column string, value any) (string, any) {
	return column + " >> ?", value
}

// BitOrCol 构建字段对字段按位或表达式：column | otherColumn。
func BitOrCol(column string, otherColumn string) string {
	return column + " | " + otherColumn
}

// BitAndCol 构建字段对字段按位与表达式：column & otherColumn。
func BitAndCol(column string, otherColumn string) string {
	return column + " & " + otherColumn
}

// BitXorCol 构建字段对字段按位异或表达式：column ^ otherColumn。
func BitXorCol(column string, otherColumn string) string {
	return column + " ^ " + otherColumn
}

// BitShiftLeftCol 构建字段对字段按位左移表达式：column << otherColumn。
func BitShiftLeftCol(column string, otherColumn string) string {
	return column + " << " + otherColumn
}

// BitShiftRightCol 构建字段对字段按位右移表达式：column >> otherColumn。
func BitShiftRightCol(column string, otherColumn string) string {
	return column + " >> " + otherColumn
}

// CastAs 构建 CAST((column) AS type) 表达式；alias 参数实际是 SQL 目标类型（如 CHAR、SIGNED、DECIMAL(10,2)），而非别名。
func CastAs(column string, alias string) string {
	return fmt.Sprintf("CAST((%v) AS %v)", column, alias)
}

// DateFormat 构建 MySQL 日期格式化表达式：DATE_FORMAT(column, 'format')。
func DateFormat(format string, column string) string {
	return fmt.Sprintf("DATE_FORMAT(%v, '%v')", column, format)
}

// Negative 构建取负表达式：-column。
func Negative(column string) string {
	return "-" + column
}

// GormAs 构造子查询列表达式 "(?) AS alias" 并原样返回子查询 v，用于 gorm.Select(sharksql.GormAs(...))。
func GormAs(alias string, v *gorm.DB) (string, *gorm.DB) {
	return fmt.Sprintf("(?) AS %v", alias), v
}
