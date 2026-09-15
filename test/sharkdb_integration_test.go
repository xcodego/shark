package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lornshark/shark/sharkdb"
	"go.uber.org/zap"
)

func TestDBNewDb(t *testing.T) {
	cfg := loadDBConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := sharkdb.NewDb(ctx, zap.NewNop(), cfg)
	if err != nil {
		t.Fatalf("连接数据库失败: %v", err)
	}
	t.Log("数据库连接成功")

	// 获取底层 sql.DB 验证连接池配置
	gdb, _ := db.DB()
	if err := gdb.Ping(); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
	t.Log("Ping 成功")

	// 验证 GORM 可以正常查询
	var result int
	err = db.Raw("SELECT 1").Scan(&result).Error
	if err != nil {
		t.Fatalf("SELECT 1 失败: %v", err)
	}
	if result != 1 {
		t.Errorf("SELECT 1 = %d, want 1", result)
	}
	t.Log("SELECT 1 验证通过")
}

func TestDBSharkTable(t *testing.T) {
	cfg := loadDBConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := sharkdb.NewDb(ctx, zap.NewNop(), cfg)
	if err != nil {
		t.Fatalf("连接数据库失败: %v", err)
	}

	// 创建临时测试表
	tableName := "test_sharktable_temp"
	db.Exec("DROP TABLE IF EXISTS " + tableName)
	err = db.Exec("CREATE TABLE " + tableName + " (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50), status INT)").Error
	if err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer db.Exec("DROP TABLE IF EXISTS " + tableName)

	// 插入测试数据
	db.Exec("INSERT INTO "+tableName+" (name, status) VALUES (?, ?)", "Alice", 1)
	db.Exec("INSERT INTO "+tableName+" (name, status) VALUES (?, ?)", "Bob", 2)
	db.Exec("INSERT INTO "+tableName+" (name, status) VALUES (?, ?)", "Charlie", 1)

	type testRow struct {
		ID     int    `gorm:"column:id"`
		Name   string `gorm:"column:name"`
		Status int    `gorm:"column:status"`
	}

	// 测试链式查询
	table := sharkdb.NewTable(db.Table(tableName))
	var results []testRow
	err = table.Eq("status", 1).Gorm().Find(&results).Error
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("status=1 应有2条, got %d", len(results))
	}

	// 测试 Like
	var likeResults []testRow
	err = table.Like("name", "Ali").Gorm().Find(&likeResults).Error
	if err != nil {
		t.Fatalf("Like 查询失败: %v", err)
	}
	if len(likeResults) != 1 || likeResults[0].Name != "Alice" {
		t.Errorf("Like 查询结果不正确: %+v", likeResults)
	}

	t.Logf("SharkTable 链式查询通过: status=1 → %d 条, Like 'Ali' → %s", len(results), likeResults[0].Name)
}

func TestDBTableScan(t *testing.T) {
	cfg := loadDBConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := sharkdb.NewDb(ctx, zap.NewNop(), cfg)
	if err != nil {
		t.Fatalf("连接数据库失败: %v", err)
	}

	// 创建临时表
	tableName := "test_tablescan_temp"
	db.Exec("DROP TABLE IF EXISTS " + tableName)
	err = db.Exec("CREATE TABLE " + tableName + " (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50))").Error
	if err != nil {
		t.Fatalf("创建表失败: %v", err)
	}
	defer db.Exec("DROP TABLE IF EXISTS " + tableName)

	// 插入100条测试数据
	for i := 1; i <= 100; i++ {
		db.Exec("INSERT INTO "+tableName+" (name) VALUES (?)", "user-"+fmt.Sprint(i))
	}

	type testRow struct {
		ID   int    `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}

	// TableScan 分页扫描
	scan := sharkdb.NewTableScan[testRow]().PageSize(30).Asc("id")
	var last *testRow
	total := 0
	pages := 0
	for {
		results, err := scan.Next(db.Table(tableName), last)
		if err != nil {
			t.Fatalf("TableScan Next 失败: %v", err)
		}
		if len(results) == 0 {
			break
		}
		pages++
		total += len(results)
		last = &results[len(results)-1]
	}
	if total != 100 {
		t.Errorf("TableScan 应扫描 100 条, got %d", total)
	}
	if pages != 4 {
		t.Errorf("PageSize=30 应分 4 页, got %d", pages)
	}
	t.Logf("TableScan: 100 条数据, PageSize=30, %d 页扫描完成", pages)
}
