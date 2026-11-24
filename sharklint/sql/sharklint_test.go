package sql_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sharklint_sql "github.com/xcodego/shark/sharklint/sql"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLintSQL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), `module example.com/project

go 1.26
require (
	gorm.io/gorm v0.0.0
	github.com/xcodego/shark/sharksql v0.0.0
)
replace gorm.io/gorm => ./gorm
replace github.com/xcodego/shark/sharksql => ./sharksql
`)
	writeFile(t, filepath.Join(dir, "app/gconst/const.go"), `package gconst
const Users = "users"
const ID = "id"
const Name = "name"
var Dynamic = "id"
`)
	writeFile(t, filepath.Join(dir, "gorm/go.mod"), "module gorm.io/gorm\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "gorm/gorm.go"), `package gorm
type DB struct{}
func (*DB) Raw(string, ...any) *DB { return nil }
func (*DB) Exec(string, ...any) *DB { return nil }
func (*DB) Where(any, ...any) *DB { return nil }
func (*DB) Table(string, ...any) *DB { return nil }
func (*DB) Select(any, ...any) *DB { return nil }
func (*DB) Update(string, any) *DB { return nil }
func (*DB) Updates(any) *DB { return nil }
func (*DB) Create(any) *DB { return nil }
func (*DB) CreateInBatches(any, int) *DB { return nil }
func (*DB) Find(any, ...any) *DB { return nil }
func Expr(string, ...any) any { return nil }
`)
	writeFile(t, filepath.Join(dir, "sharksql/go.mod"), "module github.com/xcodego/shark/sharksql\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "sharksql/sql.go"), `package sharksql
type SqlBuilder struct{}
func NewSql() *SqlBuilder { return nil }
func (*SqlBuilder) Eq(string, any) *SqlBuilder { return nil }
func (*SqlBuilder) EqCol(string, string) *SqlBuilder { return nil }
func (*SqlBuilder) Build() (string, []any) { return "", nil }
func Eq(string, any) (string, any) { return "", nil }
func Count(string) string { return "" }
func Add(string, any) (string, any) { return "", nil }
func Coalesce(...any) string { return "" }
func When(...any) string { return "" }
func JsonPath(...string) string { return "" }
`)
	source := `package example
import (
	"context"
	"database/sql"
	"fmt"
	c "example.com/project/app/gconst"
	s "github.com/xcodego/shark/sharksql"
	g "gorm.io/gorm"
)
type other struct{}
func (other) Where(string) {}
func run(db *g.DB, raw *sql.DB, ctx context.Context, id int) {
	db.Raw("SELECT * FROM users") // want
	db.Exec("DELETE FROM users WHERE id = ?", id) // want
	raw.Query("SELECT * FROM users") // want
	raw.QueryContext(ctx, "SELECT * FROM users") // want
	(*sql.DB).Exec(raw, "DELETE FROM users") // want
	raw.Prepare("SELECT * FROM users") // want
	db.Where("id = ?", id) // want
	db.Where(c.ID + " = ?", id) // want
	db.Where(fmt.Sprintf("id = %d", id)) // want
	db.Where(map[string]any{"id": id}) // want
	db.Where(&map[string]any{"id": id}) // want
	db.Table("users") // want
	db.Table(c.Users)
	db.Select("id") // want
	db.Select(c.ID, c.Name)
	db.Select([]string{c.ID, "name"}) // want
	db.Select(s.Count(c.ID))
	count := s.Count(c.ID)
	db.Select(count)
	db.Select(s.Coalesce("id", c.Name)) // want
	_ = s.When("id = 1", 1) // want
	db.Where(s.JsonPath("id = ?")) // want
	db.Update("name", "Alice") // want
	db.Update(c.Name, "Alice")
	db.Updates(map[string]any{"name": "Alice"}) // want
	db.Updates(map[string]any{c.Name: "Alice"})
	db.Create(&map[string]any{"name": "Alice"}) // want
	db.Create(&map[string]any{c.Name: "Alice"})
	orderData := map[string]any{c.ID: id}
	orderData[c.Name] = "Alice"
	func(tx *g.DB) {
		orderData[c.ID] = id
		tx.Table(c.Users).Create(orderData)
	}(db)
	db.CreateInBatches(orderData, 10)
	db.Updates(orderData)
	db.Where(orderData)
	bad := map[string]any{"id": id}
	db.Create(bad) // want
	badWrite := map[string]any{c.ID: id}
	badWrite["name"] = "Alice"
	db.Create(badWrite) // want
	dynamicKey := "name"
	dynamic := map[string]any{c.ID: id}
	dynamic[dynamicKey] = "Alice"
	db.Create(dynamic) // want
	reassigned := map[string]any{c.ID: id}
	reassigned = map[string]any{"name": "Alice"}
	db.Create(reassigned) // want
	original := map[string]any{c.ID: id}
	alias := original
	alias["name"] = "Alice"
	db.Create(original) // want
	db.Create(alias) // want
	db.Where(map[string]any{c.ID: id})
	db.Where(s.Eq(c.ID, id))
	db.Where(s.Eq("id", id)) // want
	db.Where(s.Eq(c.Dynamic, id)) // want
	db.Where(fmt.Sprint("id = ?")) // want
	db.Find(nil, "id = ?", id) // want
	db.Find(nil, id)
	g.Expr("id + ?", 1) // want
	g.Expr(s.Add(c.ID, 1))
	s.NewSql().EqCol(c.ID, "name") // want
	s.NewSql().Eq(c.ID, id)
	query, args := s.NewSql().Eq(c.ID, id).Build()
	db.Where(query, args...) // want
	query = "id = ?"
	db.Where(query, id) // want
	safe, safeArgs := s.NewSql().Eq(c.ID, id).Build()
	db.Where(safe, safeArgs...)
	// force ignore sharklint: sql
	db.Raw("SELECT 1")
	other{}.Where("not SQL")
}
`
	writeFile(t, filepath.Join(dir, "main.go"), source)
	err := sharklint_sql.Lint(dir)
	if err == nil || strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应当只报告违规，实际得到 %v", err)
	}
	var violation sharklint_sql.Violation
	if !errors.As(err, &violation) {
		t.Fatalf("错误中应当包含 Violation: %v", err)
	}
	count := 0
	for i, line := range strings.Split(source, "\n") {
		location := fmt.Sprintf("main.go:%d:", i+1)
		reported := strings.Contains(err.Error(), location)
		if strings.Contains(line, "// want") {
			count++
			if !reported {
				t.Errorf("应报告 %s: %v", location, err)
			}
		} else if reported {
			t.Errorf("不应报告 %s: %v", location, err)
		}
	}
	if got := strings.Count(err.Error(), "main.go:"); got != count {
		t.Errorf("违规数为 %d，期望为 %d: %v", got, count, err)
	}
}

func TestLintErrors(t *testing.T) {
	dir := t.TempDir()
	if err := sharklint_sql.Lint(dir); err != nil {
		t.Fatalf("空目录不应报错: %v", err)
	}

	if err := sharklint_sql.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应报告目录不存在: %v", err)
	}
	file := filepath.Join(dir, "bad.go")
	writeFile(t, file, "package broken\nfunc bad( {\n")
	if err := sharklint_sql.Lint(file); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("应报告不是目录: %v", err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/broken\n\ngo 1.26\n")
	if err := sharklint_sql.Lint(dir); err == nil || !strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应报告解析错误: %v", err)
	}
}

func TestRealDependencies(t *testing.T) {
	err := sharklint_sql.Lint(filepath.Join("testdata", "real"))
	if err == nil || strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应当只报告违规，实际得到 %v", err)
	}
	for _, line := range []int{15, 16, 17, 18} {
		if !strings.Contains(err.Error(), fmt.Sprintf("main.go:%d:", line)) {
			t.Errorf("应报告第 %d 行: %v", line, err)
		}
	}
	if count := strings.Count(err.Error(), "main.go:"); count != 4 {
		t.Errorf("应报告 4 处违规，实际得到 %v", err)
	}
}
