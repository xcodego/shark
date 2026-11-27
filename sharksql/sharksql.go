// Package sharksql 提供 SQL 条件构建（SqlBuilder、Where）、表达式/聚合/JSON 函数、JOIN 和分页辅助函数。
// 参数化条件（带 ? 的方法）对值做占位符绑定；而列名、别名、路径及表达式函数的参数
// 都是直接拼入 SQL 的，必须是可信的固定值，不能来自用户输入。
package sharksql

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// IsDuplicateKeyError 判断 err 是否为 MySQL 重复键错误（1062）。
// 传入 key 时，错误信息必须同时包含所有 key（如唯一索引名）才返回 true。
func IsDuplicateKeyError(err error, key ...string) bool {
	if err == nil {
		return false
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		if mysqlErr.Number == 1062 {
			if len(key) > 0 {
				s := err.Error()
				for _, k := range key {
					if !strings.Contains(s, k) {
						return false
					}
				}
			}
			return true
		}
	}
	return false
}

// IsNotFoundError 判断 err 是否为 gorm.ErrRecordNotFound。
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// ToFindSql 返回 Find 生成的 SQL（DryRun，不执行）。
func ToFindSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Find(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

// ToUpdateSql 返回 Update 生成的 SQL（DryRun，不执行，SET 部分为占位的 dummy 列）。
func ToUpdateSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Update("dummy", "dummy")
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

// ToDeleteSql 返回 Delete 生成的 SQL（DryRun，不执行）。
func ToDeleteSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Delete(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

// ToInsertSql 返回 Create 生成的 SQL（DryRun，不执行）。
func ToInsertSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Create(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

// ToTakeSql 返回 Take 生成的 SQL（DryRun，不执行）。
func ToTakeSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Take(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}
