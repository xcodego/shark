// Package sharksql 提供 SQL 条件构建器和常用数据库操作辅助函数。
//
// It provides parameterized conditions, SQL expressions, joins, JSON helpers,
// and GORM pagination utilities. Use the condition helpers with GORM's Where or Having
// methods to keep values separate from SQL text.
package sharksql

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// IsDuplicateKeyError 判断 err 是否为 MySQL 重复键错误（错误码 1062）。
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

// ToFindSql 返回 GORM 为 Find 生成的 SQL，不执行查询。
func ToFindSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Find(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

// ToUpdateSql 返回 GORM 为 Update 生成的 SQL，不执行更新。
func ToUpdateSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Update("dummy", "dummy")
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

// ToDeleteSql 返回 GORM 为 Delete 生成的 SQL，不执行删除。
func ToDeleteSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Delete(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

// ToInsertSql 返回 GORM 为 Create 生成的 SQL，不执行插入。
func ToInsertSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Create(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}

// ToTakeSql 返回 GORM 为 Take 生成的 SQL，不执行查询。
func ToTakeSql(db *gorm.DB) string {
	tx := db.Session(&gorm.Session{DryRun: true}).Take(nil)
	sql := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
	return sql
}
