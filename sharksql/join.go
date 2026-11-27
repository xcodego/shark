package sharksql

// LeftJoin 构建 LEFT JOIN 子句；条件为空时返回空 SQL 和 nil 参数。
func LeftJoin(table string, on *SqlBuilder) (string, []any) {
	if on == nil {
		return "", nil
	}
	sql, args := on.Build()
	if sql == "" {
		return "", nil
	}
	return "LEFT JOIN " + table + " ON " + sql, args
}

// InnerJoin 构建 INNER JOIN 子句；条件为空时返回空 SQL 和 nil 参数。
func InnerJoin(table string, on *SqlBuilder) (string, []any) {
	if on == nil {
		return "", nil
	}
	sql, args := on.Build()
	if sql == "" {
		return "", nil
	}
	return "INNER JOIN " + table + " ON " + sql, args
}
