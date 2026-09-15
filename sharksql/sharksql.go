// Package sharksql 提供 SQL 条件构建器和常用数据库操作辅助函数。
//
// 核心功能：
//  1. 条件构建器：一组链式函数，将 Go 表达式转换为参数化的 SQL WHERE 条件片段
//  2. 聚合函数构建器：Sum/Count/Avg/Max/Min 及其带别名的变体
//  3. 排序构建器：Asc/Desc 生成 ORDER BY 子句
//  4. JSON 操作：MySQL JSON 函数封装（JSON_SEARCH、JSON_CONTAINS、JSON_ARRAY_APPEND、JSON_SET）
//  5. 分页查询：泛型分页函数 PageQuery，自动计算 total + offset + limit
//  6. 工具函数：重复键检测、JSON 路径构建、表名.列名拼接
//
// 设计理念：
//   - 全部函数返回参数化条件（条件字符串 + 参数值），天然防止 SQL 注入
//   - 条件函数与 GORM 的 Where()/Having() 等方法无缝配合
//   - 泛型分页函数 PageQuery 支持任意 GORM 模型类型
//
// 文件结构：
//   - condition.go   — 条件构建器 (Eq/Neq/Gt/Gte/Lt/Lte/Like/In/IsNull 等)
//   - aggregate.go   — 聚合函数 (Count/Sum/Avg/Max/Min 及 As 变体)
//   - expression.go  — 表达式辅助 (As/Paren/Coalesce/IfNull/Case/When/Distinct/Column)
//   - json.go        — MySQL JSON 函数 (JsonExtract/JsonContains/JsonSet 等)
//   - join.go        — JOIN 子句构建 (LeftJoin/InnerJoin)
//   - builder.go     — SqlBuilder 流式 API 条件构建器
//   - struct_where.go — Where/ToUpdate (反射结构体自动生成条件)
//   - pagination.go  — PageQuery 泛型分页
//
// 使用示例：
//
//	import "github.com/lornshark/shark/sharksql"
//
//	// 构建查询条件
//	db.Where(
//	    sharksql.Eq("status", 1),
//	    sharksql.Gte("age", 18),
//	    sharksql.Like("name", "张三"),
//	).Find(&users)
//
//	// 分页查询
//	users, total, err := sharksql.PageQuery[User](db, 1, 20)
//
//	// JSON 字段操作
//	db.Update("tags", gorm.Expr(
//	    sharksql.JsonArrayAppend("tags", "new_tag"),
//	))
package sharksql

import (
	"errors"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// IsDuplicateKey 判断错误是否为 MySQL 的重复键错误（错误码 1062）。
//
// 常用于 INSERT 操作的幂等性处理：检测到重复键时返回已有记录或忽略。
//
// 示例：
//
//	err := db.Create(&user).Error
//	if err != nil {
//	    if sharksql.IsDuplicateKey(err) {
//	        // 重复键：返回已存在的记录
//	        db.Where("email = ?", user.Email).First(&user)
//	        return user, nil
//	    }
//	    return nil, err
//	}
func IsDuplicateKey(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		if mysqlErr.Number == 1062 {
			return true
		}
	}
	return false
}

func ToFindSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Find(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

func ToUpdateSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Update("dummy", "dummy")
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

func ToDeleteSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Delete(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

func ToInsertSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Create(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

func ToTakeSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Take(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}
