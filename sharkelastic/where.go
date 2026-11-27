package sharkelastic

import (
	"fmt"
	"strings"
)

// ParsedQuery 是 SQL 风格查询解析后的结果。
type ParsedQuery struct {
	Index string         // Elasticsearch 索引名，来自 from 子句；缺省为空
	Body  map[string]any // Elasticsearch 查询体
}

// selectItemType 是 select 列表中单项的类别。
type selectItemType int

// select 项类别：普通字段、聚合函数、算术表达式。
const (
	selPlain selectItemType = iota
	selAgg
	selExpr
)

// selectItem 是解析后的 select 列表单项。
type selectItem struct {
	Type      selectItemType
	Alias     string
	RawInput  string
	Field     string
	AggFunc   string
	AggField  string
	ExprParts []aggExprPart
}

// aggExprPart 是算术表达式中引用的一个聚合函数调用，如 sum(price)。
type aggExprPart struct {
	FuncName string
	Field    string
	RefName  string
}

// Build 将 SQL 风格查询解析为 Elasticsearch 查询。
//
// 语法：[select ...] [from index] [where cond] [order by ...] [limit N] [offset N]，
// 各子句均可省略，仅有条件时可直接传入 where 之后的内容。
// select 含聚合函数或算术表达式时 size 置 0 并生成 aggs / script_fields。
// where 支持 = != > >= < <= like in 以及 and / or / 括号；like 映射为 match 全文查询。
func Build(where string) (*ParsedQuery, error) {
	input := strings.TrimSpace(where)
	if input == "" {
		return nil, fmt.Errorf("WHERE 子句不能为空")
	}

	var selectItems []selectItem
	var fromIndex string
	var whereClause string

	remaining := input
	remaining, selectItems = extractSelect(remaining)
	remaining, fromIndex = extractFrom(remaining)

	var hasWhere bool
	remaining, hasWhere = extractWhere(remaining)
	if hasWhere {
		whereClause = remaining
	} else {
		if len(selectItems) > 0 || fromIndex != "" {
			whereClause = remaining
		} else {
			whereClause = remaining
		}
	}

	var sortBody []any
	whereClause, sortBody = extractOrderBy(whereClause)
	var size, esFrom int
	whereClause, size, esFrom = extractLimitOffset(whereClause)

	whereClause = strings.TrimSpace(whereClause)
	var esQuery map[string]any
	if whereClause == "" {
		esQuery = map[string]any{"match_all": map[string]any{}}
	} else {
		tokens, err := tokenize(whereClause)
		if err != nil {
			return nil, fmt.Errorf("词法分析失败: %w", err)
		}
		p := &parser{tokens: tokens, pos: 0}
		esQuery, err = p.parseWhere()
		if err != nil {
			return nil, fmt.Errorf("语法解析失败: %w", err)
		}
		if p.pos < len(p.tokens)-1 {
			return nil, fmt.Errorf("语法解析失败: 第 %d 个 token 附近有未预期的内容 '%s'", p.pos, p.tokens[p.pos].value)
		}
	}

	body := map[string]any{"query": esQuery}

	var plainFields []string
	var hasAgg bool

	for _, item := range selectItems {
		switch item.Type {
		case selAgg:
			hasAgg = true
		case selExpr:
			hasAgg = true
		case selPlain:
			alias := item.Field
			if item.Alias != "" {
				alias = item.Alias
			}
			plainFields = append(plainFields, alias)
		}
	}

	if hasAgg {
		body["size"] = 0
		aggs := buildAggs(selectItems)
		if len(aggs) > 0 {
			body["aggs"] = aggs
		}
		scriptFields := buildScriptFields(selectItems)
		if len(scriptFields) > 0 {
			body["script_fields"] = scriptFields
		}
	} else {
		if size > 0 {
			body["size"] = size
		}
		if esFrom > 0 {
			body["from"] = esFrom
		}
		if len(plainFields) > 0 {
			// 含算术表达式的非聚合查询通过 script_fields 返回，此时不能同时用 _source 过滤，且 size 被置 0
			var sf map[string]any
			for _, item := range selectItems {
				if item.Type == selExpr {
					if sf == nil {
						sf = make(map[string]any)
					}
					alias := item.Alias
					if alias == "" {
						alias = sanitizeAlias(item.RawInput)
					}
					sf[alias] = map[string]any{
						"script": map[string]any{
							"source": fieldExprToPainless(item.RawInput),
						},
					}
				}
			}
			if len(sf) > 0 {
				body["script_fields"] = sf
				body["size"] = 0
			} else {
				body["_source"] = plainFields
			}
		}
	}

	if len(sortBody) > 0 {
		body["sort"] = sortBody
	}

	return &ParsedQuery{Index: fromIndex, Body: body}, nil
}

// buildAggs 构建 Elasticsearch 聚合查询。
// 存在聚合算术表达式时，需要 bucket_script 管道聚合，而它必须挂在某个 bucket 聚合之下，
// 因此所有聚合被包进一个 match_all 的 filters 聚合（all/_all）里。
func buildAggs(items []selectItem) map[string]any {
	aggs := make(map[string]any)
	hasPipeline := false

	for _, item := range items {
		if item.Type == selAgg {
			alias := item.Alias
			if alias == "" {
				alias = item.AggFunc + "_" + item.AggField
			}
			aggs[alias] = buildSingleAgg(item)
		}
	}
	for _, item := range items {
		if item.Type == selExpr && len(item.ExprParts) > 0 {
			hasPipeline = true
			alias := item.Alias
			if alias == "" {
				alias = "expr"
			}
			for _, part := range item.ExprParts {
				partAlias := part.FuncName + "_" + part.Field
				if _, exists := aggs[partAlias]; !exists {
					aggs[partAlias] = buildSingleAgg(selectItem{
						Type: selAgg, Alias: partAlias, AggFunc: part.FuncName, AggField: part.Field,
					})
				}
			}
			bucketsPath := make(map[string]string)
			scriptSource := item.RawInput
			for _, part := range item.ExprParts {
				refName := part.FuncName + "_" + part.Field
				bucketsPath[refName] = refName
				needle := part.FuncName + "(" + part.Field + ")"
				scriptSource = strings.ReplaceAll(scriptSource, needle, "params."+refName)
			}
			aggs[alias] = map[string]any{
				"bucket_script": map[string]any{
					"buckets_path": bucketsPath,
					"script":       map[string]any{"source": scriptSource, "lang": "painless"},
				},
			}
		}
	}

	if hasPipeline {
		return map[string]any{
			"all": map[string]any{
				"filters": map[string]any{
					"filters": map[string]any{
						"_all": map[string]any{"match_all": map[string]any{}},
					},
				},
				"aggs": aggs,
			},
		}
	}
	return aggs
}

// buildSingleAgg 将单个聚合函数转换为 ES 聚合；count(*) 对 _id 计数。
func buildSingleAgg(item selectItem) map[string]any {
	switch item.AggFunc {
	case "sum":
		return map[string]any{"sum": map[string]any{"field": item.AggField}}
	case "avg":
		return map[string]any{"avg": map[string]any{"field": item.AggField}}
	case "count":
		if item.AggField == "" || strings.EqualFold(item.AggField, "*") {
			return map[string]any{"value_count": map[string]any{"field": "_id"}}
		}
		return map[string]any{"value_count": map[string]any{"field": item.AggField}}
	case "min":
		return map[string]any{"min": map[string]any{"field": item.AggField}}
	case "max":
		return map[string]any{"max": map[string]any{"field": item.AggField}}
	default:
		return map[string]any{}
	}
}

// buildScriptFields 为不含聚合函数的算术表达式生成 script_fields。
func buildScriptFields(items []selectItem) map[string]any {
	sf := make(map[string]any)
	for _, item := range items {
		if item.Type == selExpr && len(item.ExprParts) == 0 {
			alias := item.Alias
			if alias == "" {
				alias = sanitizeAlias(item.RawInput)
			}
			sf[alias] = map[string]any{
				"script": map[string]any{"source": fieldExprToPainless(item.RawInput)},
			}
		}
	}
	if len(sf) == 0 {
		return nil
	}
	return sf
}

// fieldExprToPainless 将字段算术表达式转换为 Painless 脚本：标识符转为 doc['field'].value，数字与运算符原样保留。
func fieldExprToPainless(expr string) string {
	var result strings.Builder
	i := 0
	for i < len(expr) {
		c := expr[i]
		if c == ' ' || c == '\t' {
			result.WriteByte(' ')
			i++
		} else if c == '+' || c == '-' || c == '*' || c == '/' || c == '(' || c == ')' {
			result.WriteByte(c)
			i++
		} else if c >= '0' && c <= '9' {
			start := i
			for i < len(expr) && ((expr[i] >= '0' && expr[i] <= '9') || expr[i] == '.') {
				i++
			}
			result.WriteString(expr[start:i])
		} else {
			start := i
			for i < len(expr) && !isExprDelim(expr[i]) {
				i++
			}
			ident := strings.TrimSpace(expr[start:i])
			if ident != "" {
				result.WriteString("doc['")
				result.WriteString(ident)
				result.WriteString("'].value")
			}
		}
	}
	return result.String()
}

// isExprDelim 判断是否为表达式中的分隔字符（空白、运算符、括号）。
func isExprDelim(c byte) bool {
	return c == ' ' || c == '\t' || c == '+' || c == '-' || c == '*' || c == '/' || c == '(' || c == ')'
}

// sanitizeAlias 将表达式转成可用作 script_fields 名称的字符串（运算符替换为 _plus_ 等）。
func sanitizeAlias(expr string) string {
	r := strings.NewReplacer("+", "_plus_", "-", "_minus_", "*", "_mul_", "/", "_div_", " ", "", "(", "", ")", "")
	return r.Replace(expr)
}

// extractSelect 解析开头的 select 列表，返回剩余部分及各项；select * 或无 select 时 items 为空。
// 以 " from " 或 " where " 作为列表结束位置。
func extractSelect(input string) (remaining string, items []selectItem) {
	lower := strings.ToLower(input)
	if !strings.HasPrefix(lower, "select ") && !strings.HasPrefix(lower, "select\t") {
		return input, nil
	}
	rest := input[6:]
	rest = strings.TrimLeft(rest, " \t")
	lowerRest := strings.ToLower(rest)
	endIdx := findKeywordBoundary(lowerRest, " from ")
	if endIdx < 0 {
		endIdx = findKeywordEndBoundary(lowerRest, " from")
	}
	if endIdx < 0 {
		endIdx = findKeywordBoundary(lowerRest, " where ")
	}
	if endIdx < 0 {
		endIdx = findKeywordEndBoundary(lowerRest, " where")
	}
	var fieldsStr string
	if endIdx >= 0 {
		fieldsStr = strings.TrimSpace(rest[:endIdx])
		remaining = rest[endIdx:]
	} else {
		fieldsStr = strings.TrimSpace(rest)
		remaining = ""
	}
	if fieldsStr == "" || fieldsStr == "*" {
		return remaining, nil
	}
	items = parseSelectItems(fieldsStr)
	return remaining, items
}

// parseSelectItems 按顶层逗号（忽略括号内）拆分并解析 select 列表。
func parseSelectItems(raw string) []selectItem {
	var items []selectItem
	parts := splitSelectTopLevel(raw, ',')
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		items = append(items, parseSelectItem(part))
	}
	return items
}

// splitSelectTopLevel 按分隔符拆分字符串，括号内的分隔符不拆分。
func splitSelectTopLevel(s string, sep byte) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// parseSelectItem 解析单个 select 项，依次识别 as 别名、聚合函数、算术表达式，否则视为普通字段。
func parseSelectItem(raw string) selectItem {
	raw = strings.TrimSpace(raw)
	var alias string
	var withoutAlias string
	if idx, sepLen := findAsKeyword(raw); idx >= 0 {
		withoutAlias = strings.TrimSpace(raw[:idx])
		alias = strings.TrimSpace(raw[idx+sepLen:])
		alias = trimBacktick(alias)
	} else {
		withoutAlias = raw
	}
	withoutAlias = strings.TrimSpace(withoutAlias)
	if aggItem, ok := tryParseAgg(withoutAlias, alias); ok {
		return aggItem
	}

	unwrapped := withoutAlias
	for len(unwrapped) > 0 && unwrapped[0] == '(' {
		closeIdx := findMatchingParen(unwrapped, 0)
		if closeIdx == len(unwrapped)-1 {
			unwrapped = strings.TrimSpace(unwrapped[1:closeIdx])
		} else {
			break
		}
	}

	if containsArithOps(unwrapped) {
		exprParts := extractAggExprParts(unwrapped)
		return selectItem{
			Type: selExpr, Alias: alias, RawInput: withoutAlias, ExprParts: exprParts,
		}
	}
	field := trimBacktick(withoutAlias)
	return selectItem{Type: selPlain, Alias: alias, Field: field}
}

// findAsKeyword 返回独立关键字 as（前后不是标识符字符）的位置和长度，未找到返回 -1。
func findAsKeyword(s string) (int, int) {
	lower := strings.ToLower(s)
	for i := 0; i < len(lower); i++ {
		if lower[i] != 'a' {
			continue
		}
		if i+1 >= len(lower) || lower[i+1] != 's' {
			continue
		}
		if i > 0 && isIdentChar(lower[i-1]) {
			continue
		}
		after := i + 2
		if after < len(lower) && isIdentChar(lower[after]) {
			continue
		}
		return i, 2
	}
	return -1, 0
}

// isIdentChar 判断是否为标识符字符（字母、数字、下划线）。
func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// tryParseAgg 识别整项为 sum/avg/count/min/max 单个聚合调用的情况。
func tryParseAgg(raw, alias string) (selectItem, bool) {
	lower := strings.ToLower(raw)
	aggFuncs := []string{"sum", "avg", "count", "min", "max"}
	for _, fn := range aggFuncs {
		prefix := fn + "("
		if strings.HasPrefix(lower, prefix) {
			closeIdx := findMatchingParen(raw, len(fn))
			if closeIdx == len(raw)-1 {
				inner := strings.TrimSpace(raw[len(fn)+1 : closeIdx])
				if fn == "count" && (inner == "*" || strings.ToLower(inner) == "*") {
					return selectItem{Type: selAgg, Alias: alias, AggFunc: "count", AggField: "*"}, true
				}
				inner = trimBacktick(inner)
				if inner != "" {
					return selectItem{Type: selAgg, Alias: alias, AggFunc: fn, AggField: inner}, true
				}
			}
		}
	}
	return selectItem{}, false
}

// containsArithOps 判断是否存在括号之外的 + - * / 运算符，聚合函数内部的运算符不算。
func containsArithOps(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '+', '-', '*', '/':
			if !isInsideParens(s, i) {
				return true
			}
		}
	}
	return false
}

// isInsideParens 判断 pos 位置是否位于括号内。
func isInsideParens(s string, pos int) bool {
	depth := 0
	for i := 0; i < pos; i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth > 0
}

// extractAggExprParts 提取表达式中所有 sum/avg/count/min/max 调用，用于生成对应聚合及 buckets_path。
func extractAggExprParts(expr string) []aggExprPart {
	var parts []aggExprPart
	fns := []string{"sum", "avg", "count", "min", "max"}
	lower := strings.ToLower(expr)
	for _, fn := range fns {
		prefix := fn + "("
		searchStart := 0
		for {
			idx := strings.Index(lower[searchStart:], prefix)
			if idx < 0 {
				break
			}
			absIdx := searchStart + idx
			parenStart := absIdx + len(prefix)
			closeIdx := findMatchingParen(expr, parenStart-1)
			if closeIdx < 0 {
				searchStart = absIdx + 1
				continue
			}
			inner := strings.TrimSpace(expr[parenStart:closeIdx])
			inner = trimBacktick(inner)
			if fn == "count" && (inner == "*" || strings.ToLower(inner) == "*") {
				inner = "*"
			}
			if inner != "" {
				parts = append(parts, aggExprPart{FuncName: fn, Field: inner, RefName: fn + "_" + inner})
			}
			searchStart = closeIdx + 1
		}
	}
	return parts
}

// findMatchingParen 返回与 openIdx 处左括号匹配的右括号位置，找不到返回 -1。
func findMatchingParen(s string, openIdx int) int {
	if openIdx < 0 || openIdx >= len(s) || s[openIdx] != '(' {
		return -1
	}
	depth := 0
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// trimBacktick 去掉首尾成对的反引号。
func trimBacktick(s string) string {
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		return s[1 : len(s)-1]
	}
	return s
}

// findKeywordBoundary 返回关键字（含两侧空格）首次出现的位置，未找到返回 -1。
func findKeywordBoundary(s, keyword string) int {
	idx := strings.Index(s, keyword)
	if idx < 0 {
		return -1
	}
	return idx
}

// findKeywordEndBoundary 判断关键字是否位于字符串末尾（忽略尾部空白），是则返回其起始位置，否则返回 -1。
func findKeywordEndBoundary(s, keyword string) int {
	trimmed := strings.TrimRight(s, " \t")
	if strings.HasSuffix(strings.ToLower(trimmed), keyword) {
		return len(trimmed) - len(keyword)
	}
	return -1
}

// extractFrom 解析开头的 from 子句，返回剩余部分和索引名（可用反引号包裹）。
func extractFrom(input string) (remaining string, indexName string) {
	trimmed := strings.TrimLeft(input, " \t")
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "from ") && !strings.HasPrefix(lower, "from\t") {
		return input, ""
	}
	rest := trimmed[4:]
	rest = strings.TrimLeft(rest, " \t")
	idx := strings.IndexAny(rest, " \t")
	if idx < 0 {
		return "", trimBacktick(rest)
	}
	indexName = trimBacktick(rest[:idx])
	remaining = strings.TrimSpace(rest[idx:])
	return remaining, indexName
}

// extractWhere 去掉开头的 where 关键字，found 表示是否存在该关键字。
func extractWhere(input string) (remaining string, found bool) {
	trimmed := strings.TrimLeft(input, " \t")
	lowerTrimmed := strings.ToLower(trimmed)
	if lowerTrimmed == "where" {
		return "", true
	}
	if strings.HasPrefix(lowerTrimmed, "where ") || strings.HasPrefix(lowerTrimmed, "where\t") {
		skipped := trimmed[5:]
		return strings.TrimLeft(skipped, " \t"), true
	}
	return input, false
}

// extractLimitOffset 从末尾解析 offset 和 limit（先 offset 后 limit），返回去掉后的剩余部分；值为 0 视为未指定。
func extractLimitOffset(input string) (remaining string, size int, from int) {
	lower := strings.ToLower(input)
	offsetIdx := lastKeywordIndex(lower, "offset")
	if offsetIdx >= 0 {
		after := strings.TrimSpace(input[offsetIdx+6:])
		n, ok := parseInt(after)
		if ok {
			if n > 0 {
				from = n
			}
			input = strings.TrimSpace(input[:offsetIdx])
			lower = strings.ToLower(input)
		}
	}
	limitIdx := lastKeywordIndex(lower, "limit")
	if limitIdx >= 0 {
		after := strings.TrimSpace(input[limitIdx+5:])
		n, ok := parseInt(after)
		if ok {
			if n > 0 {
				size = n
			}
			input = strings.TrimSpace(input[:limitIdx])
		}
	}
	return input, size, from
}

// extractOrderBy 解析 order by 列表（方向缺省为 asc），并把其后的 limit/offset 尾部拼回剩余部分。
func extractOrderBy(input string) (remaining string, sortBody []any) {
	lower := strings.ToLower(input)
	idx := lastKeywordIndex(lower, "order by")
	if idx < 0 {
		return input, nil
	}
	after := strings.TrimSpace(input[idx+8:])
	sortEnd := len(after)
	lowerAfter := strings.ToLower(after)
	if limitIdx := lastKeywordIndex(lowerAfter, "limit"); limitIdx >= 0 {
		sortEnd = limitIdx
	} else if offsetIdx := lastKeywordIndex(lowerAfter, "offset"); offsetIdx >= 0 {
		sortEnd = offsetIdx
	}
	fieldsStr := strings.TrimSpace(after[:sortEnd])
	tail := strings.TrimSpace(after[sortEnd:])
	if fieldsStr != "" {
		parts := strings.Split(fieldsStr, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			words := strings.Fields(part)
			if len(words) == 0 {
				continue
			}
			field := trimBacktick(words[0])
			dir := "asc"
			if len(words) >= 2 && strings.ToLower(words[1]) == "desc" {
				dir = "desc"
			}
			sortBody = append(sortBody, map[string]any{field: dir})
		}
	}
	remaining = strings.TrimSpace(input[:idx])
	if tail != "" {
		remaining = remaining + " " + tail
	}
	return remaining, sortBody
}

// lastKeywordIndex 返回关键字最后一次作为独立单词出现的位置，未找到返回 -1。
func lastKeywordIndex(s, keyword string) int {
	idx := strings.LastIndex(s, keyword)
	if idx < 0 {
		return -1
	}
	if idx > 0 && s[idx-1] != ' ' && s[idx-1] != '\t' && s[idx-1] != '\n' && s[idx-1] != '\r' {
		return -1
	}
	end := idx + len(keyword)
	if end < len(s) && s[end] != ' ' && s[end] != '\t' && s[end] != '\n' && s[end] != '\r' {
		return -1
	}
	return idx
}

// parseInt 解析由十进制数字组成的字符串，含其他字符返回 false（空串返回 0, true）。
func parseInt(s string) (int, bool) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// Token 表示查询中的词法单元，主要用于调试和测试。
type Token struct {
	Typ string // token 类型：FIELD、OP、VALUE、LPAREN、RPAREN、COMMA、AND、OR
	Val string // token 值
}

// Tokenize 将 where 条件切分为 token，结果不含结尾的 EOF。
func Tokenize(input string) ([]Token, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	var result []Token
	for _, tok := range tokens {
		if tok.typ == tokEOF {
			break
		}
		result = append(result, Token{Typ: tokTypeName(tok.typ), Val: tok.value})
	}
	return result, nil
}

// ParseValue 将查询字面量转换为 Go 值：能无损还原的整数为 int64，浮点为 float64，其余保持字符串。
func ParseValue(s string) any {
	return parseValue(s)
}

// tokType 是词法单元类型。
type tokType int

const (
	tokEOF tokType = iota
	tokField
	tokOp
	tokValue
	tokLParen
	tokRParen
	tokComma
	tokAnd
	tokOr
)

// token 是内部词法单元。
type token struct {
	typ   tokType
	value string
}

// tokenize 将 where 条件切分为 token，以 tokEOF 结尾。
// 引号字面量为值，反引号内容为字段名；裸单词除 and/or/in/like 外均视为字段名。
func tokenize(input string) ([]token, error) {
	var tokens []token
	runes := []rune(input)
	pos := 0
	size := len(runes)

	skipWhitespace := func() {
		for pos < size && (runes[pos] == ' ' || runes[pos] == '\t' || runes[pos] == '\n' || runes[pos] == '\r') {
			pos++
		}
	}

	readField := func() string {
		start := pos
		for pos < size && (isLetter(runes[pos]) || isDigit(runes[pos]) || runes[pos] == '_' || runes[pos] == '.') {
			pos++
		}
		return string(runes[start:pos])
	}

	readNumber := func() string {
		start := pos
		if pos < size && runes[pos] == '-' {
			pos++
		}
		for pos < size && isDigit(runes[pos]) {
			pos++
		}
		if pos < size && runes[pos] == '.' {
			pos++
			for pos < size && isDigit(runes[pos]) {
				pos++
			}
		}
		return string(runes[start:pos])
	}

	readQuoted := func(quote rune) (string, error) {
		pos++
		var buf []rune
		for pos < size {
			if runes[pos] == '\\' && pos+1 < size && runes[pos+1] == quote {
				buf = append(buf, quote)
				pos += 2
				continue
			}
			if runes[pos] == quote {
				pos++
				return string(buf), nil
			}
			buf = append(buf, runes[pos])
			pos++
		}
		return "", fmt.Errorf("未闭合的字符串字面量")
	}

	for pos < size {
		skipWhitespace()
		if pos >= size {
			break
		}
		c := runes[pos]

		if c == '(' {
			tokens = append(tokens, token{typ: tokLParen, value: "("})
			pos++
			continue
		}
		if c == ')' {
			tokens = append(tokens, token{typ: tokRParen, value: ")"})
			pos++
			continue
		}
		if c == ',' {
			tokens = append(tokens, token{typ: tokComma, value: ","})
			pos++
			continue
		}
		if c == '`' {
			s, err := readQuoted('`')
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{typ: tokField, value: s})
			continue
		}
		if c == '\'' || c == '"' {
			s, err := readQuoted(c)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{typ: tokValue, value: s})
			continue
		}
		if isDigit(c) || (c == '-' && pos+1 < size && isDigit(runes[pos+1])) {
			num := readNumber()
			tokens = append(tokens, token{typ: tokValue, value: num})
			continue
		}

		if c == '!' && pos+1 < size && runes[pos+1] == '=' {
			tokens = append(tokens, token{typ: tokOp, value: "!="})
			pos += 2
			continue
		}
		if c == '>' && pos+1 < size && runes[pos+1] == '=' {
			tokens = append(tokens, token{typ: tokOp, value: ">="})
			pos += 2
			continue
		}
		if c == '<' && pos+1 < size && runes[pos+1] == '=' {
			tokens = append(tokens, token{typ: tokOp, value: "<="})
			pos += 2
			continue
		}
		if c == '=' || c == '>' || c == '<' {
			tokens = append(tokens, token{typ: tokOp, value: string(c)})
			pos++
			continue
		}

		if isLetter(c) || c == '_' {
			word := readField()
			lower := strings.ToLower(word)
			switch lower {
			case "and":
				tokens = append(tokens, token{typ: tokAnd, value: word})
			case "or":
				tokens = append(tokens, token{typ: tokOr, value: word})
			case "in":
				tokens = append(tokens, token{typ: tokOp, value: "in"})
			case "like":
				tokens = append(tokens, token{typ: tokOp, value: "like"})
			default:
				tokens = append(tokens, token{typ: tokField, value: word})
			}
			continue
		}
		return nil, fmt.Errorf("位置 %d: 未预期的字符 '%c'", pos, c)
	}
	tokens = append(tokens, token{typ: tokEOF, value: ""})
	return tokens, nil
}

// isLetter 判断是否为 ASCII 字母。
func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isDigit 判断是否为 ASCII 数字。
func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// tokTypeName 返回 token 类型的可读名称。
func tokTypeName(t tokType) string {
	switch t {
	case tokEOF:
		return "EOF"
	case tokField:
		return "FIELD"
	case tokOp:
		return "OP"
	case tokValue:
		return "VALUE"
	case tokLParen:
		return "LPAREN"
	case tokRParen:
		return "RPAREN"
	case tokComma:
		return "COMMA"
	case tokAnd:
		return "AND"
	case tokOr:
		return "OR"
	default:
		return "UNKNOWN"
	}
}

// parser 是 where 条件的递归下降解析器，优先级：括号 > and > or。
type parser struct {
	tokens []token
	pos    int
}

// cur 返回当前 token，越界时返回 EOF。
func (p *parser) cur() token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return token{typ: tokEOF}
}

func (p *parser) advance() { p.pos++ }

// parseWhere 解析完整的条件表达式。
func (p *parser) parseWhere() (map[string]any, error) {
	return p.parseOr()
}

// parseOr 解析 or 表达式，合并为 bool.should。
func (p *parser) parseOr() (map[string]any, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur().typ == tokOr {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = mergeShould(left, right)
	}
	return left, nil
}

// parseAnd 解析 and 表达式，合并为 bool.must。
func (p *parser) parseAnd() (map[string]any, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.cur().typ == tokAnd {
		p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = mergeMust(left, right)
	}
	return left, nil
}

// parseUnary 解析括号子表达式或单个比较。
func (p *parser) parseUnary() (map[string]any, error) {
	if p.cur().typ == tokLParen {
		p.advance()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.cur().typ != tokRParen {
			return nil, fmt.Errorf("期望 ')'，但得到 '%s'", p.cur().value)
		}
		p.advance()
		return expr, nil
	}
	return p.parseComparison()
}

// parseComparison 解析 "字段 运算符 值" 形式的单个比较，并转换为对应 ES 查询。
func (p *parser) parseComparison() (map[string]any, error) {
	if p.cur().typ != tokField {
		return nil, fmt.Errorf("期望字段名，但得到 '%s'", p.cur().value)
	}
	field := p.cur().value
	p.advance()
	if p.cur().typ != tokOp {
		return nil, fmt.Errorf("期望运算符，但得到 '%s'（字段: %s）", p.cur().value, field)
	}
	op := p.cur().value
	p.advance()
	switch op {
	case "=":
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return termQuery(field, val), nil
	case "!=":
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return mustNotQuery(termQuery(field, val)), nil
	case ">":
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return rangeQuery(field, "gt", val), nil
	case ">=":
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return rangeQuery(field, "gte", val), nil
	case "<":
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return rangeQuery(field, "lt", val), nil
	case "<=":
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return rangeQuery(field, "lte", val), nil
	case "like":
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return matchQuery(field, val), nil
	case "in":
		return p.parseIn(field)
	default:
		return nil, fmt.Errorf("不支持的运算符: '%s'", op)
	}
}

// parseValue 读取一个字面量值。
func (p *parser) parseValue() (any, error) {
	if p.cur().typ != tokValue {
		return nil, fmt.Errorf("期望值，但得到 '%s'", p.cur().value)
	}
	val := parseValue(p.cur().value)
	p.advance()
	return val, nil
}

// parseIn 解析 in (v1, v2, ...) 列表，转换为 terms 查询，列表不能为空。
func (p *parser) parseIn(field string) (map[string]any, error) {
	if p.cur().typ != tokLParen {
		return nil, fmt.Errorf("IN 后面期望 '('，但得到 '%s'", p.cur().value)
	}
	p.advance()
	var values []any
	for {
		if p.cur().typ == tokRParen {
			p.advance()
			if len(values) == 0 {
				return nil, fmt.Errorf("IN 列表不能为空（字段: %s）", field)
			}
			return termsQuery(field, values), nil
		}
		if len(values) > 0 {
			if p.cur().typ != tokComma {
				return nil, fmt.Errorf("IN 列表中期望 ','，但得到 '%s'", p.cur().value)
			}
			p.advance()
		}
		if p.cur().typ != tokValue {
			return nil, fmt.Errorf("IN 列表中期望值，但得到 '%s'", p.cur().value)
		}
		values = append(values, parseValue(p.cur().value))
		p.advance()
	}
}

// parseValue 将字面量转为 Go 值：整数 int64、浮点 float64，不能无损往返的（如 "007"）保持字符串。
func parseValue(s string) any {
	var iv int64
	if n, err := fmt.Sscanf(s, "%d", &iv); err == nil && n == 1 && fmt.Sprintf("%d", iv) == s {
		return iv
	}
	var fv float64
	if n, err := fmt.Sscanf(s, "%f", &fv); err == nil && n == 1 && fmt.Sprintf("%g", fv) == s {
		return fv
	}
	return s
}

// termQuery 构造精确匹配查询。
func termQuery(field string, value any) map[string]any {
	return map[string]any{"term": map[string]any{field: value}}
}

// termsQuery 构造多值匹配查询（in）。
func termsQuery(field string, values []any) map[string]any {
	return map[string]any{"terms": map[string]any{field: values}}
}

// matchQuery 构造全文匹配查询（like）。
func matchQuery(field string, value any) map[string]any {
	return map[string]any{"match": map[string]any{field: value}}
}

// rangeQuery 构造范围查询，op 为 gt/gte/lt/lte。
func rangeQuery(field, op string, value any) map[string]any {
	return map[string]any{"range": map[string]any{field: map[string]any{op: value}}}
}

// mustNotQuery 构造取反查询（!=）。
func mustNotQuery(inner map[string]any) map[string]any {
	return map[string]any{"bool": map[string]any{"must_not": inner}}
}

// mergeMust 用 and 合并两个查询，已是 bool.must 的一侧会被展开，避免嵌套过深。
func mergeMust(left, right map[string]any) map[string]any {
	lc := extractClauses(left, "must")
	rc := extractClauses(right, "must")
	return map[string]any{"bool": map[string]any{"must": append(lc, rc...)}}
}

// mergeShould 用 or 合并两个查询，已是 bool.should 的一侧会被展开，避免嵌套过深。
func mergeShould(left, right map[string]any) map[string]any {
	lc := extractClauses(left, "should")
	rc := extractClauses(right, "should")
	return map[string]any{"bool": map[string]any{"should": append(lc, rc...)}}
}

// extractClauses 返回 q 的 bool.<clause> 子句列表；q 不是该形式时将其自身作为单个子句。
func extractClauses(q map[string]any, clause string) []any {
	if b, ok := q["bool"]; ok {
		if bm, ok := b.(map[string]any); ok {
			if e, ok := bm[clause]; ok {
				if arr, ok := e.([]any); ok {
					return arr
				}
			}
		}
	}
	return []any{q}
}
