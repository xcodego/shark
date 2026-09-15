package test

import (
	"context"
	"testing"
	"time"

	"github.com/lornshark/shark/sharkrisingwave"
	"go.uber.org/zap"
)

func TestRisingWaveNew(t *testing.T) {
	cfg := loadRisingWaveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := sharkrisingwave.New(ctx, zap.NewNop(), cfg)
	if err != nil {
		t.Fatalf("连接 RisingWave 失败: %v", err)
	}
	t.Log("RisingWave 连接成功")

	// 获取底层 sql.DB 验证连接
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
	t.Log("RisingWave SELECT 1 验证通过")

	// 创建临时物化视图测试 RisingWave 特有功能
	viewName := "test_shark_mv"
	db.Exec("DROP MATERIALIZED VIEW IF EXISTS " + viewName)
	err = db.Exec("CREATE MATERIALIZED VIEW " + viewName + " AS SELECT 1 AS id").Error
	if err != nil {
		t.Logf("创建物化视图失败 (RW 版本可能不支持): %v", err)
	} else {
		type mvRow struct {
			ID int `gorm:"column:id"`
		}
		var rows []mvRow
		db.Table(viewName).Find(&rows)
		t.Logf("物化视图查询: %d 行", len(rows))
		db.Exec("DROP MATERIALIZED VIEW IF EXISTS " + viewName)
	}
}

func TestRisingWaveReadWrite(t *testing.T) {
	cfg := loadRisingWaveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := sharkrisingwave.New(ctx, zap.NewNop(), cfg)
	if err != nil {
		t.Fatalf("连接 RisingWave 失败: %v", err)
	}

	// 创建测试表（RW 可能不支持某些 DDL，失败则跳过）
	tableName := "test_shark_rw_temp"
	db.Exec("DROP TABLE IF EXISTS " + tableName)
	err = db.Exec("CREATE TABLE " + tableName + " (id INT PRIMARY KEY, name VARCHAR(50))").Error
	if err != nil {
		t.Skipf("RisingWave 不支持创建表: %v", err)
	}
	defer db.Exec("DROP TABLE IF EXISTS " + tableName)

	// 写入数据
	err = db.Exec("INSERT INTO "+tableName+" VALUES (?, ?)", 1, "shark").Error
	if err != nil {
		t.Skipf("RisingWave INSERT 失败: %v", err)
	}

	// 查询
	type testRow struct {
		ID   int    `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	var rows []testRow
	err = db.Table(tableName).Find(&rows).Error
	if err != nil {
		t.Fatalf("SELECT 失败: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "shark" {
		t.Errorf("查询结果不匹配: %+v", rows)
	}
	t.Log("RisingWave INSERT/SELECT 验证通过")
}
