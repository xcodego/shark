package sharksql

// LeftJoin 构建 LEFT JOIN 子句字符串和参数列表。
//
// 参数：
//   - table: 要 JOIN 的表名及别名，如 "orders o" 或 "accounts a"
//   - on:    SqlBuilder 实例，用于构建 ON 条件（支持字段对字段和字段对值的混合）
//
// 返回值：
//   - string: 完整的 LEFT JOIN 子句（如 "LEFT JOIN orders o ON u.id = o.user_id AND o.deleted = ?"）
//   - []any:  ON 条件中的参数值切片
//
// 使用示例：
//
//	// 基本 JOIN，纯字段关联
//	onB := sharksql.NewSql().EqCol("u.id", "o.user_id")
//	joinSQL, args := sharksql.LeftJoin("orders o", onB)
//	// joinSQL: "LEFT JOIN orders o ON u.id = o.user_id"
//	// args:    nil
//	db.Joins(joinSQL, args...).Find(&results)
//
//	// 带额外筛选条件的 JOIN
//	onB := sharksql.NewSql().
//	    EqCol("u.id", "o.user_id").
//	    Eq("o.deleted", 0)
//	joinSQL, args := sharksql.LeftJoin("orders o", onB)
//	// joinSQL: "LEFT JOIN orders o ON u.id = o.user_id AND o.deleted = ?"
//	// args:    [0]
//	db.Joins(joinSQL, args...).Find(&results)
//
//	// 多表 JOIN
//	db.Joins(sharksql.LeftJoin("orders o", sharksql.NewSql().EqCol("u.id", "o.user_id"))).
//	    Joins(sharksql.LeftJoin("accounts a", sharksql.NewSql().EqCol("u.account_id", "a.id"))).
//	    Find(&results)
//
//	// on 为 nil 或 Build 结果为空时，返回空字符串
//	joinSQL, args := sharksql.LeftJoin("orders o", nil)
//	// joinSQL: ""
//	// args:    nil
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

// InnerJoin 构建 INNER JOIN 子句字符串和参数列表。
//
// 参数和返回值说明同 LeftJoin，区别在于使用 INNER JOIN 而非 LEFT JOIN。
//
// 使用示例：
//
//	// 基本 INNER JOIN，纯字段关联
//	onB := sharksql.NewSql().EqCol("u.id", "o.user_id")
//	joinSQL, args := sharksql.InnerJoin("orders o", onB)
//	// joinSQL: "INNER JOIN orders o ON u.id = o.user_id"
//	// args:    nil
//	db.Joins(joinSQL, args...).Find(&results)
//
//	// 带额外筛选条件的 INNER JOIN
//	onB := sharksql.NewSql().
//	    EqCol("u.id", "o.user_id").
//	    Eq("o.deleted", 0)
//	joinSQL, args := sharksql.InnerJoin("orders o", onB)
//	// joinSQL: "INNER JOIN orders o ON u.id = o.user_id AND o.deleted = ?"
//	// args:    [0]
//
//	// on 为 nil 或 Build 结果为空时，返回空字符串
//	joinSQL, args := sharksql.InnerJoin("orders o", nil)
//	// joinSQL: ""
//	// args:    nil
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
