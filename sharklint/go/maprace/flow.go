package maprace

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/cfg"
)

func collect(body *ast.BlockStmt, info *types.Info) []access {
	writes := make(map[*ast.IndexExpr]bool)
	ranges := make(map[token.Pos]bool)
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if index, ok := ast.Unparen(lhs).(*ast.IndexExpr); ok {
					writes[index] = true
				}
			}
		case *ast.IncDecStmt:
			if index, ok := ast.Unparen(n.X).(*ast.IndexExpr); ok {
				writes[index] = true
			}
		case *ast.RangeStmt:
			ranges[n.X.Pos()] = true
		}
		return true
	})
	graph := cfg.New(body, func(*ast.CallExpr) bool { return true })
	in := make([]lockset, len(graph.Blocks))
	in[0] = lockset{}
	queue := []*cfg.Block{graph.Blocks[0]}
	for len(queue) > 0 {
		block := queue[0]
		queue = queue[1:]
		state := cloneLocks(in[block.Index])
		for _, node := range block.Nodes {
			updateLock(node, state, info)
		}
		for _, successor := range block.Succs {
			merged, changed := mergeLocks(in[successor.Index], state)
			if changed {
				in[successor.Index] = merged
				queue = append(queue, successor)
			}
		}
	}
	var result []access
	for _, block := range graph.Blocks {
		if !block.Live || in[block.Index] == nil {
			continue
		}
		state := cloneLocks(in[block.Index])
		for _, node := range block.Nodes {
			ast.Inspect(node, func(n ast.Node) bool {
				if n == nil {
					return false
				}
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				if expr, ok := n.(ast.Expr); ok && ranges[expr.Pos()] {
					if expr.Pos() == node.Pos() {
						result = addAccess(result, expr, false, state, info)
					}
				}
				switch n := n.(type) {
				case *ast.IndexExpr:
					result = addAccess(result, n.X, writes[n], state, info)
				case *ast.CallExpr:
					id, ok := ast.Unparen(n.Fun).(*ast.Ident)
					if !ok || len(n.Args) == 0 {
						break
					}
					builtin, ok := info.Uses[id].(*types.Builtin)
					if ok && (builtin.Name() == "delete" || builtin.Name() == "clear" || builtin.Name() == "len") {
						result = addAccess(result, n.Args[0], builtin.Name() != "len", state, info)
					}
				}
				return true
			})
			updateLock(node, state, info)
		}
	}
	return result
}

func addAccess(out []access, expr ast.Expr, write bool, locks lockset, info *types.Info) []access {
	t := info.TypeOf(expr)
	if t == nil {
		return out
	}
	if _, ok := types.Unalias(t).Underlying().(*types.Map); !ok {
		return out
	}
	key, root := objectKey(expr, info)
	if root == nil {
		return out
	}
	return append(out, access{key: key, root: root, pos: expr.Pos(), write: write, locks: cloneLocks(locks)})
}

func objectKey(expr ast.Expr, info *types.Info) (string, types.Object) {
	switch expr := ast.Unparen(expr).(type) {
	case *ast.Ident:
		obj := info.ObjectOf(expr)
		if obj != nil {
			return fmt.Sprintf("%p", obj), obj
		}
	case *ast.SelectorExpr:
		key, root := objectKey(expr.X, info)
		field := info.Uses[expr.Sel]
		if root != nil && field != nil {
			return fmt.Sprintf("%s/%p", key, field), root
		}
	case *ast.StarExpr:
		return objectKey(expr.X, info)
	}
	return "", nil
}

func updateLock(node ast.Node, state lockset, info *types.Info) {
	stmt, ok := node.(*ast.ExprStmt)
	if !ok {
		return
	}
	call, ok := ast.Unparen(stmt.X).(*ast.CallExpr)
	if !ok {
		return
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || info.Selections[sel] == nil {
		return
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "sync" {
		return
	}
	recv := info.Selections[sel].Recv()
	if ptr, ok := recv.(*types.Pointer); ok {
		recv = ptr.Elem()
	}
	named, ok := types.Unalias(recv).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "sync" ||
		(named.Obj().Name() != "Mutex" && named.Obj().Name() != "RWMutex") {
		return
	}
	key, root := objectKey(sel.X, info)
	if root == nil {
		return
	}
	switch fn.Name() {
	case "Lock":
		state[key] = true
	case "RLock":
		state[key] = false
	case "Unlock", "RUnlock":
		delete(state, key)
	}
}

func cloneLocks(in lockset) lockset {
	out := make(lockset, len(in))
	for key, exclusive := range in {
		out[key] = exclusive
	}
	return out
}

func mergeLocks(old, incoming lockset) (lockset, bool) {
	if old == nil {
		return cloneLocks(incoming), true
	}
	out := cloneLocks(old)
	changed := false
	for key, exclusive := range old {
		value, ok := incoming[key]
		if !ok {
			delete(out, key)
			changed = true
		} else if exclusive && !value {
			out[key] = false
			changed = true
		}
	}
	return out, changed
}
