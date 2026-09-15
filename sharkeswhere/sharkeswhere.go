// Package sharkeswhere 提供将 SQL WHERE 风格字符串解析为 Elasticsearch Query DSL 的能力。
//
// 支持的操作符映射：
//
//	=      -> term query
//	!=     -> bool.must_not + term
//	>      -> range gt
//	>=     -> range gte
//	<      -> range lt
//	<=     -> range lte
//	in     -> terms
//	like   -> match
//	and    -> bool.must (自动扁平化)
//	or     -> bool.should (自动扁平化)
//	()     -> 分组
//
// select 聚合函数：
//
//	sum(field) / avg(field) / count(field) / count(*) / min(field) / max(field)
//	可选 as 别名，例如 sum(amount) as total
//	聚合存在时自动 size=0
//
// select 表达式：
//
//	a + b as c, a - b, a * b, a / b → ES script_fields (Painless)
//
// 完整语法：
//
//	select field1, field2 from indexname where conditions limit N offset N
//
// select   → ES _source 字段过滤（可选）
// from     → 指定索引名（可选，在 where 之前）
// where    → 查询条件
// limit    → ES size（可选）
// offset   → ES from（分页偏移，可选，必须在 limit 之后）
package sharkeswhere

import (
	"fmt"
	"strings"
)

// ParsedQuery 是 Build 的解析结果。
type ParsedQuery struct {
	Index string
	Body  map[string]any
}

// ---------------------------------------------------------------------------
// select item 类型
// ---------------------------------------------------------------------------

type selectItemType int

const (
	selPlain selectItemType = iota
	selAgg
	selExpr
)

type selectItem struct {
	Type      selectItemType
	Alias     string
	RawInput  string
	Field     string
	AggFunc   string
	AggField  string
	ExprParts []aggExprPart
}

type aggExprPart struct {
	FuncName string
	Field    string
	RefName  string
}

// ---------------------------------------------------------------------------
// Build
// ---------------------------------------------------------------------------

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

// buildAggs 构建 ES aggs DSL。
// bucket_script 是 pipeline 聚合，必须嵌套在 bucket 聚合内。
// 当存在 selExpr 时，用一个 filter(match_all) 包裹所有聚合。
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

	// bucket_script 是 pipeline 聚合，必须嵌套在 bucket 聚合内。
	// 使用 filters 聚合（multi-bucket）包裹，兼容所有 ES 版本。
	// 响应路径：aggregations.all.buckets._all.<alias>.value
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

func isExprDelim(c byte) bool {
	return c == ' ' || c == '\t' || c == '+' || c == '-' || c == '*' || c == '/' || c == '(' || c == ')'
}

func sanitizeAlias(expr string) string {
	r := strings.NewReplacer("+", "_plus_", "-", "_minus_", "*", "_mul_", "/", "_div_", " ", "", "(", "", ")", "")
	return r.Replace(expr)
}

// ---------------------------------------------------------------------------
// select 解析
// ---------------------------------------------------------------------------

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

	// 解开最外层括号，支持 (sum(a)-sum(b)) as c 这类包裹形式
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

// findAsKeyword 查找独立关键字 "as" 的位置，返回 (idx, sepLen)。
// "as" 被视为关键字当且仅当它的前后都是非标识符字符（或边界）。
// 标识符字符包括：字母、数字、下划线。
func findAsKeyword(s string) (int, int) {
	lower := strings.ToLower(s)
	for i := 0; i < len(lower); i++ {
		// 找 "as" 子串
		if lower[i] != 'a' {
			continue
		}
		if i+1 >= len(lower) || lower[i+1] != 's' {
			continue
		}
		// 前面必须是边界或非标识符字符（如空格、Tab、) 等）
		if i > 0 && isIdentChar(lower[i-1]) {
			continue
		}
		// 后面必须是边界或非标识符字符
		after := i + 2
		if after < len(lower) && isIdentChar(lower[after]) {
			continue
		}
		return i, 2
	}
	return -1, 0
}

// isIdentChar 判断字符是否属于标识符（字母/数字/下划线）。
func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

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

func trimBacktick(s string) string {
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		return s[1 : len(s)-1]
	}
	return s
}

func findKeywordBoundary(s, keyword string) int {
	idx := strings.Index(s, keyword)
	if idx < 0 {
		return -1
	}
	return idx
}

func findKeywordEndBoundary(s, keyword string) int {
	trimmed := strings.TrimRight(s, " \t")
	if strings.HasSuffix(strings.ToLower(trimmed), keyword) {
		return len(trimmed) - len(keyword)
	}
	return -1
}

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

// ---------------------------------------------------------------------------
// 公开类型
// ---------------------------------------------------------------------------

type Token struct {
	Typ string
	Val string
}

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

func ParseValue(s string) any {
	return parseValue(s)
}

// ---------------------------------------------------------------------------
// 词法分析
// ---------------------------------------------------------------------------

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

type token struct {
	typ   tokType
	value string
}

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

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

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

// ---------------------------------------------------------------------------
// 语法解析
// ---------------------------------------------------------------------------

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) cur() token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return token{typ: tokEOF}
}

func (p *parser) advance() { p.pos++ }

func (p *parser) parseWhere() (map[string]any, error) {
	return p.parseOr()
}

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

func (p *parser) parseValue() (any, error) {
	if p.cur().typ != tokValue {
		return nil, fmt.Errorf("期望值，但得到 '%s'", p.cur().value)
	}
	val := parseValue(p.cur().value)
	p.advance()
	return val, nil
}

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

func termQuery(field string, value any) map[string]any {
	return map[string]any{"term": map[string]any{field: value}}
}

func termsQuery(field string, values []any) map[string]any {
	return map[string]any{"terms": map[string]any{field: values}}
}

func matchQuery(field string, value any) map[string]any {
	return map[string]any{"match": map[string]any{field: value}}
}

func rangeQuery(field, op string, value any) map[string]any {
	return map[string]any{"range": map[string]any{field: map[string]any{op: value}}}
}

func mustNotQuery(inner map[string]any) map[string]any {
	return map[string]any{"bool": map[string]any{"must_not": inner}}
}

func mergeMust(left, right map[string]any) map[string]any {
	lc := extractClauses(left, "must")
	rc := extractClauses(right, "must")
	return map[string]any{"bool": map[string]any{"must": append(lc, rc...)}}
}

func mergeShould(left, right map[string]any) map[string]any {
	lc := extractClauses(left, "should")
	rc := extractClauses(right, "should")
	return map[string]any{"bool": map[string]any{"should": append(lc, rc...)}}
}

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
