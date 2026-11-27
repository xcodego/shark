package redis

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
	"strings"
)

// keyReason 是直接使用字符串字面量或拼接字符串作为 key 时的违规说明。
const keyReason = "禁止直接使用字符串或构造字符串作为 Redis key"

// rawArgument 描述一个命令参数的静态分析结果。
type rawArgument struct {
	// text 是可确定的常量文本（字符串或整数），未知时为空。
	text string
	// built 表示该值由字符串字面量、拼接或 fmt/strings/strconv 等构造而来。
	built bool
}

// rawSequence 描述切片形式的参数列表（如 []any 或 args...）的静态分析结果。
type rawSequence struct {
	// values 记录已知下标上的元素。
	values map[int]rawArgument
	// length 是已知长度，-1 表示长度未知。
	length int
	// prefix 是长度未知时，下标仍然精确可信的前缀长度。
	prefix int
	// cap 是已知容量，-1 表示未知；用于判断 append 后是否仍共享底层数组。
	cap int
	// origin 标识底层数组的创建位置，用于把别名切片的写入同步到同一底层数组。
	origin token.Pos
	// tail 汇总长度未知或下标未知的元素，只要有一个 built 就视为 built。
	tail rawArgument
}

// keyMap 描述 map 参数的分析结果，built 表示任一 map key 为构造出的字符串。
type keyMap struct {
	built bool
}

// keyState 是流分析中某一程序点上各变量的状态，按 types.Object 记录。
type keyState struct {
	built     map[types.Object]bool
	strings   map[types.Object]string
	sequences map[types.Object]*rawSequence
	maps      map[types.Object]*keyMap
}

// builtString 判断 expr 是否是直接字符串或构造出来的字符串（字面量、+ 拼接、
// fmt.Sprint*、strings/strconv 返回值、string 转换，及含此类 Key/Keys/Stream 字段的结构体）。
// 常量标识符不算，这样引用常量定义的 key 不会被报告。
func builtString(expr ast.Expr, info *types.Info, built map[types.Object]bool) bool {
	switch e := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		return e.Kind == token.STRING
	case *ast.Ident:
		return built[info.ObjectOf(e)]
	case *ast.BinaryExpr:
		if t := info.TypeOf(e); e.Op == token.ADD && t != nil {
			basic, ok := t.Underlying().(*types.Basic)
			return ok && basic.Info()&types.IsString != 0
		}
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return builtString(e.X, info, built)
		}
	case *ast.CompositeLit:
		if t := info.TypeOf(e); t != nil {
			if _, ok := t.Underlying().(*types.Struct); ok {
				for _, elt := range e.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					field, ok := kv.Key.(*ast.Ident)
					if ok && (field.Name == "Key" || field.Name == "Keys" || field.Name == "Stream") && builtString(kv.Value, info, built) {
						return true
					}
					if ok && field.Name == "Streams" {
						if lit, ok := ast.Unparen(kv.Value).(*ast.CompositeLit); ok {
							for _, key := range lit.Elts[:len(lit.Elts)/2] {
								if builtString(key, info, built) {
									return true
								}
							}
						} else if builtString(kv.Value, info, built) {
							return true
						}
					}
				}
				return false
			}
			if _, ok := t.Underlying().(*types.Map); ok {
				for _, elt := range e.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok && builtString(kv.Key, info, built) {
						return true
					}
				}
				return false
			}
		}
		for _, elt := range e.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if builtString(kv.Key, info, built) || builtString(kv.Value, info, built) {
					return true
				}
			} else if builtString(elt, info, built) {
				return true
			}
		}
	case *ast.CallExpr:
		if tv, ok := info.Types[e.Fun]; ok && tv.IsType() {
			t := info.TypeOf(e)
			return t != nil && types.Identical(t.Underlying(), types.Typ[types.String])
		}
		if sel, ok := ast.Unparen(e.Fun).(*ast.SelectorExpr); ok {
			fn, _ := info.Uses[sel.Sel].(*types.Func)
			if fn != nil && fn.Pkg() != nil {
				path := fn.Pkg().Path()
				if path == "fmt" && (fn.Name() == "Sprint" || fn.Name() == "Sprintf" || fn.Name() == "Sprintln") {
					return true
				}
				if path == "strings" || path == "strconv" {
					t := info.TypeOf(e)
					return t != nil && types.Identical(t.Underlying(), types.Typ[types.String])
				}
			}
		}
	}
	return false
}

// keyCall 判断 call 是否把 built 字符串作为 key 参数传给受支持的 Redis 客户端，
// 依据方法形参名（key/keys/dest 等）确定哪些参数是 key；原始命令入口则交给 rawKeys。
func keyCall(call *ast.CallExpr, info *types.Info, state *keyState) bool {
	built := state.built
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return false
	}
	selection := info.Selections[sel]
	offset := 0
	if selection != nil && selection.Kind() == types.MethodExpr {
		offset = 1
	}
	path := fn.Pkg().Path()
	switch path {
	case "github.com/go-redis/redis", "github.com/redis/go-redis",
		"github.com/go-redis/redis/v7", "github.com/go-redis/redis/v8",
		"github.com/redis/go-redis/v9", "github.com/redis/rueidis/rueidiscompat":
		if selection != nil && sig.Variadic() &&
			(fn.Name() == "Do" || fn.Name() == "DoRaw" || fn.Name() == "DoRawWriteTo") {
			return rawKeys(call, info, state, offset+sig.Params().Len()-1)
		}
		if selection == nil && strings.HasPrefix(fn.Name(), "New") && strings.HasSuffix(fn.Name(), "Cmd") && sig.Variadic() {
			return rawKeys(call, info, state, offset+sig.Params().Len()-1)
		}
		if selection == nil || fn.Name() == "ClientKillByFilter" {
			return false
		}
		if fn.Name() == "MSet" || fn.Name() == "MSetNX" {
			return alternatingKeys(call, info, state, offset+sig.Params().Len()-1)
		}
		for i := 0; i < sig.Params().Len(); i++ {
			name := strings.ToLower(sig.Params().At(i).Name())
			switch name {
			case "key", "keys", "newkey", "destkey", "source", "destination", "dest", "dst", "stream":
				index := offset + i
				end := index + 1
				if sig.Variadic() && i == sig.Params().Len()-1 {
					end = len(call.Args)
				}
				for ; index < end && index < len(call.Args); index++ {
					if state.argument(call.Args[index], info).built {
						return true
					}
				}
			}
			t := sig.Params().At(i).Type()
			if ptr, ok := t.(*types.Pointer); ok {
				t = ptr.Elem()
			}
			if named, ok := types.Unalias(t).(*types.Named); ok && named.Obj().Pkg() != nil &&
				named.Obj().Pkg().Path() == path {
				if _, ok := named.Underlying().(*types.Struct); ok && offset+i < len(call.Args) &&
					builtString(call.Args[offset+i], info, built) {
					return true
				}
			}
		}
	case "github.com/gomodule/redigo/redis", "github.com/garyburd/redigo/redis":
		index := offset
		switch fn.Name() {
		case "Do", "Send":
			if selection == nil || sig.Params().Len() == 0 || !types.Identical(sig.Params().At(0).Type(), types.Typ[types.String]) {
				return false
			}
		case "DoContext", "DoWithTimeout":
			index++
			if selection == nil {
				index++
			}
		default:
			return false
		}
		return rawKeys(call, info, state, index)
	case "github.com/redis/rueidis/internal/cmds":
		if selection != nil && fn.Name() == "Arbitrary" {
			return rawKeys(call, info, state, offset)
		}
		if selection != nil && (fn.Name() == "Key" || fn.Name() == "Keys") {
			for _, arg := range call.Args[offset:] {
				if state.argument(arg, info).built {
					return true
				}
			}
		}
	}
	return false
}

// alternatingKeys 检查 MSET 这类 key、value 交替排列的参数，只看 key 位置（偶数下标）。
func alternatingKeys(call *ast.CallExpr, info *types.Info, state *keyState, start int) bool {
	if start == len(call.Args)-1 {
		if seq, ok := state.sequence(call.Args[start], info); ok {
			for index, arg := range seq.values {
				if index%2 == 0 && arg.built {
					return true
				}
			}
			return seq.tail.built
		}
		if m := state.mapValue(call.Args[start], info); m != nil {
			return m.built
		}
	}
	for i := start; i < len(call.Args); i += 2 {
		if state.argument(call.Args[i], info).built {
			return true
		}
	}
	return false
}

// argument 分析 expr 作为命令参数时的 built 状态与常量文本，
// 会展开 args[i] 下标访问和切片元素。
func (s *keyState) argument(expr ast.Expr, info *types.Info) rawArgument {
	arg := rawArgument{built: builtString(expr, info, s.built)}
	if seq, ok := s.sequence(expr, info); ok {
		arg.built = seq.tail.built
		for _, element := range seq.values {
			arg.built = arg.built || element.built
		}
	}
	if index, ok := ast.Unparen(expr).(*ast.IndexExpr); ok {
		if seq, ok := s.sequence(index.X, info); ok {
			value := info.Types[index.Index].Value
			if value != nil && value.Kind() == constant.Int {
				i, ok := constant.Int64Val(value)
				if ok && i >= 0 && int64(int(i)) == i {
					arg := seq.values[int(i)]
					arg.built = arg.built || seq.tail.built
					return arg
				}
			}
			for _, element := range seq.values {
				arg.built = arg.built || element.built
			}
			arg.built = arg.built || seq.tail.built
		}
	}
	if value := info.Types[expr].Value; value != nil {
		switch value.Kind() {
		case constant.String:
			arg.text = constant.StringVal(value)
		case constant.Int:
			arg.text = value.ExactString()
		}
	} else if id, ok := ast.Unparen(expr).(*ast.Ident); ok {
		arg.text = s.strings[info.ObjectOf(id)]
	}
	return arg
}

// sequence 把 expr 解析为切片序列：支持变量、make、append 及切片字面量；
// 无法识别时返回 false。
func (s *keyState) sequence(expr ast.Expr, info *types.Info) (*rawSequence, bool) {
	if id, ok := ast.Unparen(expr).(*ast.Ident); ok {
		args, found := s.sequences[info.ObjectOf(id)]
		if found {
			return args, true
		}
		if t := info.TypeOf(id); t != nil {
			if _, ok := t.Underlying().(*types.Slice); ok {
				args = &rawSequence{length: -1, cap: -1, origin: info.ObjectOf(id).Pos(), values: make(map[int]rawArgument)}
				s.sequences[info.ObjectOf(id)] = args
				return args, true
			}
		}
		return nil, false
	}
	if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok {
		id, ok := ast.Unparen(call.Fun).(*ast.Ident)
		if !ok || len(call.Args) == 0 {
			return nil, false
		}
		if info.Uses[id] == types.Universe.Lookup("make") {
			t := info.TypeOf(expr)
			if t == nil {
				return nil, false
			}
			if _, ok := t.Underlying().(*types.Slice); !ok {
				return nil, false
			}
			result := &rawSequence{length: -1, cap: -1, origin: call.Pos(), values: make(map[int]rawArgument)}
			if len(call.Args) > 1 {
				value := info.Types[call.Args[1]].Value
				if value != nil && value.Kind() == constant.Int {
					length, ok := constant.Int64Val(value)
					if ok && length >= 0 && int64(int(length)) == length {
						result.length = int(length)
						result.cap = result.length
					}
				}
			}
			if len(call.Args) > 2 {
				result.cap = -1
				value := info.Types[call.Args[2]].Value
				if value != nil && value.Kind() == constant.Int {
					capacity, ok := constant.Int64Val(value)
					if ok && capacity >= 0 && int64(int(capacity)) == capacity {
						result.cap = int(capacity)
					}
				}
			}
			return result, true
		}
		if info.Uses[id] != types.Universe.Lookup("append") {
			return nil, false
		}
		base, ok := s.sequence(call.Args[0], info)
		if !ok {
			return nil, false
		}
		result := copySequence(base)
		for i, expr := range call.Args[1:] {
			if call.Ellipsis.IsValid() && i == len(call.Args)-2 {
				tail, ok := s.sequence(expr, info)
				if !ok {
					return nil, false
				}
				if result.length < 0 || tail.length < 0 {
					if result.length >= 0 {
						result.prefix = result.length
					}
					result.length = -1
					result.tail.built = result.tail.built || tail.tail.built
					for _, value := range tail.values {
						result.tail.built = result.tail.built || value.built
					}
				} else {
					for index, value := range tail.values {
						result.values[result.length+index] = value
					}
					result.length += tail.length
				}
			} else if result.length < 0 {
				result.tail.built = result.tail.built || s.argument(expr, info).built
			} else {
				result.values[result.length] = s.argument(expr, info)
				result.length++
			}
		}
		if base.cap >= 0 && result.length >= 0 && result.length > base.cap {
			result.origin = call.Pos()
			result.cap = -1
		}
		return result, true
	}
	lit, ok := ast.Unparen(expr).(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	t := info.TypeOf(lit)
	if t == nil {
		return nil, false
	}
	if _, ok := t.Underlying().(*types.Slice); !ok {
		return nil, false
	}
	sequence := &rawSequence{origin: lit.Pos(), values: make(map[int]rawArgument, len(lit.Elts))}
	next := 0
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			value := info.Types[kv.Key].Value
			if value == nil || value.Kind() != constant.Int {
				return nil, false
			}
			index, ok := constant.Int64Val(value)
			if !ok || index < 0 || int64(int(index)) != index || int(index) == int(^uint(0)>>1) {
				return nil, false
			}
			next = int(index)
			elt = kv.Value
		}
		sequence.values[next] = s.argument(elt, info)
		if next == int(^uint(0)>>1) {
			return nil, false
		}
		next++
		if next > sequence.length {
			sequence.length = next
		}
	}
	sequence.cap = sequence.length
	return sequence, true
}

// mapValue 把 expr 解析为 map 状态：支持变量与 map 字面量；无法识别时返回 nil。
func (s *keyState) mapValue(expr ast.Expr, info *types.Info) *keyMap {
	if id, ok := ast.Unparen(expr).(*ast.Ident); ok {
		obj := info.ObjectOf(id)
		if m := s.maps[obj]; m != nil {
			return m
		}
		if t := info.TypeOf(id); t != nil {
			if _, ok := t.Underlying().(*types.Map); ok {
				m := &keyMap{}
				s.maps[obj] = m
				return m
			}
		}
		return nil
	}
	lit, ok := ast.Unparen(expr).(*ast.CompositeLit)
	if !ok || info.TypeOf(lit) == nil {
		return nil
	}
	if _, ok := info.TypeOf(lit).Underlying().(*types.Map); !ok {
		return nil
	}
	m := &keyMap{}
	for _, elt := range lit.Elts {
		if pair, ok := elt.(*ast.KeyValueExpr); ok {
			m.built = m.built || s.argument(pair.Key, info).built
		}
	}
	return m
}

// assign 在重新赋值时先清除 obj 原有状态，再写入新的文本、序列和 map 状态。
func (s *keyState) assign(obj types.Object, arg rawArgument, sequence *rawSequence, m *keyMap) {
	if obj == nil {
		return
	}
	delete(s.strings, obj)
	delete(s.sequences, obj)
	delete(s.maps, obj)
	if arg.text != "" {
		s.strings[obj] = arg.text
	}
	if sequence != nil {
		s.sequences[obj] = sequence
	}
	if m != nil {
		s.maps[obj] = m
	}
}

// rawKeys 检查原始命令调用（Do、Send 等）：先从 commandIndex 处取出常量命令名，
// 再按该命令的 key 位置规则判断对应参数是否为 built 字符串。
func rawKeys(call *ast.CallExpr, info *types.Info, state *keyState, commandIndex int) bool {
	args := make([]rawArgument, 0, len(call.Args))
	var expanded *rawSequence
	for i, expr := range call.Args {
		if call.Ellipsis.IsValid() && i == len(call.Args)-1 {
			var ok bool
			expanded, ok = state.sequence(expr, info)
			if !ok {
				break
			}
		} else {
			args = append(args, state.argument(expr, info))
		}
	}
	at := func(i int) rawArgument {
		if i >= 0 && i < len(args) {
			return args[i]
		}
		if expanded != nil && i >= len(args) {
			index := i - len(args)
			arg := expanded.values[index]
			if expanded.length < 0 && index >= expanded.prefix {
				arg.built = arg.built || expanded.tail.built
			}
			return arg
		}
		return rawArgument{}
	}
	command := strings.ToUpper(at(commandIndex).text)
	start := commandIndex + 1
	keyAt := func(i int) bool { return at(i).built }
	hasKeys := func(start, end, step int) bool {
		if start >= end {
			return false
		}
		isKey := func(index int, arg rawArgument) bool {
			return index >= start && index < end && (index-start)%step == 0 && arg.built
		}
		for i, arg := range args {
			if isKey(i, arg) {
				return true
			}
		}
		if expanded != nil {
			if expanded.tail.built && expanded.prefix <= int(^uint(0)>>1)-len(args) &&
				expanded.prefix+len(args) < end {
				return true
			}
			for i, arg := range expanded.values {
				if i <= int(^uint(0)>>1)-len(args) && isKey(i+len(args), arg) {
					return true
				}
			}
		}
		return false
	}
	end := int(^uint(0) >> 1)
	switch command {
	case "MSET", "MSETNX":
		return hasKeys(start, end, 2)
	case "EVAL", "EVALSHA", "EVAL_RO", "EVALSHA_RO", "FCALL", "FCALL_RO":
		count, err := strconv.Atoi(at(start + 1).text)
		if err != nil {
			return hasKeys(start+2, end, 1)
		}
		if count < 0 {
			return false
		}
		if count < end-(start+2) {
			end = start + 2 + count
		}
		return hasKeys(start+2, end, 1)
	case "GET", "SET", "SETNX", "SETEX", "PSETEX", "GETSET", "GETDEL", "GETEX",
		"APPEND", "STRLEN", "INCR", "INCRBY", "INCRBYFLOAT", "DECR", "DECRBY",
		"HGET", "HSET", "HDEL", "HMGET", "HMSET", "HGETALL", "HEXISTS", "HINCRBY",
		"HSCAN", "HKEYS", "HVALS", "HLEN", "HSTRLEN", "HINCRBYFLOAT", "HSETNX",
		"LPUSH", "RPUSH", "LPOP", "RPOP", "LRANGE", "LLEN", "LINDEX", "LSET",
		"LPUSHX", "RPUSHX", "LINSERT", "LREM", "LTRIM", "LPOS",
		"SADD", "SREM", "SMEMBERS", "SISMEMBER", "SCARD", "SPOP",
		"SSCAN", "SMISMEMBER", "SRANDMEMBER",
		"ZADD", "ZREM", "ZRANGE", "ZSCORE", "ZCARD", "ZINCRBY",
		"ZSCAN", "ZCOUNT", "ZLEXCOUNT", "ZRANK", "ZREVRANK", "ZMSCORE",
		"ZPOPMIN", "ZPOPMAX", "ZREVRANGE", "ZRANGEBYSCORE", "ZREVRANGEBYSCORE",
		"ZREMRANGEBYRANK", "ZREMRANGEBYSCORE", "ZREMRANGEBYLEX",
		"EXPIRE", "PEXPIRE", "TTL", "PTTL", "PERSIST", "TYPE", "DUMP", "RESTORE",
		"EXPIREAT", "PEXPIREAT", "EXPIRETIME", "PEXPIRETIME",
		"GETBIT", "SETBIT", "BITCOUNT", "BITFIELD", "BITFIELD_RO", "BITPOS",
		"GEOADD", "GEODIST", "GEOHASH", "GEOPOS", "GEOSEARCH",
		"XADD", "XRANGE", "XREVRANGE", "XLEN", "XDEL", "XTRIM":
		return keyAt(start)
	case "DEL", "UNLINK", "EXISTS", "MGET", "TOUCH", "WATCH", "RENAME", "RENAMENX",
		"RPOPLPUSH", "LMOVE", "SMOVE", "COPY", "SDIFF", "SINTER", "SUNION",
		"SDIFFSTORE", "SINTERSTORE", "SUNIONSTORE", "PFCOUNT", "PFMERGE":
		// 这些命令除 key 外还带有其他参数，仅前两个参数是 key。
		if command == "RENAME" || command == "RENAMENX" || command == "RPOPLPUSH" ||
			command == "LMOVE" || command == "SMOVE" || command == "COPY" {
			end = start + 2
		}
		return hasKeys(start, end, 1)
	default:
		return false
	}
}
