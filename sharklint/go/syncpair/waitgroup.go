package syncpair

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

// waitGroupEscapes 判断 body 中的 WaitGroup 是否会以本检查无法分析的方式使用:
// 作为参数传出、被非 go 启动的闭包引用、Add 的参数不是范围内的常量, 或 worker 内的用法不受支持。
func waitGroupEscapes(body *ast.BlockStmt, obj object, info *types.Info) bool {
	escapes := false
	workers := make(map[*ast.CallExpr]bool)
	inspectBody(body, func(n ast.Node) {
		if stmt, ok := n.(*ast.GoStmt); ok {
			workers[stmt.Call] = true
		}
	})
	inspectBody(body, func(n ast.Node) {
		var worker *ast.FuncLit
		switch n := n.(type) {
		case *ast.GoStmt:
			worker, _ = ast.Unparen(n.Call.Fun).(*ast.FuncLit)
		case *ast.CallExpr:
			if isSafeGo(n, info) && len(n.Args) == 1 {
				worker, _ = ast.Unparen(n.Args[0]).(*ast.FuncLit)
			}
		}
		if worker != nil && workerEscapes(worker.Body, obj, info) {
			escapes = true
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return
		}
		if lit, ok := ast.Unparen(call.Fun).(*ast.FuncLit); ok && !workers[call] && references(lit.Body, obj, info) {
			escapes = true
		}
		got, name := method(call, info)
		if got.key == obj.key && name == "Add" {
			if len(call.Args) != 1 || info.Types[call.Args[0]].Value == nil {
				escapes = true
			} else if n, ok := constant.Int64Val(info.Types[call.Args[0]].Value); !ok || n < -limit || n > limit {
				escapes = true
			}
		}
		if isSafeGo(call, info) {
			return
		}
		for _, arg := range call.Args {
			key, _ := objectKey(arg, info)
			if key == obj.key {
				escapes = true
			}
			if lit, ok := ast.Unparen(arg).(*ast.FuncLit); ok && references(lit.Body, obj, info) {
				escapes = true
			}
		}
	})
	return escapes
}

// workerEscapes 判断 worker 闭包体内对 WaitGroup 的使用是否不受支持:
// 在其中再启动引用它的闭包, 或在 worker 内增加计数（Add 正数或非常量）。
func workerEscapes(body *ast.BlockStmt, obj object, info *types.Info) bool {
	if waitGroupEscapes(body, obj, info) {
		return true
	}
	unsupported := false
	inspectBody(body, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.GoStmt:
			if lit, ok := ast.Unparen(n.Call.Fun).(*ast.FuncLit); ok && references(lit.Body, obj, info) {
				unsupported = true
			}
		case *ast.CallExpr:
			got, name := method(n, info)
			if got.key == obj.key && name == "Add" && len(n.Args) == 1 {
				v := info.Types[n.Args[0]].Value
				if v == nil || constant.Sign(v) > 0 {
					unsupported = true
				}
			}
		}
	})
	return unsupported
}

// references 判断 body 中是否引用了 obj 的根变量。
func references(body *ast.BlockStmt, obj object, info *types.Info) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && info.ObjectOf(id) == obj.root {
			found = true
		}
		return !found
	})
	return found
}

// applyWorker 统计直接启动的 worker 闭包在每个可达退出路径上对 WaitGroup 的完成次数
// （Done 与负数常量 Add, 含 defer）, 并从 s 中扣除, 返回各路径对应的后继状态;
// 某条路径完成次数超过未完成的 Add 时报告未配对的释放。
func applyWorker(s state, lit *ast.FuncLit, obj object, info *types.Info, report func(token.Pos, string)) []state {
	g := graph(lit.Body, info)
	type item struct {
		block int
		done  int
	}
	queue := []item{{block: 0}}
	seen := make(map[item]bool)
	results := make(map[state]bool)
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		if seen[w] {
			continue
		}
		seen[w] = true
		block := g.Blocks[w.block]
		if !block.Live {
			continue
		}
		done := w.done
		for _, node := range block.Nodes {
			var call *ast.CallExpr
			switch n := node.(type) {
			case *ast.ExprStmt:
				call, _ = ast.Unparen(n.X).(*ast.CallExpr)
			case *ast.DeferStmt:
				call = n.Call
			}
			if call == nil {
				continue
			}
			got, name := method(call, info)
			if got.key != obj.key {
				continue
			}
			switch name {
			case "Done":
				done = min(limit, done+1)
			case "Add":
				if len(call.Args) == 1 {
					v := info.Types[call.Args[0]].Value
					if v != nil {
						if n, ok := constant.Int64Val(v); ok && n < 0 && n >= -limit {
							done = min(limit, done-int(n))
						}
					}
				}
			}
		}
		if len(block.Succs) == 0 {
			if done > s.count {
				report(lit.Pos(), "WaitGroup 存在未配对的释放操作")
			}
			next := s
			next.count = max(0, next.count-done)
			if next.count == 0 && next.deferred == 0 {
				next.origin = token.NoPos
			}
			results[next] = true
		}
		for _, succ := range block.Succs {
			queue = append(queue, item{int(succ.Index), done})
		}
	}
	var out []state
	for result := range results {
		out = append(out, result)
	}
	// worker 永不退出时无法抵消登记, 保持原状态。
	if len(out) == 0 {
		return []state{s}
	}
	return out
}
