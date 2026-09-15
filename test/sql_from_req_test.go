package test

import (
	"encoding/json"
	"testing"

	"github.com/lornshark/shark/sharksql"
	"github.com/shopspring/decimal"
)

// ========== Where 测试 (sql tag) ==========

func TestWhereAllFieldsSet(t *testing.T) {
	type Req struct {
		UserId *int64  `sql:"user_id = ?"`
		Name   *string `sql:"name LIKE ?"`
	}
	userId := int64(100)
	name := "张三"
	req := Req{UserId: &userId, Name: &name}
	sql, args := sharksql.Where(req)
	expected := "user_id = ? AND name LIKE ?"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 2 {
		t.Fatalf("args count = %d, want 2", len(args))
	}
	if args[0] != int64(100) {
		t.Errorf("args[0] = %v, want 100", args[0])
	}
	if args[1] != "%张三%" {
		t.Errorf("args[1] = %v, want %%张三%%", args[1])
	}
}

func TestWhereNilPointerIgnored(t *testing.T) {
	type Req struct {
		UserId *int64  `sql:"user_id = ?"`
		Name   *string `sql:"name LIKE ?"`
	}
	name := "张三"
	req := Req{UserId: nil, Name: &name}
	sql, args := sharksql.Where(req)
	expected := "name LIKE ?"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 1 {
		t.Fatalf("args count = %d, want 1", len(args))
	}
}

func TestWhereEmptySliceIgnored(t *testing.T) {
	type Req struct {
		Name   *string `sql:"name LIKE ?"`
		Status []int   `sql:"status in (?)"`
	}
	name := "张三"
	req := Req{Name: &name, Status: []int{}}
	sql, args := sharksql.Where(req)
	expected := "name LIKE ?"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 1 {
		t.Fatalf("args count = %d, want 1", len(args))
	}
}

func TestWhereAllNilOrEmpty(t *testing.T) {
	type Req struct {
		UserId *int64  `sql:"user_id = ?"`
		Name   *string `sql:"name LIKE ?"`
	}
	req := Req{}
	sql, args := sharksql.Where(req)
	if sql != "" || len(args) != 0 {
		t.Errorf("sql = %s, want ''", sql)
	}
}

func TestWhereOnlyIn(t *testing.T) {
	type Req struct {
		Status []int `sql:"status in (?)"`
	}
	req := Req{Status: []int{1, 5, 10}}
	sql, args := sharksql.Where(req)
	expected := "status in (?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 1 {
		t.Fatalf("args count = %d, want 1", len(args))
	}
}

func TestWhereNotIn(t *testing.T) {
	type Req struct {
		ExcludeId []int64 `sql:"id NOT IN (?)"`
	}
	req := Req{ExcludeId: []int64{100, 200}}
	sql, args := sharksql.Where(req)
	expected := "id NOT IN (?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 1 {
		t.Fatalf("args count = %d, want 1", len(args))
	}
}

func TestWhereNonPointerIgnored(t *testing.T) {
	type NonPtrReq struct {
		Age int `sql:"age = ?"`
	}
	req := NonPtrReq{Age: 18}
	sql, _ := sharksql.Where(req)
	if sql != "" {
		t.Errorf("sql = %s, want ''", sql)
	}
}

func TestWhereNoSqlTagIgnored(t *testing.T) {
	type NoSqlTagReq struct {
		Status *int    `json:"status"`
		Name   *string `sql:"name LIKE ?"`
	}
	name := "hello"
	req := NoSqlTagReq{Status: nil, Name: &name}
	sql, args := sharksql.Where(req)
	expected := "name LIKE ?"
	if sql != expected || args[0] != "%hello%" {
		t.Errorf("sql = %s, args = %v", sql, args)
	}
}

func TestWhereGt(t *testing.T) {
	type Req struct {
		Age *int `sql:"age > ?"`
	}
	age := 18
	req := Req{Age: &age}
	sql, args := sharksql.Where(req)
	expected := "age > ?"
	if sql != expected || args[0] != 18 {
		t.Errorf("sql = %s, args = %v", sql, args)
	}
}

func TestWhereGte(t *testing.T) {
	type Req struct {
		Score *int `sql:"score >= ?"`
	}
	score := 60
	req := Req{Score: &score}
	sql, args := sharksql.Where(req)
	expected := "score >= ?"
	if sql != expected || args[0] != 60 {
		t.Errorf("sql = %s, args = %v", sql, args)
	}
}

func TestWhereLt(t *testing.T) {
	type Req struct {
		Price *int `sql:"price < ?"`
	}
	price := 5000
	req := Req{Price: &price}
	sql, args := sharksql.Where(req)
	expected := "price < ?"
	if sql != expected || args[0] != 5000 {
		t.Errorf("sql = %s, args = %v", sql, args)
	}
}

func TestWhereLte(t *testing.T) {
	type Req struct {
		Stock *int `sql:"stock <= ?"`
	}
	stock := 10
	req := Req{Stock: &stock}
	sql, args := sharksql.Where(req)
	expected := "stock <= ?"
	if sql != expected || args[0] != 10 {
		t.Errorf("sql = %s, args = %v", sql, args)
	}
}

func TestWhereNeq(t *testing.T) {
	type Req struct {
		Deleted *int `sql:"deleted <> ?"`
	}
	deleted := 1
	req := Req{Deleted: &deleted}
	sql, args := sharksql.Where(req)
	expected := "deleted <> ?"
	if sql != expected || args[0] != 1 {
		t.Errorf("sql = %s, args = %v", sql, args)
	}
}

func TestWhereLikeICaseLower(t *testing.T) {
	type Req struct {
		Name *string `sql:"name like ?"`
	}
	name := "test"
	req := Req{Name: &name}
	sql, args := sharksql.Where(req)
	expected := "name like ?"
	if sql != expected || args[0] != "%test%" {
		t.Errorf("sql = %s, args = %v", sql, args)
	}
}

func TestWhereInICase(t *testing.T) {
	type Req struct {
		Ids []int `sql:"id in (?)"`
	}
	req := Req{Ids: []int{1, 2}}
	sql, args := sharksql.Where(req)
	expected := "id in (?)"
	if sql != expected || len(args) != 1 {
		t.Errorf("sql = %s", sql)
	}
}

func TestWhereNotInICase(t *testing.T) {
	type Req struct {
		Ids []int `sql:"id not in (?)"`
	}
	req := Req{Ids: []int{5, 6}}
	sql, args := sharksql.Where(req)
	expected := "id not in (?)"
	if sql != expected || len(args) != 1 {
		t.Errorf("sql = %s", sql)
	}
}

// ========== ToUpdate 测试 (json tag) ==========

func TestToUpdatePtr(t *testing.T) {
	type Req struct {
		Name *string `json:"name"`
		Age  *int    `json:"age"`
	}
	n := "张三"
	a := 25
	req := Req{Name: &n, Age: &a}
	data := sharksql.ToUpdateData(req)
	if len(data) != 2 {
		t.Fatalf("len = %d", len(data))
	}
	if data["name"] != "张三" || data["age"] != 25 {
		t.Errorf("data = %v", data)
	}
}

func TestToUpdateNil(t *testing.T) {
	type Req struct {
		Name *string `json:"name"`
		Age  *int    `json:"age"`
	}
	n := "张三"
	req := Req{Name: &n, Age: nil}
	data := sharksql.ToUpdateData(req)
	if len(data) != 1 || data["name"] != "张三" {
		t.Errorf("data = %v", data)
	}
}

func TestToUpdateAllNil(t *testing.T) {
	type Req struct {
		Name *string `json:"name"`
	}
	req := Req{}
	data := sharksql.ToUpdateData(req)
	if data != nil {
		t.Errorf("data = %v", data)
	}
}

func TestToUpdateSlicePtrJSON(t *testing.T) {
	type Req struct {
		Ids *[]int `json:"ids"`
	}
	ids := []int{1, 2, 3}
	req := Req{Ids: &ids}
	data := sharksql.ToUpdateData(req)
	if len(data) != 1 {
		t.Fatalf("len = %d", len(data))
	}
	s, ok := data["ids"].(string)
	if !ok {
		t.Fatalf("ids not string: %T", data["ids"])
	}
	var arr []int
	json.Unmarshal([]byte(s), &arr)
	if len(arr) != 3 {
		t.Errorf("arr len = %d", len(arr))
	}
}

func TestToUpdateStructPtrJSON(t *testing.T) {
	type Meta struct {
		V int `json:"v"`
	}
	type Req struct {
		Meta *Meta `json:"meta"`
	}
	req := Req{Meta: &Meta{V: 1}}
	data := sharksql.ToUpdateData(req)
	s, ok := data["meta"].(string)
	if !ok {
		t.Fatalf("meta not string: %T", data["meta"])
	}
	var m Meta
	json.Unmarshal([]byte(s), &m)
	if m.V != 1 {
		t.Errorf("V = %d", m.V)
	}
}

func TestToUpdateDecimal(t *testing.T) {
	type Req struct {
		Price *decimal.Decimal `json:"price"`
	}
	d := decimal.NewFromFloat(19.99)
	req := Req{Price: &d}
	data := sharksql.ToUpdateData(req)
	dd, ok := data["price"].(decimal.Decimal)
	if !ok || !dd.Equals(d) {
		t.Errorf("price = %v", data["price"])
	}
}

func TestToUpdateNonPtrIgnored(t *testing.T) {
	type Req struct {
		Name *string `json:"name"`
		Age  int     `json:"age"`
	}
	n := "test"
	req := Req{Name: &n, Age: 18}
	data := sharksql.ToUpdateData(req)
	if len(data) != 1 || data["name"] != "test" {
		t.Errorf("data = %v", data)
	}
}

func TestToUpdateNoJsonTagIgnored(t *testing.T) {
	type Req struct {
		Name   *string `json:"name"`
		Ignore *string
	}
	n := "hello"
	ig := "ignored"
	req := Req{Name: &n, Ignore: &ig}
	data := sharksql.ToUpdateData(req)
	if len(data) != 1 || data["name"] != "hello" {
		t.Errorf("data = %v", data)
	}
}
