package test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lornshark/shark/sharkelastic"
	"github.com/lornshark/shark/sharkeswhere"
)

// ---------------------------------------------------------------------------
// 单元测试：词法分析 (Tokenize Where)
// ---------------------------------------------------------------------------

// tokenizeWhereSummary 对给定的 WHERE 字符串做词法分析，返回人类可读的 token 摘要。
func tokenizeWhereSummary(t *testing.T, input string) string {
	t.Helper()
	tokens, err := tokenizeWherePublic(input)
	if err != nil {
		t.Fatalf("tokenizeWhere(%q) error: %v", input, err)
	}
	var parts []string
	for _, tok := range tokens {
		if tok.Typ == "EOF" {
			break
		}
		parts = append(parts, tok.Typ+"="+tok.Val)
	}
	return strings.Join(parts, " ")
}

// TestTokenizeWhere_Equals 测试等号词法
func TestTokenizeWhere_Equals(t *testing.T) {
	summary := tokenizeWhereSummary(t, "id = 1")
	if summary != "FIELD=id OP== VALUE=1" {
		t.Errorf("id = 1 → %s", summary)
	}
}

// TestTokenizeWhere_NotEquals 测试不等词法
func TestTokenizeWhere_NotEquals(t *testing.T) {
	summary := tokenizeWhereSummary(t, "status != 0")
	if summary != "FIELD=status OP=!= VALUE=0" {
		t.Errorf("status != 0 → %s", summary)
	}
}

// TestTokenizeWhere_Range 测试范围运算符词法
func TestTokenizeWhere_Range(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"age > 18", "FIELD=age OP=> VALUE=18"},
		{"age >= 18", "FIELD=age OP=>= VALUE=18"},
		{"age < 60", "FIELD=age OP=< VALUE=60"},
		{"age <= 60", "FIELD=age OP=<= VALUE=60"},
	}
	for _, tc := range tests {
		summary := tokenizeWhereSummary(t, tc.input)
		if summary != tc.want {
			t.Errorf("%q → %s, want %s", tc.input, summary, tc.want)
		}
	}
}

// TestTokenizeWhere_LikeIn 测试 like/in 词法
func TestTokenizeWhere_LikeIn(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"name like '张三'", "FIELD=name OP=like VALUE=张三"},
		{"status in (1,2,3)", "FIELD=status OP=in LPAREN=( VALUE=1 COMMA=, VALUE=2 COMMA=, VALUE=3 RPAREN=)"},
	}
	for _, tc := range tests {
		summary := tokenizeWhereSummary(t, tc.input)
		if summary != tc.want {
			t.Errorf("%q → %s, want %s", tc.input, summary, tc.want)
		}
	}
}

// TestTokenizeWhere_AndOr 测试 and/or 词法
func TestTokenizeWhere_AndOr(t *testing.T) {
	summary := tokenizeWhereSummary(t, "id = 1 and status = 1")
	if !strings.Contains(summary, "AND=and") {
		t.Errorf("应包含 AND: %s", summary)
	}

	summary = tokenizeWhereSummary(t, "id = 1 or id = 2")
	if !strings.Contains(summary, "OR=or") {
		t.Errorf("应包含 OR: %s", summary)
	}
}

// TestTokenizeWhere_Paren 测试括号词法
func TestTokenizeWhere_Paren(t *testing.T) {
	summary := tokenizeWhereSummary(t, "(id = 1)")
	if !strings.Contains(summary, "LPAREN=(") || !strings.Contains(summary, "RPAREN=)") {
		t.Errorf("应包含括号: %s", summary)
	}
}

// TestTokenizeWhere_NegativeNumber 测试负数
func TestTokenizeWhere_NegativeNumber(t *testing.T) {
	summary := tokenizeWhereSummary(t, "score = -1")
	if summary != "FIELD=score OP== VALUE=-1" {
		t.Errorf("score = -1 → %s", summary)
	}
}

// TestTokenizeWhere_Float 测试浮点数
func TestTokenizeWhere_Float(t *testing.T) {
	summary := tokenizeWhereSummary(t, "price > 99.9")
	if summary != "FIELD=price OP=> VALUE=99.9" {
		t.Errorf("price > 99.9 → %s", summary)
	}
}

// TestTokenizeWhere_StringWithEscape 测试转义后的字符串
func TestTokenizeWhere_StringWithEscape(t *testing.T) {
	summary := tokenizeWhereSummary(t, `name = 'O\'Reilly'`)
	if summary != "FIELD=name OP== VALUE=O'Reilly" {
		t.Errorf("转义字符串 → %s", summary)
	}
}

// TestTokenizeWhere_DoubleQuote 测试双引号字符串
func TestTokenizeWhere_DoubleQuoteString(t *testing.T) {
	summary := tokenizeWhereSummary(t, `name = "张三"`)
	if summary != "FIELD=name OP== VALUE=张三" {
		t.Errorf(`双引号字符串 → %s`, summary)
	}
}

// TestTokenizeWhere_MixedQuotes 测试单双引号混合
func TestTokenizeWhere_MixedQuotes(t *testing.T) {
	summary := tokenizeWhereSummary(t, `name = '张三' and city = "北京"`)
	if !strings.Contains(summary, "VALUE=张三") || !strings.Contains(summary, "VALUE=北京") {
		t.Errorf("混合引号 → %s", summary)
	}
}

// TestTokenizeWhere_UnclosedString 测试未闭合字符串错误
func TestTokenizeWhere_UnclosedString(t *testing.T) {
	_, err := tokenizeWherePublic("name = 'hello")
	if err == nil {
		t.Error("未闭合字符串应返回错误")
	}
}

// TestTokenizeWhere_UnexpectedChar 测试未预期字符错误
func TestTokenizeWhere_UnexpectedChar(t *testing.T) {
	_, err := tokenizeWherePublic("name = @")
	if err == nil {
		t.Error("@ 应返回错误")
	}
}

// ---------------------------------------------------------------------------
// 单元测试：parseValue 类型推断
// ---------------------------------------------------------------------------

func TestParseValue_Integer(t *testing.T) {
	v := parseValuePublic("42")
	if v != int64(42) {
		t.Errorf("parseValue('42') = %T(%v), want int64(42)", v, v)
	}
}

func TestParseValue_Float(t *testing.T) {
	v := parseValuePublic("3.14")
	if v != float64(3.14) {
		t.Errorf("parseValue('3.14') = %T(%v), want float64(3.14)", v, v)
	}
}

func TestParseValue_NegativeInt(t *testing.T) {
	v := parseValuePublic("-10")
	if v != int64(-10) {
		t.Errorf("parseValue('-10') = %T(%v), want int64(-10)", v, v)
	}
}

func TestParseValue_String(t *testing.T) {
	v := parseValuePublic("hello")
	if v != "hello" {
		t.Errorf("parseValue('hello') = %T(%v), want string", v, v)
	}
}

// ---------------------------------------------------------------------------
// 单元测试：BuildWhereQuery - 完整查询构建
// ---------------------------------------------------------------------------

// buildWhereQueryPublic 调用包内的 Build，返回序列化后的 JSON 字符串。
// 用于在不连接 ES 的情况下测试查询 DSL 生成是否正确。
func buildWhereQueryPublic(where string) (string, error) {
	pq, err := sharkeswhere.Build(where)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(pq.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// testQueryIs 断言 buildWhereQuery 生成的 JSON 与预期一致。
func testQueryIs(t *testing.T, where, want string) {
	t.Helper()
	got, err := buildWhereQueryPublic(where)
	if err != nil {
		t.Fatalf("Build(%q) error: %v", where, err)
	}
	if got != want {
		t.Errorf("\nWHERE:  %s\nGOT:    %s\nWANT:   %s", where, got, want)
	}
}

// ---------------------------------------------------------------------------
// 简单比较
// ---------------------------------------------------------------------------

func TestBuildWhere_Equals(t *testing.T) {
	testQueryIs(t, "id = 1",
		`{"query":{"term":{"id":1}}}`)
}

func TestBuildWhere_EqualsString(t *testing.T) {
	testQueryIs(t, "name = '张三'",
		`{"query":{"term":{"name":"张三"}}}`)
}

func TestBuildWhere_EqualsStringDoubleQuote(t *testing.T) {
	testQueryIs(t, `name = "张三"`,
		`{"query":{"term":{"name":"张三"}}}`)
}

func TestBuildWhere_NotEquals(t *testing.T) {
	testQueryIs(t, "status != 0",
		`{"query":{"bool":{"must_not":{"term":{"status":0}}}}}`)
}

func TestBuildWhere_GreaterThan(t *testing.T) {
	testQueryIs(t, "age > 18",
		`{"query":{"range":{"age":{"gt":18}}}}`)
}

func TestBuildWhere_GreaterEqual(t *testing.T) {
	testQueryIs(t, "age >= 18",
		`{"query":{"range":{"age":{"gte":18}}}}`)
}

func TestBuildWhere_LessThan(t *testing.T) {
	testQueryIs(t, "age < 60",
		`{"query":{"range":{"age":{"lt":60}}}}`)
}

func TestBuildWhere_LessEqual(t *testing.T) {
	testQueryIs(t, "age <= 60",
		`{"query":{"range":{"age":{"lte":60}}}}`)
}

// ---------------------------------------------------------------------------
// Like 查询（映射为 match）
// ---------------------------------------------------------------------------

func TestBuildWhere_Like(t *testing.T) {
	testQueryIs(t, "name like '张三'",
		`{"query":{"match":{"name":"张三"}}}`)
}

// ---------------------------------------------------------------------------
// IN 查询
// ---------------------------------------------------------------------------

func TestBuildWhere_In(t *testing.T) {
	testQueryIs(t, "status in (1,2,3)",
		`{"query":{"terms":{"status":[1,2,3]}}}`)
}

func TestBuildWhere_InStrings(t *testing.T) {
	testQueryIs(t, "city in ('北京','上海','深圳')",
		`{"query":{"terms":{"city":["北京","上海","深圳"]}}}`)
}

// ---------------------------------------------------------------------------
// AND 组合
// ---------------------------------------------------------------------------

func TestBuildWhere_MultiAND(t *testing.T) {
	testQueryIs(t, "status = 1 and age >= 18",
		`{"query":{"bool":{"must":[{"term":{"status":1}},{"range":{"age":{"gte":18}}}]}}}`)
}

func TestBuildWhere_TripleAND(t *testing.T) {
	testQueryIs(t, "status = 1 and age >= 18 and city = '上海'",
		`{"query":{"bool":{"must":[{"term":{"status":1}},{"range":{"age":{"gte":18}}},{"term":{"city":"上海"}}]}}}`)
}

// ---------------------------------------------------------------------------
// OR 组合
// ---------------------------------------------------------------------------

func TestBuildWhere_SimpleOR(t *testing.T) {
	testQueryIs(t, "id = 1 or id = 2",
		`{"query":{"bool":{"should":[{"term":{"id":1}},{"term":{"id":2}}]}}}`)
}

func TestBuildWhere_TripleOR(t *testing.T) {
	testQueryIs(t, "status = 1 or status = 2 or status = 3",
		`{"query":{"bool":{"should":[{"term":{"status":1}},{"term":{"status":2}},{"term":{"status":3}}]}}}`)
}

// ---------------------------------------------------------------------------
// AND + OR 组合
// ---------------------------------------------------------------------------

func TestBuildWhere_AND_OR(t *testing.T) {
	// 默认 and 优先级更高：status = 1 AND (age >= 18 OR age <= 60)
	// 等价于 → status = 1 AND age >= 18 OR age <= 60
	// 解析为：(status = 1 AND age >= 18) OR age <= 60
	// 因为 AND 先结合为左值，OR 再与右值结合
	testQueryIs(t, "status = 1 and age >= 18 or age <= 60",
		`{"query":{"bool":{"should":[{"bool":{"must":[{"term":{"status":1}},{"range":{"age":{"gte":18}}}]}},{"range":{"age":{"lte":60}}}]}}}`)
}

// ---------------------------------------------------------------------------
// NOT EQUALS + AND/OR
// ---------------------------------------------------------------------------

func TestBuildWhere_NotEqualsWithAnd(t *testing.T) {
	testQueryIs(t, "status != -1 and age >= 18",
		`{"query":{"bool":{"must":[{"bool":{"must_not":{"term":{"status":-1}}}},{"range":{"age":{"gte":18}}}]}}}`)
}

func TestBuildWhere_NotEqualsWithOr(t *testing.T) {
	testQueryIs(t, "status = 1 or status != 0",
		`{"query":{"bool":{"should":[{"term":{"status":1}},{"bool":{"must_not":{"term":{"status":0}}}}]}}}`)
}

// ---------------------------------------------------------------------------
// 括号分组
// ---------------------------------------------------------------------------

func TestBuildWhere_ParenGroup(t *testing.T) {
	testQueryIs(t, "(id = 1 or id = 2) and status = 1",
		`{"query":{"bool":{"must":[{"bool":{"should":[{"term":{"id":1}},{"term":{"id":2}}]}},{"term":{"status":1}}]}}}`)
}

func TestBuildWhere_ParenAndOr(t *testing.T) {
	// (status = 1 OR status = 2) AND age >= 18
	testQueryIs(t, "(status = 1 or status = 2) and age >= 18",
		`{"query":{"bool":{"must":[{"bool":{"should":[{"term":{"status":1}},{"term":{"status":2}}]}},{"range":{"age":{"gte":18}}}]}}}`)
}

// ---------------------------------------------------------------------------
// 复杂嵌套括号
// ---------------------------------------------------------------------------

func TestBuildWhere_DeepNestedParen(t *testing.T) {
	// (((id = 1)))
	testQueryIs(t, "(((id = 1)))",
		`{"query":{"term":{"id":1}}}`)
}

func TestBuildWhere_NestedANDInParen(t *testing.T) {
	// (id = 1 and name like '张') or status = 2
	testQueryIs(t, "(id = 1 and name like '张') or status = 2",
		`{"query":{"bool":{"should":[{"bool":{"must":[{"term":{"id":1}},{"match":{"name":"张"}}]}},{"term":{"status":2}}]}}}`)
}

// ---------------------------------------------------------------------------
// 多层级复杂查询
// ---------------------------------------------------------------------------

func TestBuildWhere_MultiLevelComplex(t *testing.T) {
	// (age >= 18 and age <= 60) and (status = 1 or status = 2) and name like '张'
	// 注意：mergeBoolMust 会自动扁平化已有的 bool.must，所以内层括号组的 must 子句被提升到外层
	testQueryIs(t, "(age >= 18 and age <= 60) and (status = 1 or status = 2) and name like '张'",
		`{"query":{"bool":{"must":[{"range":{"age":{"gte":18}}},{"range":{"age":{"lte":60}}},{"bool":{"should":[{"term":{"status":1}},{"term":{"status":2}}]}},{"match":{"name":"张"}}]}}}`)
}

func TestBuildWhere_ComplexOrWithParen(t *testing.T) {
	// (name like '张三' and age >= 18) or (name like '李四' and age <= 24)
	testQueryIs(t, "(name like '张三' and age >= 18) or (name like '李四' and age <= 24)",
		`{"query":{"bool":{"should":[{"bool":{"must":[{"match":{"name":"张三"}},{"range":{"age":{"gte":18}}}]}},{"bool":{"must":[{"match":{"name":"李四"}},{"range":{"age":{"lte":24}}}]}}]}}}`)
}

// ---------------------------------------------------------------------------
// 范围 + IN 混合
// ---------------------------------------------------------------------------

func TestBuildWhere_RangeAndIn(t *testing.T) {
	testQueryIs(t, "age >= 18 and age < 60 and status in (1,2,3)",
		`{"query":{"bool":{"must":[{"range":{"age":{"gte":18}}},{"range":{"age":{"lt":60}}},{"terms":{"status":[1,2,3]}}]}}}`)
}

// ---------------------------------------------------------------------------
// 单单词 LIKE（match）
// ---------------------------------------------------------------------------

func TestBuildWhere_LikeAndRange(t *testing.T) {
	testQueryIs(t, "name like '张三' and age > 18",
		`{"query":{"bool":{"must":[{"match":{"name":"张三"}},{"range":{"age":{"gt":18}}}]}}}`)
}

// ---------------------------------------------------------------------------
// limit / offset 语法测试
// ---------------------------------------------------------------------------

func TestBuildWhere_Limit(t *testing.T) {
	testQueryIs(t, "status = 1 limit 10",
		`{"query":{"term":{"status":1}},"size":10}`)
}

func TestBuildWhere_LimitOffset(t *testing.T) {
	testQueryIs(t, "status = 1 limit 5 offset 10",
		`{"from":10,"query":{"term":{"status":1}},"size":5}`)
}

func TestBuildWhere_OffsetWithoutLimit(t *testing.T) {
	testQueryIs(t, "age >= 18 offset 20",
		`{"from":20,"query":{"range":{"age":{"gte":18}}}}`)
}

func TestBuildWhere_ComplexWithLimit(t *testing.T) {
	testQueryIs(t, "status = 1 and age >= 18 limit 3",
		`{"query":{"bool":{"must":[{"term":{"status":1}},{"range":{"age":{"gte":18}}}]}},"size":3}`)
}

func TestBuildWhere_ComplexWithLimitOffset(t *testing.T) {
	testQueryIs(t, "(city = '北京' or city = '深圳') and status = 1 limit 2 offset 1",
		`{"from":1,"query":{"bool":{"must":[{"bool":{"should":[{"term":{"city":"北京"}},{"term":{"city":"深圳"}}]}},{"term":{"status":1}}]}},"size":2}`)
}

func TestBuildWhere_LimitZeroIgnored(t *testing.T) {
	// limit 0 不注入 size
	testQueryIs(t, "status = 1 limit 0",
		`{"query":{"term":{"status":1}}}`)
}

// ---------------------------------------------------------------------------
// order by 语法测试
// ---------------------------------------------------------------------------

func TestBuildWhere_OrderBySingleAsc(t *testing.T) {
	testQueryIs(t, "status = 1 order by age asc",
		`{"query":{"term":{"status":1}},"sort":[{"age":"asc"}]}`)
}

func TestBuildWhere_OrderBySingleDesc(t *testing.T) {
	testQueryIs(t, "status = 1 order by age desc",
		`{"query":{"term":{"status":1}},"sort":[{"age":"desc"}]}`)
}

func TestBuildWhere_OrderByDefaultAsc(t *testing.T) {
	// 不指定方向，默认 asc
	testQueryIs(t, "status = 1 order by age",
		`{"query":{"term":{"status":1}},"sort":[{"age":"asc"}]}`)
}

func TestBuildWhere_OrderByMultiFields(t *testing.T) {
	testQueryIs(t, "status = 1 order by age desc, name asc",
		`{"query":{"term":{"status":1}},"sort":[{"age":"desc"},{"name":"asc"}]}`)
}

func TestBuildWhere_OrderByMixedDirs(t *testing.T) {
	// 混合 asc/desc + 默认
	testQueryIs(t, "status = 1 order by age, name desc, score asc",
		`{"query":{"term":{"status":1}},"sort":[{"age":"asc"},{"name":"desc"},{"score":"asc"}]}`)
}

func TestBuildWhere_OrderByWithLimit(t *testing.T) {
	testQueryIs(t, "status = 1 order by age desc limit 10",
		`{"query":{"term":{"status":1}},"size":10,"sort":[{"age":"desc"}]}`)
}

func TestBuildWhere_OrderByWithLimitOffset(t *testing.T) {
	testQueryIs(t, "status = 1 order by age desc limit 5 offset 10",
		`{"from":10,"query":{"term":{"status":1}},"size":5,"sort":[{"age":"desc"}]}`)
}

func TestBuildWhere_OrderByFullSyntax(t *testing.T) {
	testQueryIs(t, "select name,age from users where status = 1 order by age desc limit 5 offset 2",
		`{"_source":["name","age"],"from":2,"query":{"term":{"status":1}},"size":5,"sort":[{"age":"desc"}]}`)
}

// ---------------------------------------------------------------------------
// select / from 语法测试
// ---------------------------------------------------------------------------

func TestBuildWhere_SelectFields(t *testing.T) {
	testQueryIs(t, "select name,age where status = 1",
		`{"_source":["name","age"],"query":{"term":{"status":1}}}`)
}

func TestBuildWhere_SelectStar(t *testing.T) {
	// select * 等同于不指定 _source
	testQueryIs(t, "select * where status = 1",
		`{"query":{"term":{"status":1}}}`)
}

func TestBuildWhere_SelectWithFrom(t *testing.T) {
	testQueryIs(t, "select name from users where status = 1",
		`{"_source":["name"],"query":{"term":{"status":1}}}`)
}

func TestBuildWhere_FromAndSelectAndLimit(t *testing.T) {
	testQueryIs(t, "select name,age from users where status = 1 limit 5",
		`{"_source":["name","age"],"query":{"term":{"status":1}},"size":5}`)
}

func TestBuildWhere_FromWithoutSelect(t *testing.T) {
	testQueryIs(t, "from users where status = 1",
		`{"query":{"term":{"status":1}}}`)
}

func TestBuildWhere_FullSyntax(t *testing.T) {
	testQueryIs(t, "select name,age from users where status = 1 and age >= 18 limit 10 offset 5",
		`{"_source":["name","age"],"from":5,"query":{"bool":{"must":[{"term":{"status":1}},{"range":{"age":{"gte":18}}}]}},"size":10}`)
}

// TestBuildWhere_FromIndex 测试 from 子句是否被正确提取到 ParsedQuery.Index。
func TestBuildWhere_FromIndex(t *testing.T) {
	pq, err := sharkeswhere.Build("select name from users where status = 1")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if pq.Index != "users" {
		t.Errorf("Index = %q, want \"users\"", pq.Index)
	}
	if pq.Body == nil {
		t.Fatal("Body is nil")
	}
}

func TestBuildWhere_NoFromIndex(t *testing.T) {
	pq, err := sharkeswhere.Build("status = 1")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if pq.Index != "" {
		t.Errorf("Index = %q, want \"\"", pq.Index)
	}
}

func TestBuildWhere_SelectOnlyFrom(t *testing.T) {
	// select name from myindex where (无条件) — match_all
	testQueryIs(t, "select name from myindex where ",
		`{"_source":["name"],"query":{"match_all":{}}}`)
}

// ---------------------------------------------------------------------------
// 错误情况
// ---------------------------------------------------------------------------

func TestBuildWhere_EmptyWhere(t *testing.T) {
	_, err := sharkeswhere.Build("")
	if err == nil {
		t.Error("空 WHERE 应返回错误")
	}
}

func TestBuildWhere_WhitespaceOnly(t *testing.T) {
	_, err := sharkeswhere.Build("   ")
	if err == nil {
		t.Error("纯空白 WHERE 应返回错误")
	}
}

func TestBuildWhere_MissingOperator(t *testing.T) {
	_, err := sharkeswhere.Build("id 1")
	if err == nil {
		t.Error("缺少运算符应返回错误")
	}
}

func TestBuildWhere_MissingValue(t *testing.T) {
	_, err := sharkeswhere.Build("id = ")
	if err == nil {
		t.Error("缺少值应返回错误")
	}
}

func TestBuildWhere_UnexpectedAnd(t *testing.T) {
	_, err := sharkeswhere.Build("and id = 1")
	if err == nil {
		t.Error("开头 AND 应返回错误")
	}
}

func TestBuildWhere_UnexpectedOperator(t *testing.T) {
	_, err := sharkeswhere.Build("id <> 1")
	if err == nil {
		t.Error("不支持的运算符 <> 应返回错误")
	}
}

func TestBuildWhere_EmptyIn(t *testing.T) {
	_, err := sharkeswhere.Build("id in ()")
	if err == nil {
		t.Error("空 IN 应返回错误")
	}
}

func TestBuildWhere_UnmatchedParen(t *testing.T) {
	_, err := sharkeswhere.Build("(id = 1")
	if err == nil {
		t.Error("未闭合括号应返回错误")
	}
}

func TestBuildWhere_UnmatchedParenRight(t *testing.T) {
	_, err := sharkeswhere.Build("id = 1)")
	if err == nil {
		t.Error("多余的右括号应返回错误")
	}
}

func TestBuildWhere_TrailingGarbage(t *testing.T) {
	_, err := sharkeswhere.Build("id = 1 xyz")
	if err == nil {
		t.Error("末尾垃圾内容应返回错误")
	}
}

// ---------------------------------------------------------------------------
// 反引号（Backtick）支持测试：MySQL 风格标识符引用
// ---------------------------------------------------------------------------

func TestTokenizeWhere_BacktickField(t *testing.T) {
	summary := tokenizeWhereSummary(t, "`status` = 1")
	if summary != "FIELD=status OP== VALUE=1" {
		t.Errorf("反引号字段 → %s", summary)
	}
}

func TestTokenizeWhere_BacktickMultipleFields(t *testing.T) {
	summary := tokenizeWhereSummary(t, "`settlement_time` >= '2026-06-26' and `status` = 1")
	if !strings.Contains(summary, "FIELD=settlement_time") || !strings.Contains(summary, "FIELD=status") {
		t.Errorf("多反引号字段 → %s", summary)
	}
}

func TestBuildWhere_BacktickFieldInWhere(t *testing.T) {
	testQueryIs(t, "`status` = 1",
		`{"query":{"term":{"status":1}}}`)
}

func TestBuildWhere_BacktickFieldRange(t *testing.T) {
	testQueryIs(t, "`settlement_time` >= '2026-06-26 00:00:00' and `settlement_time` < '2026-06-27 00:00:00'",
		`{"query":{"bool":{"must":[{"range":{"settlement_time":{"gte":"2026-06-26 00:00:00"}}},{"range":{"settlement_time":{"lt":"2026-06-27 00:00:00"}}}]}}}`)
}

func TestBuildWhere_BacktickIndexInFrom(t *testing.T) {
	pq, err := sharkeswhere.Build("from `x_report_special_award` where status = 1")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if pq.Index != "x_report_special_award" {
		t.Errorf("反引号索引名: Index = %q, want \"x_report_special_award\"", pq.Index)
	}
}

func TestBuildWhere_BacktickSelect(t *testing.T) {
	testQueryIs(t, "select `name`,`age` from users where status = 1",
		`{"_source":["name","age"],"query":{"term":{"status":1}}}`)
}

func TestBuildWhere_BacktickOrderBy(t *testing.T) {
	testQueryIs(t, "from users where status = 1 order by `settlement_time` desc, `order_id` desc",
		`{"query":{"term":{"status":1}},"sort":[{"settlement_time":"desc"},{"order_id":"desc"}]}`)
}

func TestBuildWhere_BacktickFullSyntax(t *testing.T) {
	sql := "select * from `x_report_special_award` where `settlement_time` >= '2026-06-26 00:00:00' and `settlement_time` < '2026-06-27 00:00:00' order by `settlement_time` desc, `order_id` desc"
	pq, err := sharkeswhere.Build(sql)
	if err != nil {
		t.Fatalf("完整反引号SQL Build失败: %v", err)
	}
	if pq.Index != "x_report_special_award" {
		t.Errorf("Index = %q, want \"x_report_special_award\"", pq.Index)
	}
	if _, ok := pq.Body["_source"]; ok {
		t.Error("select * 不应产生 _source")
	}
	sortArr, ok := pq.Body["sort"].([]any)
	if !ok || len(sortArr) != 2 {
		t.Fatalf("sort 应为2个元素，实际 %v", pq.Body["sort"])
	}
}

// TestBuildWhere_SumAgg 测试 sum 聚合。
func TestBuildWhere_SumAgg(t *testing.T) {
	testQueryIs(t, "select sum(amount) as total from orders",
		`{"aggs":{"total":{"sum":{"field":"amount"}}},"query":{"match_all":{}},"size":0}`)
}

// TestBuildWhere_AvgAgg 测试 avg 聚合。
func TestBuildWhere_AvgAgg(t *testing.T) {
	testQueryIs(t, "select avg(age) from users",
		`{"aggs":{"avg_age":{"avg":{"field":"age"}}},"query":{"match_all":{}},"size":0}`)
}

// TestBuildWhere_CountAll 测试 count(*) 聚合。
func TestBuildWhere_CountAll(t *testing.T) {
	testQueryIs(t, "select count(*) as cnt from users",
		`{"aggs":{"cnt":{"value_count":{"field":"_id"}}},"query":{"match_all":{}},"size":0}`)
}

// TestBuildWhere_FieldExprAdd 测试字段表达式 +。
func TestBuildWhere_FieldExprAdd(t *testing.T) {
	testQueryIs(t, "select price+tax as total_price from orders",
		`{"query":{"match_all":{}},"script_fields":{"total_price":{"script":{"source":"doc['price'].value+doc['tax'].value"}}},"size":0}`)
}

// TestBuildWhere_FieldExprMinus 测试字段表达式 -。
func TestBuildWhere_FieldExprMinus(t *testing.T) {
	testQueryIs(t, "select price-discount as final_price from orders",
		`{"query":{"match_all":{}},"script_fields":{"final_price":{"script":{"source":"doc['price'].value-doc['discount'].value"}}},"size":0}`)
}

// TestBuildWhere_FieldExprMul 测试字段表达式 *。
func TestBuildWhere_FieldExprMul(t *testing.T) {
	testQueryIs(t, "select price*quantity as total from orders",
		`{"query":{"match_all":{}},"script_fields":{"total":{"script":{"source":"doc['price'].value*doc['quantity'].value"}}},"size":0}`)
}

// TestBuildWhere_FieldExprDiv 测试字段表达式 /。
func TestBuildWhere_FieldExprDiv(t *testing.T) {
	testQueryIs(t, "select amount/count as avg_price from orders",
		`{"query":{"match_all":{}},"script_fields":{"avg_price":{"script":{"source":"doc['amount'].value/doc['count'].value"}}},"size":0}`)
}

// TestBuildWhere_AggExprSumPlusSum 测试聚合表达式 sum(a)+sum(b)。
func TestBuildWhere_AggExprSumPlusSum(t *testing.T) {
	got, err := buildWhereQueryPublic("select sum(a) + sum(b) as total from test")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if !strings.Contains(got, "bucket_script") {
		t.Errorf("应包含 bucket_script: %s", got)
	}
}

// TestBuildWhere_AggExprSumMinusSum 测试聚合表达式 sum(a)-sum(b)。
func TestBuildWhere_AggExprSumMinusSum(t *testing.T) {
	got, err := buildWhereQueryPublic("select sum(a) - sum(b) as diff from test")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if !strings.Contains(got, "bucket_script") {
		t.Errorf("应包含 bucket_script: %s", got)
	}
	if !strings.Contains(got, `"diff"`) {
		t.Errorf("应包含 diff: %s", got)
	}
}

// TestBuildWhere_AggExprSumMinusSumParen 测试括号包裹的聚合表达式 (sum(a)-sum(b)) as c。
// 这是用户报告的 BUG：(sum(amount) - sum(winlost_amount)) as x 被错误归类导致 x 丢失。
func TestBuildWhere_AggExprSumMinusSumParen(t *testing.T) {
	got, err := buildWhereQueryPublic("select (sum(a) - sum(b)) as c from test")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if !strings.Contains(got, "bucket_script") {
		t.Errorf("应包含 bucket_script: %s", got)
	}
	if !strings.Contains(got, `"c"`) {
		t.Errorf("应包含 alias c: %s", got)
	}
}

// TestBuildWhere_AggExprMixedWithParen 模拟用户真实场景：混合纯聚合 + 括号聚合表达式。
// SELECT count(*) as count, sum(amount) as amount, sum(winlost_amount) as winlost_amount, (sum(amount) - sum(winlost_amount)) as x FROM `x_user`
func TestBuildWhere_AggExprMixedWithParen(t *testing.T) {
	sql := `select count(*) as count, sum(amount) as amount, sum(winlost_amount) as winlost_amount, (sum(amount) - sum(winlost_amount)) as x from x_user`
	got, err := buildWhereQueryPublic(sql)
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	// x 必须存在
	if !strings.Contains(got, `"x"`) {
		t.Errorf("应包含 alias x: %s", got)
	}
	// 三个独立聚合也必须存在
	if !strings.Contains(got, `"amount"`) {
		t.Errorf("应包含 amount: %s", got)
	}
	if !strings.Contains(got, `"winlost_amount"`) {
		t.Errorf("应包含 winlost_amount: %s", got)
	}
	if !strings.Contains(got, `"count"`) {
		t.Errorf("应包含 count: %s", got)
	}
	// count 应是 value_count
	if !strings.Contains(got, "value_count") {
		t.Errorf("应包含 value_count: %s", got)
	}
}

// TestBuildWhere_AggExprMultiParenNoSpaceAs 测试多层括号 + )as 无空格格式。
// (((sum(amount) )-( sum(winlost_amount))) )as x
func TestBuildWhere_AggExprMultiParenNoSpaceAs(t *testing.T) {
	got, err := buildWhereQueryPublic("select (((sum(a) )-( sum(b))) )as c from test")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if !strings.Contains(got, "bucket_script") {
		t.Errorf("应包含 bucket_script: %s", got)
	}
	if !strings.Contains(got, `"c"`) {
		t.Errorf("应包含 alias c: %s", got)
	}
}

// TestBuildWhere_AggExprMultiParenSpaceAs 测试多层括号 + ) as 有空格格式。
// (((sum(amount) )-( sum(winlost_amount))) ) as x
func TestBuildWhere_AggExprMultiParenSpaceAs(t *testing.T) {
	got, err := buildWhereQueryPublic("select (((sum(a) )-( sum(b))) ) as c from test")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if !strings.Contains(got, "bucket_script") {
		t.Errorf("应包含 bucket_script: %s", got)
	}
	if !strings.Contains(got, `"c"`) {
		t.Errorf("应包含 alias c: %s", got)
	}
}

// ---------------------------------------------------------------------------
// 集成测试：真实 ES 集群
// ---------------------------------------------------------------------------

// TestElasticQueryWithWhere_Integration 集成测试 QueryWithWhere 在真实 ES 集群上执行。
func TestElasticQueryWithWhere_Integration(t *testing.T) {
	cfg := loadElasticConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	es, err := sharkelastic.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 Elasticsearch 失败: %v", err)
	}

	indexName := "test_shark_qww"

	// 1. 创建索引
	err = es.CreateIndex(ctx, indexName, 1,
		sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
		sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
		sharkelastic.FieldMapping{Name: "status", Type: sharkelastic.MappingTypeInteger},
		sharkelastic.FieldMapping{Name: "city", Type: sharkelastic.MappingTypeKeyword},
	)
	if err != nil {
		t.Fatalf("CreateIndex 失败: %v", err)
	}
	defer es.Client.Indices.Delete([]string{indexName})

	// 2. 插入测试数据
	err = es.Insert(ctx, indexName, "user_id",
		map[string]any{"user_id": "1", "name": "张三", "age": 25, "status": 1, "city": "北京"},
		map[string]any{"user_id": "2", "name": "李四", "age": 30, "status": 1, "city": "上海"},
		map[string]any{"user_id": "3", "name": "王五", "age": 17, "status": 0, "city": "北京"},
		map[string]any{"user_id": "4", "name": "赵六", "age": 45, "status": 1, "city": "深圳"},
		map[string]any{"user_id": "5", "name": "孙七", "age": 22, "status": 2, "city": "上海"},
	)
	if err != nil {
		t.Fatalf("Insert 失败: %v", err)
	}

	// 刷新索引使文档可搜索
	es.Client.Indices.Refresh(es.Client.Indices.Refresh.WithIndex(indexName))

	// 3. 执行各种查询测试
	t.Run("TermAndRange", func(t *testing.T) {
		resp, err := es.SqlRaw(ctx, "from "+indexName+" where status = 1 and age >= 18")
		if err != nil {
			t.Fatalf("SqlRaw 失败: %v", err)
		}
		total := extractTotalHits(t, resp)
		if total != 3 {
			t.Errorf("status=1 AND age>=18: 期望 3 条，实际 %d", total)
		}
		t.Logf("status=1 AND age>=18: total=%d, resp=%s", total, string(resp))
	})

	t.Run("Or", func(t *testing.T) {
		resp, err := es.SqlRaw(ctx, "from "+indexName+" where age < 18 or age > 40")
		if err != nil {
			t.Fatalf("SqlRaw 失败: %v", err)
		}
		total := extractTotalHits(t, resp)
		if total != 2 { // 王五(17) + 赵六(45)
			t.Errorf("age<18 OR age>40: 期望 2 条，实际 %d", total)
		}
		t.Logf("age<18 OR age>40: total=%d", total)
	})

	t.Run("In", func(t *testing.T) {
		resp, err := es.SqlRaw(ctx, "from "+indexName+" where city in ('北京','上海')")
		if err != nil {
			t.Fatalf("SqlRaw 失败: %v", err)
		}
		total := extractTotalHits(t, resp)
		if total != 4 { // 张三, 李四, 王五, 孙七
			t.Errorf("city IN (北京,上海): 期望 4 条，实际 %d", total)
		}
		t.Logf("city IN (北京,上海): total=%d", total)
	})

	t.Run("NotEquals", func(t *testing.T) {
		resp, err := es.SqlRaw(ctx, "from "+indexName+" where status != 0")
		if err != nil {
			t.Fatalf("SqlRaw 失败: %v", err)
		}
		total := extractTotalHits(t, resp)
		if total != 4 { // 张三, 李四, 赵六, 孙七
			t.Errorf("status!=0: 期望 4 条，实际 %d", total)
		}
		t.Logf("status!=0: total=%d", total)
	})

	t.Run("Complex", func(t *testing.T) {
		resp, err := es.SqlRaw(ctx, "from "+indexName+" where (city = '北京' or city = '深圳') and status = 1 and age >= 18")
		if err != nil {
			t.Fatalf("SqlRaw 失败: %v", err)
		}
		total := extractTotalHits(t, resp)
		if total != 2 { // 张三(北京/25/1) + 赵六(深圳/45/1)
			t.Errorf("复杂查询: 期望 2 条，实际 %d", total)
		}
		t.Logf("复杂查询: total=%d, resp=%s", total, string(resp))
	})

	t.Run("NotEqualsWithOr", func(t *testing.T) {
		resp, err := es.SqlRaw(ctx, "from "+indexName+" where status != 1 or age < 18")
		if err != nil {
			t.Fatalf("SqlRaw 失败: %v", err)
		}
		total := extractTotalHits(t, resp)
		if total != 2 { // 王五(status=0,age=17) + 孙七(status=2)
			t.Errorf("status!=1 OR age<18: 期望 2 条，实际 %d", total)
		}
		t.Logf("status!=1 OR age<18: total=%d", total)
	})

	t.Run("Like", func(t *testing.T) {
		resp, err := es.SqlRaw(ctx, "from "+indexName+" where name like '张'")
		if err != nil {
			t.Fatalf("SqlRaw 失败: %v", err)
		}
		total := extractTotalHits(t, resp)
		if total != 1 { // 张三
			t.Errorf("name like 张: 期望 1 条，实际 %d", total)
		}
		t.Logf("name like 张: total=%d", total)
	})

	// ====== limit / offset 集成测试 ======

	t.Run("Limit", func(t *testing.T) {
		resp, err := es.SqlRaw(ctx, "from "+indexName+" where age >= 18 limit 2")
		if err != nil {
			t.Fatalf("SqlRaw with limit 失败: %v", err)
		}
		total := extractTotalHits(t, resp)
		if total != 4 {
			t.Errorf("age>=18 total: 期望 4，实际 %d", total)
		}
		hitsCount := extractHitsCount(t, resp)
		if hitsCount != 2 {
			t.Errorf("age>=18 limit 2 hits: 期望 2，实际 %d", hitsCount)
		}
		t.Logf("age>=18 limit 2: total=%d, hits=%d", total, hitsCount)
	})

	t.Run("LimitOffset", func(t *testing.T) {
		resp, err := es.SqlRaw(ctx, "from "+indexName+" where age >= 18 limit 10 offset 2")
		if err != nil {
			t.Fatalf("SqlRaw with limit+offset 失败: %v", err)
		}
		total := extractTotalHits(t, resp)
		if total != 4 {
			t.Errorf("age>=18 total: 期望 4，实际 %d", total)
		}
		hitsCount := extractHitsCount(t, resp)
		if hitsCount != 2 {
			t.Errorf("age>=18 offset 2 hits: 期望 2，实际 %d", hitsCount)
		}
		t.Logf("age>=18 limit 10 offset 2: total=%d, hits=%d", total, hitsCount)
	})
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// extractTotalHits 从 ES 搜索响应中提取 hits.total.value。
func extractTotalHits(t *testing.T, resp []byte) int {
	t.Helper()
	var result struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("解析响应 JSON 失败: %v\nresp=%s", err, string(resp))
	}
	return result.Hits.Total.Value
}

// extractHitsCount 从 ES 搜索响应中提取实际返回的 hits 数量。
func extractHitsCount(t *testing.T, resp []byte) int {
	t.Helper()
	var result struct {
		Hits struct {
			Hits []any `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("解析响应 JSON 失败: %v\nresp=%s", err, string(resp))
	}
	return len(result.Hits.Hits)
}

// ---------------------------------------------------------------------------
// 暴露包内私有函数用于测试
// ---------------------------------------------------------------------------

func tokenizeWherePublic(input string) ([]sharkeswhere.Token, error) {
	return sharkeswhere.Tokenize(input)
}

func parseValuePublic(s string) any {
	return sharkeswhere.ParseValue(s)
}

// ---------------------------------------------------------------------------
// 集成测试：Find（类似 GORM Find，反序列化到结构体切片）
// ---------------------------------------------------------------------------

// esFindUser 测试 Find 用的用户结构体
type esFindUser struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Age    int    `json:"age"`
	Status int    `json:"status"`
	City   string `json:"city"`
}

// TestElasticFind_Integration 集成测试 Find 方法。
func TestElasticFind_Integration(t *testing.T) {
	cfg := loadElasticConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	es, err := sharkelastic.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 Elasticsearch 失败: %v", err)
	}

	indexName := "test_shark_find"

	// 1. 创建索引
	err = es.CreateIndex(ctx, indexName, 1,
		sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
		sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
		sharkelastic.FieldMapping{Name: "status", Type: sharkelastic.MappingTypeInteger},
		sharkelastic.FieldMapping{Name: "city", Type: sharkelastic.MappingTypeKeyword},
	)
	if err != nil {
		t.Fatalf("CreateIndex 失败: %v", err)
	}
	defer es.Client.Indices.Delete([]string{indexName})

	// 2. 插入测试数据
	err = es.Insert(ctx, indexName, "user_id",
		map[string]any{"user_id": "1", "name": "张三", "age": 25, "status": 1, "city": "北京"},
		map[string]any{"user_id": "2", "name": "李四", "age": 30, "status": 1, "city": "上海"},
		map[string]any{"user_id": "3", "name": "王五", "age": 17, "status": 0, "city": "北京"},
		map[string]any{"user_id": "4", "name": "赵六", "age": 45, "status": 1, "city": "深圳"},
		map[string]any{"user_id": "5", "name": "孙七", "age": 22, "status": 2, "city": "上海"},
	)
	if err != nil {
		t.Fatalf("Insert 失败: %v", err)
	}

	es.Client.Indices.Refresh(es.Client.Indices.Refresh.WithIndex(indexName))

	// ====== Find 测试 ======

	t.Run("FindByStatus", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "from "+indexName+" where status = 1", &users)
		if err != nil {
			t.Fatalf("Find 失败: %v", err)
		}
		if len(users) != 3 {
			t.Errorf("status=1: 期望 3 条，实际 %d", len(users))
		}
		for _, u := range users {
			if u.Status != 1 {
				t.Errorf("status=1 查询混入了 status=%d: %+v", u.Status, u)
			}
		}
		t.Logf("Find status=1: %d 条", len(users))
	})

	t.Run("FindByAgeRange", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "from "+indexName+" where age >= 18 and age <= 30", &users)
		if err != nil {
			t.Fatalf("Find 失败: %v", err)
		}
		// 张三(25) + 李四(30) + 孙七(22) = 3
		if len(users) != 3 {
			t.Errorf("age [18,30]: 期望 3 条，实际 %d", len(users))
		}
		for _, u := range users {
			if u.Age < 18 || u.Age > 30 {
				t.Errorf("age 范围外: %+v", u)
			}
		}
		t.Logf("Find age[18,30]: %d 条", len(users))
	})

	t.Run("FindByComplexWhere", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "from "+indexName+" where (city = '北京' or city = '深圳') and status = 1 and age >= 18", &users)
		if err != nil {
			t.Fatalf("Find 失败: %v", err)
		}
		// 张三(北京/25/1) + 赵六(深圳/45/1) = 2
		if len(users) != 2 {
			t.Errorf("复杂查询: 期望 2 条，实际 %d", len(users))
		}
		t.Logf("Find 复杂查询: %d 条", len(users))
	})

	t.Run("FindWithNotEquals", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "from "+indexName+" where status != 0", &users)
		if err != nil {
			t.Fatalf("Find 失败: %v", err)
		}
		if len(users) != 4 {
			t.Errorf("status!=0: 期望 4 条，实际 %d", len(users))
		}
		t.Logf("Find status!=0: %d 条", len(users))
	})

	t.Run("FindWithLike", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "from "+indexName+` where name like "张"`, &users)
		if err != nil {
			t.Fatalf("Find 失败: %v", err)
		}
		if len(users) != 1 || users[0].Name != "张三" {
			t.Errorf("name like 张: 期望 1 条(张三)，实际 %d 条", len(users))
		}
		t.Logf("Find name like 张: %+v", users)
	})

	t.Run("FindNoResults", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "from "+indexName+" where age > 100", &users)
		if err != nil {
			t.Fatalf("Find 失败: %v", err)
		}
		if len(users) != 0 {
			t.Errorf("无结果查询: 期望 0 条，实际 %d", len(users))
		}
		t.Log("Find age>100: 0 条(正确)")
	})

	// ====== 错误情况 ======

	t.Run("FindNilResult", func(t *testing.T) {
		err := es.Find(ctx, "from "+indexName+" where status = 1", nil)
		if err == nil {
			t.Error("nil result 应返回错误")
		}
	})

	t.Run("FindEmptySQL", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "", &users)
		if err == nil {
			t.Error("空 SQL 应返回错误")
		}
	})

	t.Run("FindMissingFrom", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "status = 1", &users)
		if err == nil {
			t.Error("缺少 from 应返回错误")
		}
		t.Logf("Find missing from error: %v", err)
	})

	// ====== limit / offset 分页测试 ======

	t.Run("FindWithLimit", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "from "+indexName+" where status = 1 limit 2", &users)
		if err != nil {
			t.Fatalf("Find with limit 失败: %v", err)
		}
		if len(users) != 2 {
			t.Errorf("status=1 limit 2: 期望 2 条，实际 %d", len(users))
		}
		t.Logf("Find status=1 limit 2: %d 条", len(users))
	})

	t.Run("FindWithOffset", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "from "+indexName+" where status = 1 offset 1", &users)
		if err != nil {
			t.Fatalf("Find with offset 失败: %v", err)
		}
		if len(users) != 2 {
			t.Errorf("status=1 offset 1: 期望 2 条，实际 %d", len(users))
		}
		t.Logf("Find status=1 offset 1: %d 条", len(users))
	})

	t.Run("FindWithLimitOffset", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "from "+indexName+" where age >= 18 limit 2 offset 1", &users)
		if err != nil {
			t.Fatalf("Find with limit+offset 失败: %v", err)
		}
		if len(users) != 2 {
			t.Errorf("age>=18 limit 2 offset 1: 期望 2 条，实际 %d", len(users))
		}
		t.Logf("Find age>=18 limit 2 offset 1: %d 条", len(users))
	})

	t.Run("FindWithOffsetExceed", func(t *testing.T) {
		var users []esFindUser
		err := es.Find(ctx, "from "+indexName+" where age > 0 offset 100", &users)
		if err != nil {
			t.Fatalf("Find with offset exceed 失败: %v", err)
		}
		if len(users) != 0 {
			t.Errorf("age>0 offset 100: 期望 0 条,实际 %d", len(users))
		}
		t.Log("Find age>0 offset 100: 0 条(正确)")
	})
}
