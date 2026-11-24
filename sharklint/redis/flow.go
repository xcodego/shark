package redis

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"sort"

	"golang.org/x/tools/go/cfg"
)

func newKeyState() *keyState {
	return &keyState{
		built: make(map[types.Object]bool), strings: make(map[types.Object]string),
		sequences: make(map[types.Object]*rawSequence), maps: make(map[types.Object]*keyMap),
	}
}

func (s *keyState) clone() *keyState {
	copy := newKeyState()
	for obj, value := range s.built {
		copy.built[obj] = value
	}
	for obj, value := range s.strings {
		copy.strings[obj] = value
	}
	sequences := make(map[*rawSequence]*rawSequence)
	for obj, sequence := range s.sequences {
		if sequences[sequence] == nil {
			sequences[sequence] = copySequence(sequence)
		}
		copy.sequences[obj] = sequences[sequence]
	}
	maps := make(map[*keyMap]*keyMap)
	for obj, m := range s.maps {
		if maps[m] == nil {
			maps[m] = &keyMap{built: m.built}
		}
		copy.maps[obj] = maps[m]
	}
	return copy
}

func copySequence(sequence *rawSequence) *rawSequence {
	copy := &rawSequence{length: sequence.length, prefix: sequence.prefix, cap: sequence.cap, origin: sequence.origin, tail: sequence.tail, values: make(map[int]rawArgument)}
	for index, arg := range sequence.values {
		copy.values[index] = arg
	}
	return copy
}

func joinArgument(a, b rawArgument) rawArgument {
	joined := rawArgument{built: a.built || b.built}
	if a.text == b.text {
		joined.text = a.text
	}
	return joined
}

func joinSequence(a, b *rawSequence) *rawSequence {
	if a == nil {
		return copySequence(b)
	}
	if b == nil {
		return copySequence(a)
	}
	out := &rawSequence{length: a.length, cap: -1, values: make(map[int]rawArgument), tail: joinArgument(a.tail, b.tail)}
	if a.cap == b.cap {
		out.cap = a.cap
	}
	if a.origin == b.origin {
		out.origin = a.origin
	}
	limit := a.length
	if a.length < 0 || b.length < 0 || a.length != b.length {
		out.length = -1
		if a.length < 0 {
			limit = a.prefix
		}
		if b.length < 0 {
			if b.prefix < limit {
				limit = b.prefix
			}
		} else if b.length < limit {
			limit = b.length
		}
		out.prefix = limit
	}
	for index, arg := range a.values {
		if out.length < 0 && index >= limit {
			out.tail.built = out.tail.built || arg.built
		} else {
			out.values[index] = joinArgument(arg, b.values[index])
		}
	}
	for index, arg := range b.values {
		if out.length < 0 && index >= limit {
			out.tail.built = out.tail.built || arg.built
		} else if _, ok := a.values[index]; !ok {
			out.values[index] = joinArgument(rawArgument{}, arg)
		}
	}
	return out
}

func joinStates(a, b *keyState) *keyState {
	out := newKeyState()
	for obj, value := range a.built {
		out.built[obj] = value || b.built[obj]
	}
	for obj, value := range b.built {
		out.built[obj] = out.built[obj] || value
	}
	for obj, value := range a.strings {
		if b.strings[obj] == value {
			out.strings[obj] = value
		}
	}
	type seqPair struct{ a, b *rawSequence }
	seqs := make(map[seqPair]*rawSequence)
	for obj := range a.sequences {
		pair := seqPair{a.sequences[obj], b.sequences[obj]}
		if seqs[pair] == nil {
			seqs[pair] = joinSequence(pair.a, pair.b)
		}
		out.sequences[obj] = seqs[pair]
	}
	for obj := range b.sequences {
		if _, ok := out.sequences[obj]; !ok {
			pair := seqPair{nil, b.sequences[obj]}
			if seqs[pair] == nil {
				seqs[pair] = joinSequence(pair.a, pair.b)
			}
			out.sequences[obj] = seqs[pair]
		}
	}
	type mapPair struct{ a, b *keyMap }
	maps := make(map[mapPair]*keyMap)
	for obj := range a.maps {
		pair := mapPair{a.maps[obj], b.maps[obj]}
		if maps[pair] == nil {
			maps[pair] = &keyMap{built: pair.a.built || (pair.b != nil && pair.b.built)}
		}
		out.maps[obj] = maps[pair]
	}
	for obj, m := range b.maps {
		if _, ok := out.maps[obj]; !ok {
			pair := mapPair{nil, m}
			if maps[pair] == nil {
				maps[pair] = &keyMap{built: m.built}
			}
			out.maps[obj] = maps[pair]
		}
	}
	out.refresh()
	return out
}

func (s *keyState) refresh() {
	for obj, seq := range s.sequences {
		built := seq.tail.built
		for _, arg := range seq.values {
			built = built || arg.built
		}
		s.built[obj] = built
	}
	for obj, m := range s.maps {
		s.built[obj] = m.built
	}
}

func checkKeys(file *ast.File, fset *token.FileSet, info *types.Info) []error {
	violations := make(map[token.Pos]Violation)
	ast.Inspect(file, func(node ast.Node) bool {
		var body *ast.BlockStmt
		switch n := node.(type) {
		case *ast.FuncDecl:
			body = n.Body
		case *ast.FuncLit:
			body = n.Body
		default:
			return true
		}
		if body == nil {
			return true
		}
		graph := cfg.New(body, func(*ast.CallExpr) bool { return true })
		states := make([]*keyState, len(graph.Blocks))
		states[0] = newKeyState()
		queue := []*cfg.Block{graph.Blocks[0]}
		for len(queue) > 0 {
			block := queue[0]
			queue = queue[1:]
			state := states[block.Index].clone()
			if block.Kind == cfg.KindRangeBody {
				if stmt, ok := block.Stmt.(*ast.RangeStmt); ok {
					state.rangeValues(stmt, info)
				}
			}
			for _, item := range block.Nodes {
				ast.Inspect(item, func(node ast.Node) bool {
					if _, ok := node.(*ast.FuncLit); ok {
						return false
					}
					if call, ok := node.(*ast.CallExpr); ok && keyCall(call, info, state) {
						position := fset.PositionFor(call.Pos(), false)
						if !hasForceIgnoreComment(file, fset, position.Line) {
							violations[call.Pos()] = Violation{Position: position, Reason: keyReason}
						}
					}
					return true
				})
				state.transfer(item, info)
			}
			for _, next := range block.Succs {
				if states[next.Index] == nil {
					states[next.Index] = state.clone()
					queue = append(queue, next)
				} else {
					joined := joinStates(states[next.Index], state)
					if !reflect.DeepEqual(states[next.Index], joined) {
						states[next.Index] = joined
						queue = append(queue, next)
					}
				}
			}
		}
		return true
	})
	var errs []error
	for _, violation := range violations {
		errs = append(errs, violation)
	}
	sort.Slice(errs, func(i, j int) bool {
		return errs[i].(Violation).Position.Offset < errs[j].(Violation).Position.Offset
	})
	return errs
}

func (s *keyState) rangeValues(stmt *ast.RangeStmt, info *types.Info) {
	t := info.TypeOf(stmt.X)
	if t == nil {
		return
	}
	var expr ast.Expr
	var built bool
	switch t.Underlying().(type) {
	case *types.Slice, *types.Array:
		expr = stmt.Value
		built = s.argument(stmt.X, info).built
	case *types.Map:
		expr = stmt.Key
		if m := s.mapValue(stmt.X, info); m != nil {
			built = m.built
		}
	}
	if id, ok := expr.(*ast.Ident); ok {
		obj := info.ObjectOf(id)
		if obj != nil {
			s.built[obj] = built
			s.assign(obj, rawArgument{}, nil, nil)
		}
	}
}

func (s *keyState) transfer(node ast.Node, info *types.Info) {
	switch n := node.(type) {
	case *ast.AssignStmt:
		if len(n.Lhs) != len(n.Rhs) {
			return
		}
		args := make([]rawArgument, len(n.Rhs))
		seqs := make([]*rawSequence, len(n.Rhs))
		maps := make([]*keyMap, len(n.Rhs))
		for i, expr := range n.Rhs {
			args[i] = s.argument(expr, info)
			seqs[i], _ = s.sequence(expr, info)
			maps[i] = s.mapValue(expr, info)
		}
		for i, lhs := range n.Lhs {
			switch lhs := ast.Unparen(lhs).(type) {
			case *ast.Ident:
				obj := info.ObjectOf(lhs)
				if obj == nil {
					continue
				}
				if n.Tok == token.ADD_ASSIGN {
					s.built[obj] = true
					s.assign(obj, rawArgument{}, nil, nil)
				} else {
					s.built[obj] = args[i].built
					s.assign(obj, args[i], seqs[i], maps[i])
				}
			case *ast.IndexExpr:
				id, ok := ast.Unparen(lhs.X).(*ast.Ident)
				if !ok {
					continue
				}
				if m := s.mapValue(id, info); m != nil {
					m.built = m.built || s.argument(lhs.Index, info).built
				}
				if seq, ok := s.sequence(id, info); ok {
					value := info.Types[lhs.Index].Value
					if value != nil && value.Kind() == constant.Int {
						index, ok := constant.Int64Val(value)
						if ok && index >= 0 && int64(int(index)) == index {
							seq.values[int(index)] = args[i]
							if seq.origin != token.NoPos {
								for _, other := range s.sequences {
									if other != seq && other.origin == seq.origin &&
										(other.length < 0 || int(index) < other.length) {
										other.values[int(index)] = joinArgument(other.values[int(index)], args[i])
									}
								}
							}
						}
					} else if args[i].built {
						seq.tail.built = true
					}
				}
			}
		}
	case *ast.ValueSpec:
		for i, id := range n.Names {
			if i < len(n.Values) {
				expr := n.Values[i]
				seq, _ := s.sequence(expr, info)
				arg := s.argument(expr, info)
				s.built[info.ObjectOf(id)] = arg.built
				s.assign(info.ObjectOf(id), arg, seq, s.mapValue(expr, info))
			} else if t := info.TypeOf(id); t != nil {
				if _, ok := t.Underlying().(*types.Slice); ok {
					s.sequences[info.ObjectOf(id)] = &rawSequence{origin: id.Pos(), values: make(map[int]rawArgument)}
				}
			}
		}
	}
	s.refresh()
}
