package sql_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	sharklint_sql "github.com/xcodego/shark/sharklint/sql"
)

// TestTransactionLint 验证事务检查：Rollback/提交遗漏、嵌套、RPC、等待、非事务连接和错误检查。
func TestTransactionLint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), `module example.com/transactions

go 1.26
require (
	gorm.io/gorm v0.0.0
	google.golang.org/grpc v0.0.0
)
replace gorm.io/gorm => ./gorm
replace google.golang.org/grpc => ./grpc
`)
	writeFile(t, filepath.Join(dir, "gorm/go.mod"), "module gorm.io/gorm\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "gorm/gorm.go"), `package gorm
type DB struct { Error error }
func (*DB) Begin() *DB { return nil }
func (*DB) Commit() *DB { return nil }
func (*DB) Rollback() *DB { return nil }
func (*DB) Transaction(func(*DB) error) error { return nil }
func (*DB) Table(string) *DB { return nil }
func (*DB) Model(any) *DB { return nil }
func (*DB) Create(any) *DB { return nil }
func (*DB) Updates(any) *DB { return nil }
func (*DB) Find(any) *DB { return nil }
func (*DB) AutoMigrate(...any) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "grpc/go.mod"), "module google.golang.org/grpc\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "grpc/grpc.go"), "package grpc\ntype CallOption interface{}\n")
	source := `package transactions
import (
	"context"
	"database/sql"
	"net/http"
	"time"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)
type client interface { Call(context.Context, ...grpc.CallOption) error }
func safe(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil { return err }
	defer tx.Rollback()
	if err = tx.Commit(); err != nil { return err }
	return nil
}

func missing(db *sql.DB, bad bool) error {
	tx, err := db.Begin() // want: 事务开启后应 defer Rollback
	if err != nil { return err }
	if bad { return err } // want: 事务退出时未提交或回滚
	return tx.Commit()
}
func explicit(db *sql.DB, bad bool) error {
	tx, err := db.Begin() // want: 事务开启后应 defer Rollback
	if err != nil { return err }
	if bad { return tx.Rollback() }
	return tx.Commit()
}
func slow(db *sql.DB, c client, ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	_ = c.Call(ctx) // want: 事务内禁止调用外部 RPC
	_, _ = http.Get("https://example.com") // want: 事务内禁止调用外部 RPC
	var headers http.Header
	_ = headers.Get("X-Test")
	time.Sleep(time.Second) // want: 事务内禁止显式等待
	<-time.After(time.Second) // want: 事务内禁止显式等待
	select { case <-time.After(time.Second): } // want: 事务内禁止显式等待
	select { case <-time.After(time.Second): default: }
	timer := time.NewTimer(time.Second)
	<-timer.C // want: 事务内禁止显式等待
	_, _ = tx.Exec("UPDATE t SET a=1") // want: 事务中修改数据后必须检查错误
	_, writeErr := tx.Exec("UPDATE t SET a=1") // want: 事务中修改数据后必须检查错误
	_ = writeErr
	_, err = tx.Exec("UPDATE t SET a=1")
	if err != nil { return err }
	_, _ = tx.Query("SELECT 1") // want: 事务中查询结果被丢弃
	rows, err := tx.Query("SELECT 1")
	if err != nil { return err }
	defer rows.Close()
	if err := tx.Commit(); err != nil { return err }
	time.Sleep(time.Second)
	return nil
}
func gormTx(db *gorm.DB, c client, ctx context.Context) error {
	return db.Transaction(func(tx *gorm.DB) error {
		tx.Create(nil) // want: 事务中修改数据后必须检查错误
		_ = tx.Create(nil).Error // want: 事务中修改数据后必须检查错误
		writeErr := tx.Create(nil).Error // want: 事务中修改数据后必须检查错误
		_ = writeErr
		if err := tx.Create(nil).Error; err != nil { return err }
		tx.Find(nil) // want: 事务中查询结果被丢弃
		var resultData any
		tx.Find(&resultData)
		result := tx.Updates(nil)
		if result.Error != nil { return result.Error }
		_ = c.Call(ctx) // want: 事务内禁止调用外部 RPC
		return tx.Transaction(func(inner *gorm.DB) error { return nil }) // want: 事务内禁止开启嵌套事务
	})
}
func gormBegin(db *gorm.DB) error {
	tx := db.Begin()
	if tx.Error != nil { return tx.Error }
	defer tx.Rollback()
	err := tx.Commit().Error
	time.Sleep(time.Second)
	return err
}
func connectionChecks(db *gorm.DB, other *gorm.DB, branch bool) error {
	tx := db.Begin()
	if tx.Error != nil { return tx.Error }
	defer tx.Rollback()
	tx.Model(nil).Find(new(int))
	query := tx.Model(nil)
	query.Find(new(int))
	if branch { query = other.Model(nil) }
	query.Find(new(int)) // want: 事务内禁止使用非事务数据库连接
	db.Model(nil).Find(new(int)) // want: 事务内禁止使用非事务数据库连接
	other.Find(new(int)) // want: 事务内禁止使用非事务数据库连接
	_ = db.AutoMigrate(new(int)) // want: 事务内禁止使用非事务数据库连接
	// force ignore sharklint: sql
	db.Find(new(int))
	if err := tx.Commit().Error; err != nil { return err }
	db.Find(new(int))
	return nil
}
func callbackConnections(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		tx.Find(new(int))
		db.Find(new(int)) // want: 事务内禁止使用非事务数据库连接
		return nil
	})
}
func reassignedConnection(db *gorm.DB) error {
	tx := db.Begin()
	if tx.Error != nil { return tx.Error }
	defer tx.Rollback()
	tx = db
	tx.Find(new(int)) // want: 事务内禁止使用非事务数据库连接
	return nil
}
func rollbackBeforeUse(db *gorm.DB) error {
	tx := db.Begin()
	if tx.Error != nil { return tx.Error }
	defer tx.Rollback()
	if err := tx.Rollback().Error; err != nil { return err }
	db.Find(new(int))
	return nil
}
func sqlConnections(db *sql.DB, conn *sql.Conn) error {
	stmt, err := db.Prepare("SELECT 1")
	if err != nil { return err }
	defer stmt.Close()
	tx, err := db.Begin()
	if err != nil { _ = db.Ping(); return err }
	defer tx.Rollback()
	_ = db.Ping() // want: 事务内禁止使用非事务数据库连接
	_ = (*sql.DB).Ping(db) // want: 事务内禁止使用非事务数据库连接
	_ = conn.PingContext(context.Background()) // want: 事务内禁止使用非事务数据库连接
	otherConn, err := db.Conn(context.Background()) // want: 事务内禁止使用非事务数据库连接
	if err != nil { return err }
	defer otherConn.Close()
	_, _ = stmt.Query() // want: 事务内禁止使用非事务数据库连接
	rows, err := tx.Stmt(stmt).Query()
	if err != nil { return err }
	defer rows.Close()
	stmt = tx.Stmt(stmt)
	rows, err = stmt.Query()
	if err != nil { return err }
	defer rows.Close()
	prepared, err := tx.Prepare("SELECT 1")
	if err != nil { return err }
	defer prepared.Close()
	rows, err = prepared.Query()
	if err != nil { return err }
	defer rows.Close()
	if err := tx.Commit(); err != nil { return err }
	_ = db.Ping()
	return nil
}
func conditionalDefer(db *sql.DB, ok bool) error {
	tx, err := db.Begin() // want: 事务开启后应 defer Rollback
	if err != nil { return err }
	if ok { defer tx.Rollback() }
	return tx.Commit()
}
func reassignedError(db *sql.DB) error {
	tx, err := db.Begin() // want: 事务开启后应 defer Rollback
	err = nil
	if err != nil { return err } // want: 事务退出时未提交或回滚
	return tx.Commit()
}
func valueSpec(db *sql.DB) error {
	var tx, err = db.Begin() // want: 事务开启后应 defer Rollback
	if err != nil { return err }
	return tx.Commit()
}
func deferredCommit(db *sql.DB) error {
	tx, err := db.Begin() // want: 事务开启后应 defer Rollback
	if err != nil { return err }
	defer tx.Commit()
	return nil
}
func discarded(db *gorm.DB) {
	db.Begin() // want: 事务开启后应 defer Rollback; want: 事务退出时未提交或回滚
}
func ignored(db *sql.DB) error {
	// force ignore sharklint: sql
	tx, err := db.Begin()
	if err != nil { return err }
	return tx.Commit()
}
`
	writeFile(t, filepath.Join(dir, "main.go"), source)
	err := sharklint_sql.Lint(dir)
	if err == nil || strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应当只报告违规，实际得到 %v", err)
	}
	for i, line := range strings.Split(source, "\n") {
		location := fmt.Sprintf("main.go:%d:", i+1)
		for _, reason := range []string{
			"事务开启后应 defer Rollback", "事务退出时未提交或回滚", "事务内禁止调用外部 RPC",
			"事务内禁止显式等待", "事务内禁止开启嵌套事务", "事务中修改数据后必须检查错误", "事务中查询结果被丢弃",
			"事务内禁止使用非事务数据库连接",
		} {
			got := false
			for _, diagnostic := range strings.Split(err.Error(), "\n") {
				if strings.Contains(diagnostic, location) && strings.Contains(diagnostic, reason) {
					got = true
				}
			}

			want := strings.Contains(line, "want: "+reason)
			if got != want {
				t.Errorf("%s %s: got %v, want %v\n%s", location, reason, got, want, err)
			}
		}
	}
}

// TestTransactionRealDependencies 用真实 gorm 与 grpc 依赖的 testdata 验证事务检查。
func TestTransactionRealDependencies(t *testing.T) {
	err := sharklint_sql.Lint(filepath.Join("testdata", "real"))
	if err == nil || strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应当只报告违规，实际得到 %v", err)
	}
	for _, reason := range []string{
		"事务中修改数据后必须检查错误", "事务内禁止调用外部 RPC",
		"事务内禁止显式等待", "事务内禁止开启嵌套事务", "事务内禁止使用非事务数据库连接",
	} {
		found := false
		for _, line := range strings.Split(err.Error(), "\n") {
			if strings.Contains(line, "transactions.go:") && strings.Contains(line, reason) {
				found = true
			}
		}
		if !found {
			t.Errorf("应报告 %s: %v", reason, err)
		}
	}
}
