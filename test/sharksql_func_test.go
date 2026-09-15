package test

import (
	"strings"
	"testing"

	"github.com/lornshark/shark/sharksql"
)

func TestEq(t *testing.T) {
	sql, val := sharksql.Eq("status", 1)
	if sql != "status = ?" {
		t.Errorf("sql = %s", sql)
	}
	if val != 1 {
		t.Errorf("val = %v", val)
	}
}

func TestNeq(t *testing.T) {
	sql, _ := sharksql.Neq("status", 0)
	if sql != "status <> ?" {
		t.Errorf("sql = %s", sql)
	}
}

func TestGt(t *testing.T) {
	sql, _ := sharksql.Gt("age", 18)
	if sql != "age > ?" {
		t.Errorf("sql = %s", sql)
	}
}

func TestGte(t *testing.T) {
	sql, _ := sharksql.Gte("score", 60)
	if sql != "score >= ?" {
		t.Errorf("sql = %s", sql)
	}
}

func TestLt(t *testing.T) {
	sql, _ := sharksql.Lt("price", 100)
	if sql != "price < ?" {
		t.Errorf("sql = %s", sql)
	}
}

func TestLte(t *testing.T) {
	sql, _ := sharksql.Lte("stock", 50)
	if sql != "stock <= ?" {
		t.Errorf("sql = %s", sql)
	}
}

func TestLike(t *testing.T) {
	sql, val := sharksql.Like("name", "张")
	if sql != "name LIKE ?" {
		t.Errorf("sql = %s", sql)
	}
	if val != "%张%" {
		t.Errorf("val = %v, want %%张%%", val)
	}
}

func TestNotLike(t *testing.T) {
	sql, val := sharksql.NotLike("name", "test")
	if sql != "name NOT LIKE ?" {
		t.Errorf("sql = %s", sql)
	}
	if val != "%test%" {
		t.Errorf("val = %v", val)
	}
}

func TestIn(t *testing.T) {
	sql, val := sharksql.In("status", []int{1, 2, 3})
	if sql != "status IN (?)" {
		t.Errorf("sql = %s", sql)
	}
	slice, ok := val.([]int)
	if !ok || len(slice) != 3 {
		t.Errorf("val = %v", val)
	}
}

func TestNotIn(t *testing.T) {
	sql, _ := sharksql.NotIn("id", []int64{100, 200})
	if sql != "id NOT IN (?)" {
		t.Errorf("sql = %s", sql)
	}
}

func TestIsNull(t *testing.T) {
	sql := sharksql.IsNull("deleted_at")
	if sql != "deleted_at IS NULL" {
		t.Errorf("sql = %s", sql)
	}
}

func TestIsNotNull(t *testing.T) {
	sql := sharksql.IsNotNull("email")
	if sql != "email IS NOT NULL" {
		t.Errorf("sql = %s", sql)
	}
}

func TestAdd(t *testing.T) {
	sql, val := sharksql.Add("balance", 100)
	if sql != "balance + ?" {
		t.Errorf("sql = %s", sql)
	}
	if val != 100 {
		t.Errorf("val = %v", val)
	}
}

func TestSub(t *testing.T) {
	sql, _ := sharksql.Sub("balance", 50)
	if sql != "balance - ?" {
		t.Errorf("sql = %s", sql)
	}
}

func TestMul(t *testing.T) {
	sql, _ := sharksql.Mul("price", 1.1)
	if sql != "price * ?" {
		t.Errorf("sql = %s", sql)
	}
}

func TestDiv(t *testing.T) {
	sql, _ := sharksql.Div("total_score", "count")
	if sql != "total_score / ?" {
		t.Errorf("sql = %s", sql)
	}
}

func TestAsc(t *testing.T) {
	s := sharksql.Asc("created_at")
	if s != "created_at ASC" {
		t.Errorf("Asc = %s", s)
	}
}

func TestDesc(t *testing.T) {
	s := sharksql.Desc("amount")
	if s != "amount DESC" {
		t.Errorf("Desc = %s", s)
	}
}

func TestFromTo(t *testing.T) {
	sql, from, to := sharksql.FromTo("created_at", "2025-01-01", "2025-02-01")
	expected := "created_at >= ? AND created_at < ?"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if from != "2025-01-01" || to != "2025-02-01" {
		t.Errorf("from=%v to=%v", from, to)
	}
}

func TestSum(t *testing.T) {
	s := sharksql.Sum("bet_amount", "win_amount")
	if s != "sum(bet_amount), sum(win_amount)" {
		t.Errorf("Sum = %s", s)
	}
}

func TestSumAs(t *testing.T) {
	s := sharksql.SumAs("bet_amount", "total_bet", "win_amount", "total_win")
	if s != "sum(bet_amount) as total_bet, sum(win_amount) as total_win" {
		t.Errorf("SumAs = %s", s)
	}
}

func TestSumAsOdd(t *testing.T) {
	s := sharksql.SumAs("a", "b", "c")
	if s != "" {
		t.Errorf("奇数参数应返回空: got %s", s)
	}
}

func TestCountAs(t *testing.T) {
	s := sharksql.CountAs("id", "total_count", "user_id", "unique_users")
	if s != "count(id) as total_count, count(user_id) as unique_users" {
		t.Errorf("CountAs = %s", s)
	}
}

func TestAvg(t *testing.T) {
	s := sharksql.Avg("math_score", "english_score")
	if s != "avg(math_score), avg(english_score)" {
		t.Errorf("Avg = %s", s)
	}
}

func TestAvgAs(t *testing.T) {
	s := sharksql.AvgAs("math_score", "avg_math", "english_score", "avg_english")
	if s != "avg(math_score) as avg_math, avg(english_score) as avg_english" {
		t.Errorf("AvgAs = %s", s)
	}
}

func TestMax(t *testing.T) {
	s := sharksql.Max("high_temp", "low_temp")
	if s != "max(high_temp), max(low_temp)" {
		t.Errorf("Max = %s", s)
	}
}

func TestMaxAs(t *testing.T) {
	s := sharksql.MaxAs("high_temp", "max_high", "low_temp", "max_low")
	if s != "max(high_temp) as max_high, max(low_temp) as max_low" {
		t.Errorf("MaxAs = %s", s)
	}
}

func TestMin(t *testing.T) {
	s := sharksql.Min("price")
	if s != "min(price)" {
		t.Errorf("Min = %s", s)
	}
}

func TestMinAs(t *testing.T) {
	s := sharksql.MinAs("price", "min_price", "discount", "min_discount")
	if s != "min(price) as min_price, min(discount) as min_discount" {
		t.Errorf("MinAs = %s", s)
	}
}

func TestColumn(t *testing.T) {
	s := sharksql.Column("users", "id")
	if s != "users.id" {
		t.Errorf("Column = %s", s)
	}
}

func TestColumnAs(t *testing.T) {
	s := sharksql.ColumnAs("users", "id", "user_id")
	if s != "users.id as user_id" {
		t.Errorf("ColumnAs = %s", s)
	}
}

func TestJsonPath(t *testing.T) {
	p := sharksql.JsonPath("user", "address", "city")
	if p != "$.user.address.city" {
		t.Errorf("JsonPath = %s", p)
	}
}

func TestJsonPathSingle(t *testing.T) {
	p := sharksql.JsonPath("name")
	if p != "$.name" {
		t.Errorf("JsonPath single = %s", p)
	}
}

func TestJsonSearchOne(t *testing.T) {
	sql, data := sharksql.JsonSearchOne("tags", "vip")
	if sql != "JSON_SEARCH(tags, 'one', ?) IS NOT NULL" {
		t.Errorf("sql = %s", sql)
	}
	if data != "%vip%" {
		t.Errorf("data = %s", data)
	}
}

func TestJsonContains(t *testing.T) {
	sql, data := sharksql.JsonContains("roles", `"admin"`)
	if sql != "JSON_CONTAINS(roles, ?)" {
		t.Errorf("sql = %s", sql)
	}
	if data != `"admin"` {
		t.Errorf("data = %s", data)
	}
}

func TestJsonSet(t *testing.T) {
	sql, args := sharksql.JsonSet("metadata", "$.age", 25)
	if sql != "JSON_SET(COALESCE(metadata, JSON_OBJECT()), '$.age', ?)" {
		t.Errorf("sql = %s", sql)
	}
	if len(args) != 1 || args[0] != 25 {
		t.Errorf("args = %v", args)
	}
}

func TestJsonSetObject(t *testing.T) {
	sql, args := sharksql.JsonSetObject("metadata", "$.vip", map[string]any{"level": 3})
	if sql != "JSON_SET(COALESCE(metadata, JSON_OBJECT()), '$.vip', CONVERT(?,JSON))" {
		t.Errorf("sql = %s", sql)
	}
	if len(args) != 1 {
		t.Errorf("args length = %d, want 1", len(args))
	}
}

func TestJsonArrayAppend(t *testing.T) {
	sql, args := sharksql.JsonArrayAppend("event_ids", 100, 200, 300)
	if sql != "JSON_ARRAY_APPEND(COALESCE(event_ids, JSON_ARRAY()),'$', ?,'$', ?,'$', ?)" {
		t.Errorf("sql = %s", sql)
	}
	if len(args) != 3 {
		t.Errorf("args length = %d, want 3", len(args))
	}
}

func TestJsonArrayAppendObject(t *testing.T) {
	sql, args := sharksql.JsonArrayAppendObject("tags", "vip", "premium")
	if sql != "JSON_ARRAY_APPEND(COALESCE(tags, JSON_ARRAY()),'$', CAST(? AS JSON),'$', CAST(? AS JSON))" {
		t.Errorf("sql = %s", sql)
	}
	if len(args) != 2 {
		t.Errorf("args length = %d, want 2", len(args))
	}
}

// ========== 新 JSON 函数测试 ==========

func TestJsonExtract(t *testing.T) {
	s := sharksql.JsonExtract("metadata", "$.name")
	if s != "JSON_EXTRACT(metadata, '$.name')" {
		t.Errorf("JsonExtract = %s", s)
	}
}

func TestJsonExtractMultiple(t *testing.T) {
	s := sharksql.JsonExtract("metadata", "$.name", "$.age")
	if s != "JSON_EXTRACT(metadata, '$.name', '$.age')" {
		t.Errorf("JsonExtract multiple = %s", s)
	}
}

func TestJsonExtractEmpty(t *testing.T) {
	s := sharksql.JsonExtract("metadata")
	if s != "JSON_EXTRACT(metadata, '$')" {
		t.Errorf("JsonExtract empty = %s", s)
	}
}

func TestJsonUnquote(t *testing.T) {
	s := sharksql.JsonUnquote("metadata", "$.city")
	if s != "JSON_UNQUOTE(JSON_EXTRACT(metadata, '$.city'))" {
		t.Errorf("JsonUnquote = %s", s)
	}
}

func TestJsonRemove(t *testing.T) {
	s := sharksql.JsonRemove("tags", "$[0]")
	if s != "JSON_REMOVE(tags, '$[0]')" {
		t.Errorf("JsonRemove = %s", s)
	}
}

func TestJsonRemoveMultiple(t *testing.T) {
	s := sharksql.JsonRemove("metadata", "$.temp", "$.cache")
	if s != "JSON_REMOVE(metadata, '$.temp', '$.cache')" {
		t.Errorf("JsonRemove multiple = %s", s)
	}
}

func TestJsonArrayInsert(t *testing.T) {
	sql, args := sharksql.JsonArrayInsert("tags", "$[0]", "vip")
	if sql != "JSON_ARRAY_INSERT(COALESCE(tags, JSON_ARRAY()), '$[0]', CAST(? AS JSON))" {
		t.Errorf("sql = %s", sql)
	}
	if len(args) != 1 || args[0] != "vip" {
		t.Errorf("args = %v", args)
	}
}

func TestJsonArrayInsertObject(t *testing.T) {
	sql, args := sharksql.JsonArrayInsert("tags", "$[1]", map[string]any{"name": "vip"})
	if sql != "JSON_ARRAY_INSERT(COALESCE(tags, JSON_ARRAY()), '$[1]', CAST(? AS JSON))" {
		t.Errorf("sql = %s", sql)
	}
	if len(args) != 1 {
		t.Errorf("args count = %d", len(args))
	}
}

func TestJsonLength(t *testing.T) {
	s := sharksql.JsonLength("tags")
	if s != "JSON_LENGTH(tags)" {
		t.Errorf("JsonLength = %s", s)
	}
}

func TestJsonKeys(t *testing.T) {
	s := sharksql.JsonKeys("metadata")
	if s != "JSON_KEYS(metadata)" {
		t.Errorf("JsonKeys = %s", s)
	}
}

func TestJsonType(t *testing.T) {
	s := sharksql.JsonType("metadata")
	if s != "JSON_TYPE(metadata)" {
		t.Errorf("JsonType = %s", s)
	}
}

// ========== Count 测试 ==========

func TestCount(t *testing.T) {
	s := sharksql.Count("id")
	if s != "count(id)" {
		t.Errorf("Count = %s, want count(id)", s)
	}
}

func TestCountStar(t *testing.T) {
	s := sharksql.Count("*")
	if s != "count(*)" {
		t.Errorf("Count = %s, want count(*)", s)
	}
}

func TestCountDistinct(t *testing.T) {
	s := sharksql.Count("DISTINCT user_id")
	if s != "count(DISTINCT user_id)" {
		t.Errorf("Count = %s", s)
	}
}

// ========== Between 测试 ==========

func TestBetween(t *testing.T) {
	sql, lo, hi := sharksql.Between("amount", 100, 500)
	if sql != "amount >= ? AND amount < ?" {
		t.Errorf("sql = %s", sql)
	}
	if lo != 100 || hi != 500 {
		t.Errorf("lo=%v hi=%v", lo, hi)
	}
}

// ========== Distinct 测试 ==========

func TestDistinctSingle(t *testing.T) {
	s := sharksql.Distinct("status")
	if s != "DISTINCT status" {
		t.Errorf("Distinct single = %s, want DISTINCT status", s)
	}
}

func TestDistinctMultiple(t *testing.T) {
	s := sharksql.Distinct("user_id", "org_id")
	if s != "DISTINCT(user_id, org_id)" {
		t.Errorf("Distinct multiple = %s", s)
	}
}

func TestDistinctEmpty(t *testing.T) {
	s := sharksql.Distinct()
	if s != "" {
		t.Errorf("Distinct empty = %s, want empty string", s)
	}
}

// ========== LeftJoin 测试 ==========

func TestLeftJoinBasic(t *testing.T) {
	onB := sharksql.NewSql().EqCol("u.id", "o.user_id")
	sql, args := sharksql.LeftJoin("orders o", onB)
	expected := "LEFT JOIN orders o ON u.id = o.user_id"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want nil", args)
	}
}

func TestLeftJoinWithValueCondition(t *testing.T) {
	onB := sharksql.NewSql().
		EqCol("u.id", "o.user_id").
		Eq("o.deleted", 0)
	sql, args := sharksql.LeftJoin("orders o", onB)
	expected := "LEFT JOIN orders o ON (u.id = o.user_id AND o.deleted = ?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 1 || args[0] != 0 {
		t.Errorf("args = %v, want [0]", args)
	}
}

func TestLeftJoinMultipleFields(t *testing.T) {
	onB := sharksql.NewSql().
		EqCol("u.id", "o.user_id").
		EqCol("u.org_id", "o.org_id").
		Eq("o.status", "active")
	sql, args := sharksql.LeftJoin("orders o", onB)
	expected := "LEFT JOIN orders o ON (u.id = o.user_id AND u.org_id = o.org_id AND o.status = ?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 1 || args[0] != "active" {
		t.Errorf("args = %v, want [active]", args)
	}
}

func TestLeftJoinWithOrCondition(t *testing.T) {
	// LEFT JOIN ... ON u.id = o.user_id AND (o.status = ? OR o.type = ?)
	statusB := sharksql.NewSql().Eq("o.status", "pending").Or(sharksql.NewSql().Eq("o.type", "urgent"))
	onB := sharksql.NewSql().EqCol("u.id", "o.user_id").And(statusB)
	sql, args := sharksql.LeftJoin("orders o", onB)
	expected := "LEFT JOIN orders o ON (u.id = o.user_id AND (o.status = ? OR o.type = ?))"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 2 {
		t.Errorf("args count = %d, want 2", len(args))
	}
}

func TestLeftJoinNilOn(t *testing.T) {
	sql, args := sharksql.LeftJoin("orders o", nil)
	if sql != "" {
		t.Errorf("nil on should return empty string, got %s", sql)
	}
	if len(args) != 0 {
		t.Errorf("nil on args should be nil, got %v", args)
	}
}

func TestLeftJoinEmptyOn(t *testing.T) {
	// 空 Builder（所有条件被跳过）→ 返回空
	onB := sharksql.NewSql().Eq("name", nil)
	sql, args := sharksql.LeftJoin("orders o", onB)
	if sql != "" {
		t.Errorf("empty on should return empty string, got %s", sql)
	}
	if len(args) != 0 {
		t.Errorf("empty on args should be nil, got %v", args)
	}
}

func TestLeftJoinPureValueOnly(t *testing.T) {
	// 仅包含字段对值的 ON 条件（无字段对字段）
	onB := sharksql.NewSql().
		Eq("o.deleted", 0).
		Eq("o.status", "active")
	sql, args := sharksql.LeftJoin("orders o", onB)
	expected := "LEFT JOIN orders o ON (o.deleted = ? AND o.status = ?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 2 {
		t.Errorf("args count = %d, want 2", len(args))
	}
}

// ========== InnerJoin 测试 ==========

func TestInnerJoinBasic(t *testing.T) {
	onB := sharksql.NewSql().EqCol("u.id", "o.user_id")
	sql, args := sharksql.InnerJoin("orders o", onB)
	expected := "INNER JOIN orders o ON u.id = o.user_id"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want nil", args)
	}
}

func TestInnerJoinWithValueCondition(t *testing.T) {
	onB := sharksql.NewSql().
		EqCol("u.id", "o.user_id").
		Eq("o.deleted", 0)
	sql, args := sharksql.InnerJoin("orders o", onB)
	expected := "INNER JOIN orders o ON (u.id = o.user_id AND o.deleted = ?)"
	if sql != expected {
		t.Errorf("sql = %s, want %s", sql, expected)
	}
	if len(args) != 1 || args[0] != 0 {
		t.Errorf("args = %v, want [0]", args)
	}
}

func TestInnerJoinNilOn(t *testing.T) {
	sql, args := sharksql.InnerJoin("orders o", nil)
	if sql != "" {
		t.Errorf("nil on should return empty string, got %s", sql)
	}
	if len(args) != 0 {
		t.Errorf("nil on args should be nil, got %v", args)
	}
}

func TestInnerJoinEmptyOn(t *testing.T) {
	onB := sharksql.NewSql().Eq("name", nil)
	sql, args := sharksql.InnerJoin("orders o", onB)
	if sql != "" {
		t.Errorf("empty on should return empty string, got %s", sql)
	}
	if len(args) != 0 {
		t.Errorf("empty on args should be nil, got %v", args)
	}
}

// ========== AddCol / SubCol / MulCol / DivCol / As 测试 ==========

func TestAddCol(t *testing.T) {
	s := sharksql.AddCol("base_salary", "bonus")
	if s != "base_salary + bonus" {
		t.Errorf("AddCol = %s, want base_salary + bonus", s)
	}
}

func TestAddColAs(t *testing.T) {
	s := sharksql.AddColAs("base_salary", "bonus", "total_income")
	if s != "(base_salary + bonus) as total_income" {
		t.Errorf("AddColAs = %s", s)
	}
}

func TestSubCol(t *testing.T) {
	s := sharksql.SubCol("revenue", "cost")
	if s != "revenue - cost" {
		t.Errorf("SubCol = %s, want revenue - cost", s)
	}
}

func TestSubColAs(t *testing.T) {
	s := sharksql.SubColAs("revenue", "cost", "profit")
	if s != "(revenue - cost) as profit" {
		t.Errorf("SubColAs = %s", s)
	}
}

func TestMulCol(t *testing.T) {
	s := sharksql.MulCol("price", "quantity")
	if s != "price * quantity" {
		t.Errorf("MulCol = %s, want price * quantity", s)
	}
}

func TestMulColAs(t *testing.T) {
	s := sharksql.MulColAs("price", "quantity", "total_amount")
	if s != "(price * quantity) as total_amount" {
		t.Errorf("MulColAs = %s", s)
	}
}

func TestDivCol(t *testing.T) {
	s := sharksql.DivCol("total_score", "count")
	if s != "total_score / count" {
		t.Errorf("DivCol = %s, want total_score / count", s)
	}
}

func TestDivColAs(t *testing.T) {
	s := sharksql.DivColAs("total_score", "count", "avg_score")
	if s != "(total_score / count) as avg_score" {
		t.Errorf("DivColAs = %s", s)
	}
}

func TestAs(t *testing.T) {
	s := sharksql.As(sharksql.Count("id"), "total_count")
	if s != "(count(id)) as total_count" {
		t.Errorf("As = %s", s)
	}
}

func TestAddColSelectPattern(t *testing.T) {
	// 模拟典型使用模式: SELECT (column + otherColumn) as alias
	fields := []string{
		sharksql.AddColAs("principal", "interest", "total"),
		sharksql.AddColAs("base", "bonus", "income"),
	}
	result := strings.Join(fields, ", ")
	expected := "(principal + interest) as total, (base + bonus) as income"
	if result != expected {
		t.Errorf("Select pattern = %s, want %s", result, expected)
	}
}

func TestMulColSelectPattern(t *testing.T) {
	// 模拟典型使用模式: SELECT (column * otherColumn) as alias
	fields := []string{
		sharksql.MulColAs("price", "quantity", "total_amount"),
		sharksql.MulColAs("unit_price", "count", "sub_total"),
	}
	result := strings.Join(fields, ", ")
	expected := "(price * quantity) as total_amount, (unit_price * count) as sub_total"
	if result != expected {
		t.Errorf("Select pattern = %s, want %s", result, expected)
	}
}

func TestMixedWithSum(t *testing.T) {
	// 混合使用 AddColAs 和 Sum 聚合函数
	fields := []string{
		sharksql.AddColAs("principal", "interest", "total"),
		sharksql.Sum("bet_amount", "win_amount"),
	}
	result := strings.Join(fields, ", ")
	expected := "(principal + interest) as total, sum(bet_amount), sum(win_amount)"
	if result != expected {
		t.Errorf("Mixed = %s, want %s", result, expected)
	}
}

// ========== Paren 测试 ==========

func TestParenColumn(t *testing.T) {
	s := sharksql.Paren("score")
	if s != "(score)" {
		t.Errorf("Paren = %s, want (score)", s)
	}
}

func TestParenExpression(t *testing.T) {
	s := sharksql.Paren("age >= 18 AND age <= 60")
	if s != "(age >= 18 AND age <= 60)" {
		t.Errorf("Paren = %s, want (age >= 18 AND age <= 60)", s)
	}
}

func TestParenWithAddCol(t *testing.T) {
	// 模拟 SELECT (base_salary + bonus) as total 模式
	// 注意：Paren 加了 () 后 As 还会再加一层 ()，产生双重括号
	expr := sharksql.AddCol("base_salary", "bonus")
	result := sharksql.Paren(expr)
	if result != "(base_salary + bonus)" {
		t.Errorf("Paren(AddCol) = %s, want (base_salary + bonus)", result)
	}
}

func TestParenEmpty(t *testing.T) {
	s := sharksql.Paren("")
	if s != "()" {
		t.Errorf("Paren empty = %s, want ()", s)
	}
}

// ========== Coalesce 测试 ==========

func TestCoalesce(t *testing.T) {
	s := sharksql.Coalesce("nickname", "'匿名用户'")
	if s != "COALESCE(nickname, '匿名用户')" {
		t.Errorf("Coalesce = %s", s)
	}
}

func TestCoalesceWithExpression(t *testing.T) {
	s := sharksql.Coalesce(sharksql.Sum("amount"), "0")
	if s != "COALESCE(sum(amount), 0)" {
		t.Errorf("Coalesce with Sum = %s", s)
	}
}

func TestCoalesceAs(t *testing.T) {
	// 最后一个参数是 alias
	s := sharksql.CoalesceAs("nickname", "'匿名用户'", "display_name")
	if s != "COALESCE(nickname, '匿名用户') as display_name" {
		t.Errorf("CoalesceAs = %s", s)
	}
}

func TestCoalesceAsWithExpression(t *testing.T) {
	s := sharksql.CoalesceAs(sharksql.Sum("amount"), "0", "total_amount")
	if s != "COALESCE(sum(amount), 0) as total_amount" {
		t.Errorf("CoalesceAs with Sum = %s", s)
	}
}

func TestCoalesceMultiArgs(t *testing.T) {
	s := sharksql.Coalesce("a", "b", "c")
	if s != "COALESCE(a, b, c)" {
		t.Errorf("Coalesce multi = %s", s)
	}
}

// ========== IfNull 测试 ==========

func TestIfNull(t *testing.T) {
	s := sharksql.IfNull("remark", "'无备注'")
	if s != "IFNULL(remark, '无备注')" {
		t.Errorf("IfNull = %s", s)
	}
}

func TestIfNullWithExpression(t *testing.T) {
	s := sharksql.IfNull(sharksql.Sum("amount"), "0")
	if s != "IFNULL(sum(amount), 0)" {
		t.Errorf("IfNull with Sum = %s", s)
	}
}

func TestIfNullAs(t *testing.T) {
	s := sharksql.IfNullAs("remark", "'无备注'", "remark_text")
	if s != "IFNULL(remark, '无备注') as remark_text" {
		t.Errorf("IfNullAs = %s", s)
	}
}

func TestIfNullAsWithExpression(t *testing.T) {
	s := sharksql.IfNullAs(sharksql.Sum("amount"), "0", "total")
	if s != "IFNULL(sum(amount), 0) as total" {
		t.Errorf("IfNullAs with Sum = %s", s)
	}
}

// ========== Case 测试 ==========

func TestCaseWithElse(t *testing.T) {
	s := sharksql.Case("status", "0", "'待支付'", "1", "'已支付'", "2", "'已取消'", "'未知'")
	expected := "CASE status WHEN 0 THEN '待支付' WHEN 1 THEN '已支付' WHEN 2 THEN '已取消' ELSE '未知' END"
	if s != expected {
		t.Errorf("Case = %s", s)
	}
}

func TestCaseWithoutElse(t *testing.T) {
	s := sharksql.Case("score", "90", "'优秀'", "80", "'良好'", "60", "'及格'")
	expected := "CASE score WHEN 90 THEN '优秀' WHEN 80 THEN '良好' WHEN 60 THEN '及格' END"
	if s != expected {
		t.Errorf("Case = %s", s)
	}
}

func TestCaseSingleWithElse(t *testing.T) {
	s := sharksql.Case("status", "0", "'待支付'", "'未知'")
	expected := "CASE status WHEN 0 THEN '待支付' ELSE '未知' END"
	if s != expected {
		t.Errorf("Case = %s", s)
	}
}

func TestCaseAs(t *testing.T) {
	s := sharksql.CaseAs("status", "status_name", "0", "'待支付'", "1", "'已支付'", "'未知'")
	expected := "CASE status WHEN 0 THEN '待支付' WHEN 1 THEN '已支付' ELSE '未知' END as status_name"
	if s != expected {
		t.Errorf("CaseAs = %s", s)
	}
}

func TestCaseAsWithoutElse(t *testing.T) {
	s := sharksql.CaseAs("score", "level", "90", "'优秀'", "80", "'良好'")
	expected := "CASE score WHEN 90 THEN '优秀' WHEN 80 THEN '良好' END as level"
	if s != expected {
		t.Errorf("CaseAs = %s", s)
	}
}

// ========== When 测试 ==========

func TestWhenEven(t *testing.T) {
	// 偶数个参数，没有 ELSE
	s := sharksql.When("a=1", 1, "b=2", 2, "c=3", 3, "d=4", 0)
	expected := "WHEN a=1 THEN 1 WHEN b=2 THEN 2 WHEN c=3 THEN 3 WHEN d=4 THEN 0"
	if s != expected {
		t.Errorf("When = %s", s)
	}
}

func TestWhenOdd(t *testing.T) {
	// 奇数个参数，最后一个是 ELSE
	s := sharksql.When("a=1", 1, "b=2", 2, "c=3")
	expected := "WHEN a=1 THEN 1 WHEN b=2 THEN 2 ELSE c=3"
	if s != expected {
		t.Errorf("When = %s", s)
	}
}

func TestWhenAs(t *testing.T) {
	s := sharksql.WhenAs("alias", "a=1", 1, "b=2", 2)
	expected := "WHEN a=1 THEN 1 WHEN b=2 THEN 2 as alias"
	if s != expected {
		t.Errorf("WhenAs = %s", s)
	}
}
