package test

import (
	"testing"

	"github.com/lornshark/shark/sharksql"
)

// ---------------------------------------------------------------------------
// 辅助函数：指针构建器
// ---------------------------------------------------------------------------

func intPtr(v int) *int         { return &v }
func strPtr(v string) *string   { return &v }
func f64Ptr(v float64) *float64 { return &v }

// ---------------------------------------------------------------------------
// 比较运算符
// ---------------------------------------------------------------------------

func TestWhere_Eq(t *testing.T) {
	type Req struct {
		Status *int `sql:"status = ?"`
	}
	sql, args := sharksql.Where(Req{Status: intPtr(1)})
	if sql != "status = ?" || args[0] != 1 {
		t.Errorf("Eq: sql=%q args=%v, want status = ? [1]", sql, args)
	}
}

func TestWhere_Neq(t *testing.T) {
	type Req struct {
		Status *int `sql:"status <> ?"`
	}
	sql, args := sharksql.Where(Req{Status: intPtr(0)})
	if sql != "status <> ?" || args[0] != 0 {
		t.Errorf("Neq: sql=%q args=%v", sql, args)
	}
}

func TestWhere_Gt(t *testing.T) {
	type Req struct {
		Age *int `sql:"age > ?"`
	}
	sql, args := sharksql.Where(Req{Age: intPtr(18)})
	if sql != "age > ?" || args[0] != 18 {
		t.Errorf("Gt: sql=%q args=%v", sql, args)
	}
}

func TestWhere_Gte(t *testing.T) {
	type Req struct {
		Amount *float64 `sql:"amount >= ?"`
	}
	sql, args := sharksql.Where(Req{Amount: f64Ptr(100.0)})
	if sql != "amount >= ?" || args[0] != 100.0 {
		t.Errorf("Gte: sql=%q args=%v", sql, args)
	}
}

func TestWhere_Lt(t *testing.T) {
	type Req struct {
		Price *int `sql:"price < ?"`
	}
	sql, args := sharksql.Where(Req{Price: intPtr(5000)})
	if sql != "price < ?" || args[0] != 5000 {
		t.Errorf("Lt: sql=%q args=%v", sql, args)
	}
}

func TestWhere_Lte(t *testing.T) {
	type Req struct {
		Stock *int `sql:"stock <= ?"`
	}
	sql, args := sharksql.Where(Req{Stock: intPtr(50)})
	if sql != "stock <= ?" || args[0] != 50 {
		t.Errorf("Lte: sql=%q args=%v", sql, args)
	}
}

// ---------------------------------------------------------------------------
// 模糊匹配
// ---------------------------------------------------------------------------

func TestWhere_Like(t *testing.T) {
	type Req struct {
		Name *string `sql:"name LIKE ?"`
	}
	sql, args := sharksql.Where(Req{Name: strPtr("张")})
	if sql != "name LIKE ?" || args[0] != "%张%" {
		t.Errorf("Like: sql=%q args=%v, want name LIKE ? [%s]", sql, args, "%张%")
	}
}

func TestWhere_NotLike(t *testing.T) {
	type Req struct {
		Name *string `sql:"name NOT LIKE ?"`
	}
	sql, args := sharksql.Where(Req{Name: strPtr("test")})
	if sql != "name NOT LIKE ?" || args[0] != "%test%" {
		t.Errorf("NotLike: sql=%q args=%v", sql, args)
	}
}

func TestWhere_LikeL(t *testing.T) {
	type Req struct {
		Phone *string `sql:"phone LIKEL ?"`
	}
	sql, args := sharksql.Where(Req{Phone: strPtr("138")})
	if sql != "phone LIKE ?" || args[0] != "138%" {
		t.Errorf("LIKEL: sql=%q args=%v, want phone LIKE ? [138%%]", sql, args)
	}
}

func TestWhere_LikeR(t *testing.T) {
	type Req struct {
		Email *string `sql:"email LIKER ?"`
	}
	sql, args := sharksql.Where(Req{Email: strPtr("@qq.com")})
	if sql != "email LIKE ?" || args[0] != "%@qq.com" {
		t.Errorf("LIKER: sql=%q args=%v, want email LIKE ? [%%@qq.com]", sql, args)
	}
}

// ---------------------------------------------------------------------------
// 集合运算符
// ---------------------------------------------------------------------------

func TestWhere_In(t *testing.T) {
	type Req struct {
		Status []int `sql:"status IN (?)"`
	}
	sql, args := sharksql.Where(Req{Status: []int{1, 2, 3}})
	if sql != "status IN (?)" {
		t.Errorf("In: sql=%q", sql)
	}
	slice, ok := args[0].([]int)
	if !ok || len(slice) != 3 || slice[0] != 1 {
		t.Errorf("In: args=%v, want [1 2 3]", args)
	}
}

func TestWhere_NotIn(t *testing.T) {
	type Req struct {
		IDs []int64 `sql:"id NOT IN (?)"`
	}
	sql, args := sharksql.Where(Req{IDs: []int64{100, 200}})
	if sql != "id NOT IN (?)" {
		t.Errorf("NotIn: sql=%q", sql)
	}
	if len(args) != 1 {
		t.Errorf("NotIn: args len=%d, want 1", len(args))
	}
}

func TestWhere_InEmptySlice(t *testing.T) {
	type Req struct {
		Status []int `sql:"status IN (?)"`
	}
	sql, _ := sharksql.Where(Req{Status: []int{}})
	if sql != "" {
		t.Errorf("空切片应跳过, got sql=%q", sql)
	}
}

// ---------------------------------------------------------------------------
// nil 指针跳过
// ---------------------------------------------------------------------------

func TestWhere_NilSkip(t *testing.T) {
	type Req struct {
		Status *int    `sql:"status = ?"`
		Name   *string `sql:"name LIKE ?"`
		OrgID  *int    `sql:"org_id = ?"`
	}
	sql, args := sharksql.Where(Req{
		Status: intPtr(1),
		Name:   nil,
		OrgID:  nil,
	})
	if sql != "status = ?" || len(args) != 1 || args[0] != 1 {
		t.Errorf("nil应跳过: sql=%q args=%v, want status = ? [1]", sql, args)
	}
}

func TestWhere_AllNil(t *testing.T) {
	type Req struct {
		Status *int    `sql:"status = ?"`
		Name   *string `sql:"name LIKE ?"`
	}
	sql, args := sharksql.Where(Req{})
	if sql != "" || len(args) != 0 {
		t.Errorf("全nil应返回空: sql=%q args=%v", sql, args)
	}
}

// ---------------------------------------------------------------------------
// 多字段组合 AND
// ---------------------------------------------------------------------------

func TestWhere_MultiAND(t *testing.T) {
	type Req struct {
		Status    *int    `sql:"status = ?"`
		Name      *string `sql:"name LIKE ?"`
		MinAmount *int    `sql:"amount >= ?"`
		MaxAmount *int    `sql:"amount <= ?"`
		OrgID     *int    `sql:"org_id = ?"`
	}
	sql, args := sharksql.Where(Req{
		Status:    intPtr(1),
		Name:      strPtr("张"),
		MinAmount: intPtr(100),
		MaxAmount: intPtr(5000),
		OrgID:     nil,
	})
	expected := "status = ? AND name LIKE ? AND amount >= ? AND amount <= ?"
	if sql != expected {
		t.Errorf("MultiAND: sql=%q, want %q", sql, expected)
	}
	if len(args) != 4 {
		t.Errorf("MultiAND: args count=%d, want 4", len(args))
	}
	if args[0] != 1 || args[1] != "%张%" || args[2] != 100 || args[3] != 5000 {
		t.Errorf("MultiAND: args=%v, want [1 %s 100 5000]", args, "%张%")
	}
	t.Logf("MultiAND SQL: %s", sql)
	t.Logf("MultiAND Args: %v", args)
}

// ---------------------------------------------------------------------------
// 与 Pagination 组合
// ---------------------------------------------------------------------------

func TestWhere_WithPagination(t *testing.T) {
	type ListReq struct {
		sharksql.Pagination
		Status *int    `sql:"status = ?"`
		Name   *string `sql:"name LIKE ?"`
		City   *string `sql:"city = ?"`
	}
	req := ListReq{
		Pagination: sharksql.Pagination{Page: 1, PageSize: 20},
		Status:     intPtr(1),
		Name:       strPtr("张三"),
		City:       nil,
	}
	sql, args := sharksql.Where(req)
	if sql != "status = ? AND name LIKE ?" {
		t.Errorf("WithPagination: sql=%q", sql)
	}
	if len(args) != 2 || args[0] != 1 || args[1] != "%张三%" {
		t.Errorf("WithPagination: args=%v", args)
	}
}

// ---------------------------------------------------------------------------
// 指针传递（req 为指针）
// ---------------------------------------------------------------------------

func TestWhere_PointerReq(t *testing.T) {
	type Req struct {
		Status *int `sql:"status = ?"`
	}
	req := &Req{Status: intPtr(1)}
	sql, args := sharksql.Where(req)
	if sql != "status = ?" || args[0] != 1 {
		t.Errorf("指针req: sql=%q args=%v", sql, args)
	}
}

func TestWhere_NilPointerReq(t *testing.T) {
	type Req struct {
		Status *int `sql:"status = ?"`
	}
	var req *Req = nil
	sql, args := sharksql.Where(req)
	if sql != "" || len(args) != 0 {
		t.Errorf("nil指针req: sql=%q args=%v, want empty", sql, args)
	}
}

// ---------------------------------------------------------------------------
// json:"-" 字段仍可通过 sql tag 生成条件
// ---------------------------------------------------------------------------

func TestWhere_JsonMinus(t *testing.T) {
	type Req struct {
		MinAge *int `json:"-" sql:"age >= ?"`
		MaxAge *int `json:"-" sql:"age <= ?"`
	}
	sql, args := sharksql.Where(Req{
		MinAge: intPtr(18),
		MaxAge: nil, // 跳过
	})
	if sql != "age >= ?" {
		t.Errorf("json:-: sql=%q, want age >= ?", sql)
	}
	if args[0] != 18 {
		t.Errorf("json:-: args=%v", args)
	}
}

// ---------------------------------------------------------------------------
// 非 struct 类型返回空
// ---------------------------------------------------------------------------

func TestWhere_NonStruct(t *testing.T) {
	sql, args := sharksql.Where(42)
	if sql != "" || len(args) != 0 {
		t.Errorf("非struct: sql=%q args=%v, want empty", sql, args)
	}
}

// ---------------------------------------------------------------------------
// ToUpdate 测试
// ---------------------------------------------------------------------------

func TestToUpdate_Basic(t *testing.T) {
	type UpdateReq struct {
		Name *string `json:"name"`
		Age  *int    `json:"age"`
		Bio  *string `json:"bio"`
	}
	req := UpdateReq{Name: strPtr("张三"), Age: intPtr(25)}
	data := sharksql.ToUpdateData(req)
	if data["name"] != "张三" {
		t.Errorf("ToUpdate name=%v, want 张三", data["name"])
	}
	if data["age"] != 25 {
		t.Errorf("ToUpdate age=%v, want 25", data["age"])
	}
	if _, ok := data["bio"]; ok {
		t.Error("ToUpdate: nil bio should not be present")
	}
}
