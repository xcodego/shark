package test

import (
	"testing"

	"github.com/lornshark/shark/sharksql"
)

func TestBuilderBasic(t *testing.T) {
	b := sharksql.NewSql().Eq("status", 1)
	sql, args := b.Build()
	if sql != "status = ?" {
		t.Errorf("sql = %s, want status = ?", sql)
	}
	if len(args) != 1 || args[0] != 1 {
		t.Errorf("args = %v, want [1]", args)
	}
}

func TestBuilderMultipleAND(t *testing.T) {
	b := sharksql.NewSql().
		Eq("status", 1).
		Gte("age", 18).
		Lt("age", 60)
	sql, args := b.Build()
	expected := "(status = ? AND age >= ? AND age < ?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 3 {
		t.Errorf("args count = %d, want 3", len(args))
	}
}

func TestBuilderOr(t *testing.T) {
	b := sharksql.NewSql().
		Eq("created_by", 1).
		Or(sharksql.NewSql().Eq("assignee", 1))
	sql, args := b.Build()
	if sql != "created_by = ? OR assignee = ?" {
		t.Errorf("sql = %s", sql)
	}
	if len(args) != 2 {
		t.Errorf("args count = %d, want 2", len(args))
	}
}

func TestBuilderAndSingleGroup(t *testing.T) {
	b := sharksql.NewSql().Eq("a", 1)
	other := sharksql.NewSql().Eq("b", 2).Eq("c", 3)
	b.And(other)
	sql, _ := b.Build()
	if sql != "(a = ? AND b = ? AND c = ?)" {
		t.Errorf("sql = %s", sql)
	}
}

func TestBuilderAndMultiGroup(t *testing.T) {
	b := sharksql.NewSql().Eq("deleted", 0)
	sub := sharksql.NewSql().Eq("status", "pending").Or(sharksql.NewSql().Eq("status", "in_progress"))
	b.And(sub)
	sql, _ := b.Build()
	expected := "(deleted = ? AND (status = ? OR status = ?))"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
}

func TestBuilderEmptyValueSkip(t *testing.T) {
	b := sharksql.NewSql().
		Eq("name", nil).
		Eq("status", 1).
		In("ids", []int{})
	sql, args := b.Build()
	if sql != "status = ?" {
		t.Errorf("空值应跳过, sql = %s", sql)
	}
	if len(args) != 1 {
		t.Errorf("args = %v, want [1]", args)
	}
}

func TestBuilderBetween(t *testing.T) {
	b := sharksql.NewSql().Between("age", 18, 60)
	sql, args := b.Build()
	expected := "(age >= ? AND age < ?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 2 || args[0] != 18 || args[1] != 60 {
		t.Errorf("args = %v, want [18, 60]", args)
	}
}

func TestBuilderBetweenNilSkip(t *testing.T) {
	b := sharksql.NewSql().Between("age", 18, nil).Eq("status", 1)
	sql, _ := b.Build()
	if sql != "status = ?" {
		t.Errorf("Between with nil should skip, sql = %s", sql)
	}
}

func TestBuilderLike(t *testing.T) {
	b := sharksql.NewSql().Like("name", "张")
	sql, args := b.Build()
	if sql != "name LIKE ?" {
		t.Errorf("sql = %s", sql)
	}
	if len(args) != 1 || args[0] != "%张%" {
		t.Errorf("args = %v, want [%%张%%]", args)
	}
}

func TestBuilderLikeLeft(t *testing.T) {
	b := sharksql.NewSql().LikeLeft("email", "@qq.com")
	_, args := b.Build()
	if args[0] != "%@qq.com" {
		t.Errorf("args[0] = %v, want %%@qq.com", args[0])
	}
}

func TestBuilderLikeRight(t *testing.T) {
	b := sharksql.NewSql().LikeRight("phone", "138")
	_, args := b.Build()
	if args[0] != "138%" {
		t.Errorf("args[0] = %v, want 138%%", args[0])
	}
}

func TestBuilderIn(t *testing.T) {
	b := sharksql.NewSql().In("status", []int{1, 2, 3})
	sql, _ := b.Build()
	if sql != "status IN (?)" {
		t.Errorf("sql = %s", sql)
	}
}

func TestBuilderNotIn(t *testing.T) {
	b := sharksql.NewSql().NotIn("id", []int64{100, 200})
	sql, _ := b.Build()
	if sql != "id NOT IN (?)" {
		t.Errorf("sql = %s", sql)
	}
}

func TestBuilderInNonSliceSkip(t *testing.T) {
	b := sharksql.NewSql().In("status", "not-a-slice").Eq("id", 1)
	sql, _ := b.Build()
	if sql != "id = ?" {
		t.Errorf("非切片 In 应跳过, sql = %s", sql)
	}
}

func TestBuilderIsNull(t *testing.T) {
	b := sharksql.NewSql().IsNull("deleted_at").Eq("status", 1)
	sql, _ := b.Build()
	if sql != "(deleted_at IS NULL AND status = ?)" {
		t.Errorf("sql = %s", sql)
	}
}

func TestBuilderIsNotNull(t *testing.T) {
	b := sharksql.NewSql().IsNotNull("email")
	sql, _ := b.Build()
	if sql != "email IS NOT NULL" {
		t.Errorf("sql = %s", sql)
	}
}

func TestBuilderNotLike(t *testing.T) {
	b := sharksql.NewSql().NotLike("name", "test")
	sql, args := b.Build()
	if sql != "name NOT LIKE ?" {
		t.Errorf("sql = %s", sql)
	}
	if args[0] != "%test%" {
		t.Errorf("args[0] = %v", args[0])
	}
}

func TestBuilderMultiplyOR(t *testing.T) {
	b := sharksql.NewSql().
		Eq("status", "pending").
		Or(sharksql.NewSql().Eq("status", "in_progress")).
		Or(sharksql.NewSql().Eq("status", "done"))
	sql, _ := b.Build()
	expected := "status = ? OR status = ? OR status = ?"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
}

func TestBuilderAllEmpty(t *testing.T) {
	b := sharksql.NewSql().
		Eq("name", nil).
		In("ids", []int{}).
		Between("age", nil, 60)
	sql, args := b.Build()
	if sql != "" {
		t.Errorf("空 Builder 应返回空字符串, got %s", sql)
	}
	if len(args) != 0 {
		t.Errorf("空 Builder args 应为空, got %v", args)
	}
}

func TestBuilderComplexNested(t *testing.T) {
	// (deleted = ? AND (status = ? OR status = ?) AND (created_by = ? OR assignee = ?))
	b := sharksql.NewSql().Eq("deleted", 0).
		And(sharksql.NewSql().Eq("status", "pending").Or(sharksql.NewSql().Eq("status", "done"))).
		And(sharksql.NewSql().Eq("created_by", 1).Or(sharksql.NewSql().Eq("assignee", 2)))
	sql, args := b.Build()
	expected := "(deleted = ? AND (status = ? OR status = ?) AND (created_by = ? OR assignee = ?))"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 5 {
		t.Errorf("args count = %d, want 5", len(args))
	}
	t.Logf("复杂嵌套 SQL: %s", sql)
	t.Logf("参数: %v", args)
}

func TestBuilderOrNil(t *testing.T) {
	b := sharksql.NewSql().Eq("a", 1).Or(nil)
	sql, _ := b.Build()
	if sql != "a = ?" {
		t.Errorf("Or(nil) should be noop, got %s", sql)
	}
}

func TestBuilderAndNil(t *testing.T) {
	b := sharksql.NewSql().Eq("a", 1).And(nil)
	sql, _ := b.Build()
	if sql != "a = ?" {
		t.Errorf("And(nil) should be noop, got %s", sql)
	}
}

func TestBuilderAndEmpty(t *testing.T) {
	b := sharksql.NewSql().Eq("a", 1).And(sharksql.NewSql())
	sql, _ := b.Build()
	if sql != "a = ?" {
		t.Errorf("And(empty) should be noop, got %s", sql)
	}
}

func TestBuilderNeq(t *testing.T) {
	b := sharksql.NewSql().Neq("status", 0)
	sql, _ := b.Build()
	if sql != "status <> ?" {
		t.Errorf("sql = %s, want status <> ?", sql)
	}
}

func TestBuilderGteLte(t *testing.T) {
	b := sharksql.NewSql().Gte("score", 60).Lte("score", 100)
	sql, _ := b.Build()
	expected := "(score >= ? AND score <= ?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
}

// ========== 字段对字段（EqCol 等）测试 ==========

func TestBuilderEqCol(t *testing.T) {
	b := sharksql.NewSql().EqCol("u.id", "o.user_id")
	sql, args := b.Build()
	if sql != "u.id = o.user_id" {
		t.Errorf("sql = %s, want u.id = o.user_id", sql)
	}
	if len(args) != 0 {
		t.Errorf("EqCol should not produce args, got %v", args)
	}
}

func TestBuilderEqColWithValue(t *testing.T) {
	// 混合字段对字段和字段对值
	b := sharksql.NewSql().
		EqCol("u.id", "o.user_id").
		EqCol("u.org_id", "o.org_id").
		Eq("o.deleted", 0)
	sql, args := b.Build()
	expected := "(u.id = o.user_id AND u.org_id = o.org_id AND o.deleted = ?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 1 || args[0] != 0 {
		t.Errorf("args = %v, want [0]", args)
	}
}

func TestBuilderNeqCol(t *testing.T) {
	b := sharksql.NewSql().NeqCol("u.status", "o.status")
	sql, _ := b.Build()
	if sql != "u.status <> o.status" {
		t.Errorf("sql = %s", sql)
	}
}

func TestBuilderGtCol(t *testing.T) {
	b := sharksql.NewSql().GtCol("u.score", "o.pass_score")
	sql, _ := b.Build()
	if sql != "u.score > o.pass_score" {
		t.Errorf("sql = %s", sql)
	}
}

func TestBuilderGteCol(t *testing.T) {
	b := sharksql.NewSql().GteCol("t1.amount", "t2.min_amount")
	sql, _ := b.Build()
	if sql != "t1.amount >= t2.min_amount" {
		t.Errorf("sql = %s", sql)
	}
}

func TestBuilderLtCol(t *testing.T) {
	b := sharksql.NewSql().LtCol("a.start_time", "b.end_time")
	sql, _ := b.Build()
	if sql != "a.start_time < b.end_time" {
		t.Errorf("sql = %s", sql)
	}
}

func TestBuilderLteCol(t *testing.T) {
	b := sharksql.NewSql().LteCol("a.end_time", "b.start_time")
	sql, _ := b.Build()
	if sql != "a.end_time <= b.start_time" {
		t.Errorf("sql = %s", sql)
	}
}

func TestBuilderJoinOnExample(t *testing.T) {
	// 模拟真实的 JOIN ON 场景
	// LEFT JOIN orders o ON u.id = o.user_id AND o.deleted = 0
	onB := sharksql.NewSql().
		EqCol("u.id", "o.user_id").
		Eq("o.deleted", 0)
	sql, args := onB.Build()
	expected := "(u.id = o.user_id AND o.deleted = ?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 1 || args[0] != 0 {
		t.Errorf("args = %v, want [0]", args)
	}
}
