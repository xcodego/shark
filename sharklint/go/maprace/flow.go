package maprace

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/cfg"
)

// collect 收集 body 内（不进入嵌套函数字面量）对 map 的读写访问及其访问时持有的锁。
// 先对控制流图做前向数据流分析, 求出每个基本块入口处一定持有的锁集合
// （汇合点取交集, 独占锁遇到读锁降级）, 再逐节点记录访问。
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

// addAccess 当 expr 是可定位到变量的 map 时, 追加一条访问记录。
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

// objectKey 为由标识符、字段选择和解引用组成的表达式生成稳定标识, 并返回最左侧的变量;
// 无法定位到变量（如函数调用结果）时返回空值。
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

// updateLock 在 node 是 sync.Mutex/RWMutex 的 Lock、RLock、Unlock、RUnlock 语句时更新 state。
// defer 与其他形式的调用不在此处理。
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

// cloneLocks 返回 in 的副本。
func cloneLocks(in lockset) lockset {
	out := make(lockset, len(in))
	for key, exclusive := range in {
		out[key] = exclusive
	}
	return out
}

// mergeLocks 把 incoming 汇入 old: 只保留双方都持有的锁, 强度取较弱者;
// 返回合并结果及是否相对 old 发生变化。old 为 nil 表示首次到达该块。
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
