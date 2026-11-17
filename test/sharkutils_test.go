package test

import (
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/panjf2000/ants/v2"
	"github.com/xcodego/shark/sharkutils"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRandNum(t *testing.T) {
	for i := 0; i < 100; i++ {
		n := sharkutils.RandNum(1, 100)
		if n < 1 || n >= 100 {
			t.Errorf("RandNum(1,100) = %d, 应在 [1,100) 范围内", n)
		}
	}
}

func TestRandNumMinEqMax(t *testing.T) {
	n := sharkutils.RandNum(5, 5)
	if n != 0 {
		t.Errorf("RandNum(5,5) = %d, want 0", n)
	}
}

func TestRandNumInvalidBounds(t *testing.T) {
	tests := []struct {
		min int
		max int
	}{
		{min: 0, max: 10},
		{min: -1, max: 10},
		{min: 1, max: 1},
		{min: 2, max: 1},
	}
	for _, tt := range tests {
		if n := sharkutils.RandNum(tt.min, tt.max); n != 0 {
			t.Errorf("RandNum(%d,%d) = %d, want 0", tt.min, tt.max, n)
		}
	}
}

func TestRandNumIntMaxBoundary(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if n := sharkutils.RandNum(maxInt-1, maxInt); n != maxInt-1 {
		t.Errorf("RandNum(maxInt-1,maxInt) = %d, want %d", n, maxInt-1)
	}
}

func TestMd5(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "5d41402abc4b2a76b9719d911017c592"},
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
		{"shark", "da6776e7ec7eaa7a6f3df5c6b149127e"},
	}
	for _, tt := range tests {
		hash := sharkutils.Md5([]byte(tt.input))
		if hash != tt.expected {
			t.Errorf("Md5(%q) = %s, want %s", tt.input, hash, tt.expected)
		}
	}
}

func TestGetClientIp(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xri        string
		expected   string
	}{
		{"直接连接", "192.168.1.1:12345", "", "", "192.168.1.1"},
		{"Nginx 代理 X-Forwarded-For", "10.0.0.1:80", "1.2.3.4", "", "1.2.3.4"},
		{"多层代理", "10.0.0.1:80", "1.2.3.4, 5.6.7.8", "", "1.2.3.4"},
		{"X-Real-Ip", "10.0.0.1:80", "", "9.8.7.6", "9.8.7.6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-Ip", tt.xri)
			}
			ip := sharkutils.GetClientIp(req)
			if ip != tt.expected {
				t.Errorf("GetClientIp = %s, want %s", ip, tt.expected)
			}
		})
	}
}

func TestBcryptHashAndCheck(t *testing.T) {
	password := "mySecurePassword123"
	hashed := sharkutils.BcryptHash(password)
	if hashed == "" {
		t.Fatal("加密后不应为空")
	}
	if len(hashed) < 20 {
		t.Errorf("哈希长度过短: %d", len(hashed))
	}

	// 验证匹配
	if !sharkutils.BcryptCheck(hashed, password) {
		t.Error("正确密码应该验证通过")
	}

	// 验证错误密码
	if sharkutils.BcryptCheck(hashed, "wrongPassword") {
		t.Error("错误密码不应该验证通过")
	}
}

func TestBcryptHashRandomSalt(t *testing.T) {
	pwd := "samePassword"
	h1 := sharkutils.BcryptHash(pwd)
	h2 := sharkutils.BcryptHash(pwd)
	if h1 == h2 {
		t.Error("相同密码的两次加密结果应该不同（随机盐值）")
	}
	// 但都能验证通过
	if !sharkutils.BcryptCheck(h1, pwd) || !sharkutils.BcryptCheck(h2, pwd) {
		t.Error("随机盐值的哈希都应能验证通过")
	}
}

func TestMustUpsetDb(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "upsert.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE records (tenant INTEGER, id INTEGER, name TEXT, PRIMARY KEY (tenant, id))").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO records (tenant, id, name) VALUES (1, 1, 'old'), (1, 2, 'keep')").Error; err != nil {
		t.Fatal(err)
	}
	pool, err := ants.NewPool(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Release)

	args := []map[string]any{
		{"tenant": 1, "id": 1, "name": "first"},
		{"tenant": 1, "id": 1, "name": "latest"},
		{"tenant": 1, "name": "missing id"},
	}
	if err := sharkutils.MustUpsetDb(pool, zap.NewNop(), db, "records", []string{"tenant", "id"}, args...); err != nil {
		t.Fatal(err)
	}
	var records []struct {
		ID   int
		Name string
	}
	if err := db.Table("records").Order("id").Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].ID != 1 || records[0].Name != "latest" || records[1].ID != 2 || records[1].Name != "keep" {
		t.Fatalf("unexpected records after upsert: %+v", records)
	}
}

func TestMustDeleteFromDb(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "delete.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	pool, err := ants.NewPool(5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Release)

	if err := db.Exec("CREATE TABLE records (tenant INTEGER, id INTEGER, name TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	type record struct {
		Tenant int
		ID     *int
		Name   string
	}
	var records []record
	var args []map[string]any
	for i := 1; i <= 1001; i++ {
		id := i
		records = append(records, record{Tenant: 1, ID: &id, Name: "delete"})
		args = append(args, map[string]any{"tenant": 1, "id": i})
	}
	sentinelID := 2000
	records = append(records, record{Tenant: 1, ID: &sentinelID, Name: "keep"})
	records = append(records, record{Tenant: 2, ID: &sentinelID, Name: "delete"})
	records = append(records, record{Tenant: 2, ID: nil, Name: "delete"})
	records = append(records, record{Tenant: 3, ID: nil, Name: "keep"})
	if err := db.Table("records").CreateInBatches(records, 200).Error; err != nil {
		t.Fatal(err)
	}
	args = append(args,
		map[string]any{"tenant": 2, "id": sentinelID},
		map[string]any{"tenant": 2, "id": nil},
		map[string]any{"tenant": 1, "id": 1, "name": "ignored"},
		map[string]any{"tenant": 3},
	)
	if err := sharkutils.MustDeleteFromDb(pool, zap.NewNop(), db, "records", []string{"tenant", "id"}, args...); err != nil {
		t.Fatal(err)
	}
	var remaining []record
	if err := db.Table("records").Order("tenant").Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 || remaining[0].Tenant != 1 || remaining[0].ID == nil || *remaining[0].ID != sentinelID || remaining[1].Tenant != 3 || remaining[1].ID != nil {
		t.Fatalf("unexpected remaining records: %+v", remaining)
	}
}

func TestMustDeleteFromDbRetries(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "retry.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE records (id INTEGER)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO records (id) VALUES (1), (2)").Error; err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	if err := db.Callback().Delete().Before("gorm:delete").Register("test:fail_once", func(tx *gorm.DB) {
		if attempts.Add(1) == 1 {
			tx.AddError(errors.New("temporary failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	pool, err := ants.NewPool(1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release()
	core, logs := observer.New(zapcore.ErrorLevel)
	if err := sharkutils.MustDeleteFromDb(pool, zap.New(core), db, "records", []string{"id"}, map[string]any{"id": 1}); err != nil {
		t.Fatal(err)
	}
	var remaining []int
	if err := db.Table("records").Pluck("id", &remaining).Error; err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 || logs.Len() != 1 || len(remaining) != 1 || remaining[0] != 2 {
		t.Fatalf("attempts=%d, logs=%d, remaining=%v", attempts.Load(), logs.Len(), remaining)
	}
}

func TestMustDeleteFromDbInvalidArguments(t *testing.T) {
	pool, err := ants.NewPool(1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release()
	logger := zap.NewNop()
	db := &gorm.DB{}
	valid := map[string]any{"id": 1}
	tests := []struct {
		name   string
		pool   *ants.Pool
		logger *zap.Logger
		db     *gorm.DB
		table  string
		keys   []string
		args   []map[string]any
	}{
		{"nil pool", nil, logger, db, "records", []string{"id"}, []map[string]any{valid}},
		{"nil logger", pool, nil, db, "records", []string{"id"}, []map[string]any{valid}},
		{"nil db", pool, logger, nil, "records", []string{"id"}, []map[string]any{valid}},
		{"no args", pool, logger, db, "records", []string{"id"}, nil},
		{"empty table", pool, logger, db, "", []string{"id"}, []map[string]any{valid}},
		{"no keys", pool, logger, db, "records", nil, []map[string]any{valid}},
		{"empty key", pool, logger, db, "records", []string{""}, []map[string]any{valid}},
		{"missing keys", pool, logger, db, "records", []string{"id", "tenant"}, []map[string]any{valid}},
		{"unencodable key", pool, logger, db, "records", []string{"id"}, []map[string]any{{"id": make(chan int)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := sharkutils.MustDeleteFromDb(tt.pool, tt.logger, tt.db, tt.table, tt.keys, tt.args...); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
