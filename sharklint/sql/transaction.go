package sql

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/cfg"
)

// 事务检查的违规文案。
const (
	txDefer  = "事务开启后应 defer Rollback"
	txOpen   = "事务退出时未提交或回滚"
	txRPC    = "事务内禁止调用外部 RPC"
	txWait   = "事务内禁止显式等待"
	txNested = "事务内禁止开启嵌套事务"
	txWrite  = "事务中修改数据后必须检查错误"
	txQuery  = "事务中查询结果被丢弃"
	txConn   = "事务内禁止使用非事务数据库连接"
)

// transaction 描述一个待分析的事务。
type transaction struct {
	// object 是事务变量。
	object types.Object
	// err 是接收 Begin 返回错误的变量，没有时为 nil。
	err types.Object
	// begin 是开启事务的调用；gorm Transaction 回调中的事务无此调用，为 nil。
	begin *ast.CallExpr
	// callback 表示事务来自 gorm Transaction 回调参数，由 gorm 负责提交和回滚。
	callback bool
}

// 事务流分析状态的位标志。
const (
	// txStarted 表示已开启事务。
	txStarted = 1 << iota
	// txActive 表示事务尚未提交或回滚。
	txActive
	// txDeferred 表示已 defer Rollback。
	txDeferred
	// txUnknownError 表示 Begin 返回的错误尚未检查，此时事务可能并未真正开启。
	txUnknownError
	// txDeferredCommit 表示已 defer Commit。
	txDeferredCommit
)

// checkTransactions 找出 file 中每个函数内开启的事务（database/sql 与 gorm 的 Begin，
// 以及 gorm Transaction 回调的参数），对其逐一做流分析，返回违规列表。
// 同一位置同一原因只报告一次，带豁免注释的位置不报告。
func checkTransactions(file *ast.File, fset *token.FileSet, info *types.Info) []error {
	var errors []error
	reported := make(map[string]bool)
	report := func(pos token.Pos, reason string) {
		position := fset.PositionFor(pos, false)
		key := position.String() + reason
		if !reported[key] && !hasForceIgnoreComment(file, fset, position.Line) {
			reported[key] = true
			errors = append(errors, Violation{Position: position, Reason: reason})
		}
	}
	// 先收集 gorm.Transaction 的回调函数字面量，其第一个参数即事务。
	callbacks := make(map[*ast.FuncLit]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isMethod(call, info, "gorm.io/gorm", "Transaction") || len(call.Args) == 0 {
			return true
		}
		if lit, ok := ast.Unparen(call.Args[0]).(*ast.FuncLit); ok {
			callbacks[lit] = true
		}
		return true
	})
	ast.Inspect(file, func(n ast.Node) bool {
		var body *ast.BlockStmt
		var callback *ast.FuncLit
		switch fn := n.(type) {
		case *ast.FuncDecl:
			body = fn.Body
		case *ast.FuncLit:
			body = fn.Body
			if callbacks[fn] {
				callback = fn
			}
		default:
			return true
		}
		if body == nil {
			return true
		}
		var transactions []transaction
		if callback != nil && callback.Type.Params != nil && len(callback.Type.Params.List) > 0 {
			for _, name := range callback.Type.Params.List[0].Names {
				transactions = append(transactions, transaction{object: info.Defs[name], callback: true})
			}
		}
		inspectBody(body, func(n ast.Node) {
			var lhs []ast.Expr
			var rhs []ast.Expr
			switch assign := n.(type) {
			case *ast.AssignStmt:
				lhs, rhs = assign.Lhs, assign.Rhs
			case *ast.ExprStmt:
				if call, ok := ast.Unparen(assign.X).(*ast.CallExpr); ok && isBegin(call, info) {
					report(call.Pos(), txDefer)
					report(call.Pos(), txOpen)
				}
				return
			case *ast.ValueSpec:
				for _, name := range assign.Names {
					lhs = append(lhs, name)
				}
				rhs = assign.Values
			default:
				return
			}
			if len(rhs) != 1 || len(lhs) == 0 {
				return
			}
			call, ok := ast.Unparen(rhs[0]).(*ast.CallExpr)
			if !ok || !isBegin(call, info) {
				return
			}
			id, ok := lhs[0].(*ast.Ident)
			if ok && id.Name == "_" {
				report(call.Pos(), txDefer)
				report(call.Pos(), txOpen)
				return
			}
			if !ok {
				return
			}
			tx := transaction{object: objectOf(id, info), begin: call}
			if len(lhs) > 1 {
				if errID, ok := lhs[1].(*ast.Ident); ok && errID.Name != "_" {
					tx.err = objectOf(errID, info)
				}
			}
			transactions = append(transactions, tx)
		})
		for _, tx := range transactions {
			if tx.object != nil {
				analyzeTransaction(body, tx, info, report)
			}
		}
		return true
	})
	return errors
}

// inspectBody 遍历 node 但不进入嵌套的函数字面量，使每个函数体独立分析。
func inspectBody(node ast.Node, visit func(ast.Node)) {
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		visit(n)
		return true
	})
}

// objectOf 返回 id 定义或引用的对象。
func objectOf(id *ast.Ident, info *types.Info) types.Object {
	if obj := info.Defs[id]; obj != nil {
		return obj
	}
	return info.Uses[id]
}

// isMethod 判断 call 是否调用了包 path 中名为 name 的方法。
func isMethod(call *ast.CallExpr, info *types.Info, path, name string) bool {
	fn, _ := calledFunc(call, info)
	if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != path || fn.Name() != name {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	return ok && sig.Recv() != nil
}

// isBegin 判断 call 是否是开启事务的调用（sql Begin/BeginTx 或 gorm Begin）。
func isBegin(call *ast.CallExpr, info *types.Info) bool {
	return isMethod(call, info, "database/sql", "Begin") ||
		isMethod(call, info, "database/sql", "BeginTx") ||
		isMethod(call, info, "gorm.io/gorm", "Begin")
}

// receiver 返回方法调用的接收者表达式；方法表达式形式取第一个实参，无接收者时为 nil。
func receiver(call *ast.CallExpr, info *types.Info) ast.Expr {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	if selection := info.Selections[sel]; selection != nil && selection.Kind() == types.MethodExpr {
		if len(call.Args) > 0 {
			return call.Args[0]
		}
		return nil
	}
	return sel.X
}

// rootedIn 判断 expr 是否以 obj 为链式调用的起点（如 tx.Where(...).Find(...)）。
func rootedIn(expr ast.Expr, obj types.Object, info *types.Info) bool {
	if expr == nil {
		return false
	}
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return info.Uses[e] == obj
	case *ast.CallExpr:
		return rootedIn(receiver(e, info), obj, info)
	}
	return false
}

// txWork 是流分析工作队列中的一项：在某基本块入口处的事务状态与连接状态。
type txWork struct {
	block       *cfg.Block
	state       int
	connections string
}

// txBlockState 是已访问状态的键：基本块与事务状态的组合。
type txBlockState struct {
	block *cfg.Block
	state int
}

// analyzeTransaction 沿函数体的控制流图对单个事务做前向分析，状态为 (基本块, 事务状态) 组合：
//   - 同一事务重复开启报嵌套；
//   - 事务活跃期间检查 RPC、显式等待、非事务连接、写操作未检查错误、查询结果丢弃；
//   - 函数退出时，非回调事务若未 defer Rollback 或仍未提交/回滚则报告。
//
// Begin 的错误分支（err != nil）视为事务未开启，对应路径上不再要求提交或回滚。
func analyzeTransaction(body *ast.BlockStmt, tx transaction, info *types.Info, report func(token.Pos, string)) {
	graph := cfg.New(body, func(call *ast.CallExpr) bool {
		id, ok := ast.Unparen(call.Fun).(*ast.Ident)
		return !ok || id.Name != "panic" || info.Uses[id] != types.Universe.Lookup("panic")
	})
	initial := 0
	if tx.callback {
		initial = txStarted | txActive
	}
	connections := newConnectionTracker(body, tx.object, info)
	initialConnections := connections.empty()
	if tx.callback {
		initialConnections = connections.set(initialConnections, tx.object, connectionTx)
	}
	// 带 default 分支的 select 中的接收操作不会阻塞，不算显式等待。
	nonblocking := make(map[*ast.UnaryExpr]bool)
	inspectBody(body, func(n ast.Node) {
		selectStmt, ok := n.(*ast.SelectStmt)
		if !ok {
			return
		}
		hasDefault := false
		for _, clause := range selectStmt.Body.List {
			if clause.(*ast.CommClause).Comm == nil {
				hasDefault = true
			}
		}
		if hasDefault {
			for _, clause := range selectStmt.Body.List {
				if comm := clause.(*ast.CommClause).Comm; comm != nil {
					inspectBody(comm, func(n ast.Node) {
						if unary, ok := n.(*ast.UnaryExpr); ok && unary.Op == token.ARROW {
							nonblocking[unary] = true
						}
					})
				}
			}
		}
	})
	queue := []txWork{{graph.Blocks[0], initial, initialConnections}}
	states := map[txBlockState]string{{graph.Blocks[0], initial}: initialConnections}
	for len(queue) > 0 {
		work := queue[0]
		queue = queue[1:]
		if states[txBlockState{work.block, work.state}] != work.connections {
			continue
		}
		state := work.state
		connectionState := work.connections
		for _, node := range work.block.Nodes {
			if tx.begin != nil {
				containsBegin := false
				inspectBody(node, func(n ast.Node) {
					if n == tx.begin {
						containsBegin = true
					}
				})
				if containsBegin {
					if state&txActive != 0 {
						report(tx.begin.Pos(), txNested)
					}
					state = txStarted | txActive
					if tx.err != nil || isMethod(tx.begin, info, "gorm.io/gorm", "Begin") {
						state |= txUnknownError
					}
					connectionState = connections.transfer(node, connectionState, info)
					continue
				}
			}
			if state&txActive != 0 {
				checkTransactionNode(node, body, tx, info, connections, connectionState, nonblocking, report)
			}
			if assign, ok := node.(*ast.AssignStmt); ok && tx.err != nil {
				for _, lhs := range assign.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && objectOf(id, info) == tx.err {
						state &^= txUnknownError
					}
				}
			}
			if deferStmt, ok := node.(*ast.DeferStmt); ok && isEnd(deferStmt.Call, tx, info, "Rollback") && connections.isTransaction(tx.object, connectionState) {
				state |= txDeferred
			} else if deferStmt, ok := node.(*ast.DeferStmt); ok && isEnd(deferStmt.Call, tx, info, "Commit") && connections.isTransaction(tx.object, connectionState) {
				state |= txDeferredCommit
			} else if expr, ok := node.(*ast.ExprStmt); ok {
				if endsTransaction(expr.X, tx, info) && connections.isTransaction(tx.object, connectionState) {
					state &^= txActive
				}
			} else if ret, ok := node.(*ast.ReturnStmt); ok {
				for _, result := range ret.Results {
					if endsTransaction(result, tx, info) && connections.isTransaction(tx.object, connectionState) {
						state &^= txActive
					}
				}
			} else if assign, ok := node.(*ast.AssignStmt); ok {
				for _, result := range assign.Rhs {
					if endsTransaction(result, tx, info) && connections.isTransaction(tx.object, connectionState) {
						state &^= txActive
					}
					connectionState = connections.transfer(node, connectionState, info)
				}
			}
		}
		if len(work.block.Succs) == 0 && !tx.callback && state&txStarted != 0 {
			if state&txDeferred == 0 {
				report(tx.begin.Pos(), txDefer)
				if state&txActive != 0 && state&txDeferredCommit == 0 {
					pos := body.Rbrace
					if ret := work.block.Return(); ret != nil {
						pos = ret.Pos()
					}
					report(pos, txOpen)
				}
			}
		}
		for i, next := range work.block.Succs {
			nextState := state
			if state&txUnknownError != 0 && len(work.block.Succs) == 2 && len(work.block.Nodes) > 0 {
				if cond, ok := work.block.Nodes[len(work.block.Nodes)-1].(ast.Expr); ok {
					if failed, known := errorCondition(cond, tx, info); known {
						if (i == 0) == failed {
							nextState = 0
						} else {
							nextState &^= txUnknownError
						}
					}
				}
			}
			key := txBlockState{next, nextState}
			previous, exists := states[key]
			nextConnections := connectionState
			if exists {
				nextConnections = connections.join(previous, connectionState)
				if nextConnections == previous {
					continue
				}
			}
			states[key] = nextConnections
			queue = append(queue, txWork{next, nextState, nextConnections})
		}
	}
}

// endsTransaction 判断 expr 是否是事务的 Commit 或 Rollback 调用（可带 .Error 取值）。
func endsTransaction(expr ast.Expr, tx transaction, info *types.Info) bool {
	expr = ast.Unparen(expr)
	if sel, ok := expr.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" {
		expr = ast.Unparen(sel.X)
	}
	call, ok := expr.(*ast.CallExpr)
	return ok && (isEnd(call, tx, info, "Commit") || isEnd(call, tx, info, "Rollback"))
}

// isEnd 判断 call 是否是 tx 的名为 name（Commit 或 Rollback）的调用。
func isEnd(call *ast.CallExpr, tx transaction, info *types.Info, name string) bool {
	return (isMethod(call, info, "database/sql", name) || isMethod(call, info, "gorm.io/gorm", name)) &&
		rootedIn(receiver(call, info), tx.object, info)
}

// errorCondition 解析 err == nil 或 err != nil（含 tx.Error）形式的条件，
// 返回条件为真时是否表示出错，以及条件是否属于该形式。
func errorCondition(expr ast.Expr, tx transaction, info *types.Info) (bool, bool) {
	bin, ok := ast.Unparen(expr).(*ast.BinaryExpr)
	if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
		return false, false
	}
	isErr := func(expr ast.Expr) bool {
		switch e := ast.Unparen(expr).(type) {
		case *ast.Ident:
			return tx.err != nil && info.Uses[e] == tx.err
		case *ast.SelectorExpr:
			return e.Sel.Name == "Error" && rootedIn(e.X, tx.object, info)
		}
		return false
	}
	isNil := func(expr ast.Expr) bool {
		id, ok := ast.Unparen(expr).(*ast.Ident)
		return ok && info.Uses[id] == types.Universe.Lookup("nil")
	}
	if (isErr(bin.X) && isNil(bin.Y)) || (isNil(bin.X) && isErr(bin.Y)) {
		return bin.Op == token.NEQ, true
	}
	return false, false
}

// checkTransactionNode 检查事务活跃期间的单条语句：定时通道等待、time.Sleep、
// 嵌套事务、RPC、非事务连接，以及事务上的写操作未检查错误、查询结果被丢弃。
func checkTransactionNode(node, body ast.Node, tx transaction, info *types.Info, connections *connectionTracker, connectionState string, nonblocking map[*ast.UnaryExpr]bool, report func(token.Pos, string)) {
	inspectBody(node, func(n ast.Node) {
		if unary, ok := n.(*ast.UnaryExpr); ok && unary.Op == token.ARROW && !nonblocking[unary] && isTimerChannel(unary.X, info) {
			report(unary.Pos(), txWait)
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return
		}
		fn, _ := calledFunc(call, info)
		if fn == nil || fn.Pkg() == nil {
			return
		}
		path, name := fn.Pkg().Path(), fn.Name()
		if nonTransactionConnection(call, fn, info, connections, connectionState) {
			report(call.Pos(), txConn)
		}
		if isBegin(call, info) || isMethod(call, info, "gorm.io/gorm", "Transaction") {
			report(call.Pos(), txNested)
		}
		if isRPC(fn) {
			report(call.Pos(), txRPC)
		}
		if path == "time" && name == "Sleep" {
			report(call.Pos(), txWait)
		}
		if !rootedIn(receiver(call, info), tx.object, info) {
			return
		}
		write := path == "database/sql" && (name == "Exec" || name == "ExecContext") ||
			path == "gorm.io/gorm" && (name == "Create" || name == "CreateInBatches" || name == "Save" ||
				name == "Update" || name == "Updates" || name == "UpdateColumn" || name == "UpdateColumns" || name == "Delete" || name == "Exec")
		query := path == "database/sql" && (name == "Query" || name == "QueryContext" || name == "QueryRow" || name == "QueryRowContext")
		dest := 0
		if name == "Pluck" {
			dest = 1
		}
		_, offset := calledFunc(call, info)
		dest += offset
		if path == "gorm.io/gorm" && (name == "Find" || name == "First" || name == "Last" || name == "Take" || name == "Scan" || name == "Pluck" || name == "Count") && len(call.Args) > dest {
			// 目标参数非 nil 时，查询结果已写入目标，不属于被丢弃；仅目标为 nil 时才可能丢弃。
			id, ok := ast.Unparen(call.Args[dest]).(*ast.Ident)
			query = ok && info.Uses[id] == types.Universe.Lookup("nil")
		}
		if write && !resultChecked(node, body, call, path == "gorm.io/gorm", info) {
			report(call.Pos(), txWrite)
		}
		if query && resultDiscarded(node, call) {
			report(call.Pos(), txQuery)
		}
	})
}

// isRPC 判断 fn 是否是外部 RPC 调用：net/http 客户端请求、grpc Invoke/NewStream，
// 或以 grpc.CallOption 为变长参数的方法。
func isRPC(fn *types.Func) bool {
	path, name := fn.Pkg().Path(), fn.Name()
	if path == "net/http" && (name == "Do" || name == "Get" || name == "Post" || name == "PostForm" || name == "Head") {
		sig, ok := fn.Type().(*types.Signature)
		if !ok {
			return false
		}
		if sig.Recv() == nil {
			return name != "Do"
		}
		t := types.Unalias(sig.Recv().Type())
		if ptr, ok := t.(*types.Pointer); ok {
			t = types.Unalias(ptr.Elem())
		}
		named, ok := t.(*types.Named)
		return ok && named.Obj().Name() == "Client"
	}
	if path == "google.golang.org/grpc" && (name == "Invoke" || name == "NewStream") {
		return true
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || !sig.Variadic() || sig.Params().Len() == 0 {
		return false
	}
	slice, ok := sig.Params().At(sig.Params().Len() - 1).Type().(*types.Slice)
	if !ok {
		return false
	}
	named, ok := types.Unalias(slice.Elem()).(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "google.golang.org/grpc" && named.Obj().Name() == "CallOption"
}

// isTimerChannel 判断 expr 是否是 time.After 的返回值，或 Timer/Ticker 的 C 通道。
func isTimerChannel(expr ast.Expr, info *types.Info) bool {
	switch e := ast.Unparen(expr).(type) {
	case *ast.CallExpr:
		fn, _ := calledFunc(e, info)
		return fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "time" && fn.Name() == "After"
	case *ast.SelectorExpr:
		if e.Sel.Name != "C" {
			return false
		}
		t := info.TypeOf(e.X)
		if t == nil {
			return false
		}
		if ptr, ok := types.Unalias(t).(*types.Pointer); ok {
			t = ptr.Elem()
		}
		named, ok := types.Unalias(t).(*types.Named)
		return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "time" &&
			(named.Obj().Name() == "Timer" || named.Obj().Name() == "Ticker")
	}
	return false
}

// resultDiscarded 判断 call 的结果是否被丢弃：作为独立语句，或赋值给 _。
func resultDiscarded(node ast.Node, call *ast.CallExpr) bool {
	switch n := node.(type) {
	case *ast.ExprStmt:
		return n.X == call
	case *ast.AssignStmt:
		if len(n.Rhs) != 1 || n.Rhs[0] != call || len(n.Lhs) == 0 {
			return false
		}
		id, ok := n.Lhs[0].(*ast.Ident)
		return ok && id.Name == "_"
	}
	return false
}

// resultChecked 判断写操作 call 的错误是否被检查：
// gorm 看 .Error 或结果变量的 Error 是否在后续被比较或返回，database/sql 看第二个返回值。
func resultChecked(node, body ast.Node, call *ast.CallExpr, gorm bool, info *types.Info) bool {
	if gorm {
		var errorField *ast.SelectorExpr
		inspectBody(node, func(n ast.Node) {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && sel.X == call {
				errorField = sel
			}
		})
		if errorField != nil {
			switch n := node.(type) {
			case *ast.ReturnStmt:
				return true
			case *ast.AssignStmt:
				for i, rhs := range n.Rhs {
					if rhs == errorField && i < len(n.Lhs) {
						if id, ok := n.Lhs[i].(*ast.Ident); ok && id.Name != "_" {
							return errorUsed(body, objectOf(id, info), nil, n.End(), info)
						}
					}
				}
			case *ast.BinaryExpr:
				return true
			}
			return false
		}
	}
	assign, ok := node.(*ast.AssignStmt)
	if !ok || len(assign.Rhs) != 1 || assign.Rhs[0] != call {
		_, returned := node.(*ast.ReturnStmt)
		return returned && !gorm
	}
	index := 1
	if gorm {
		index = 0
	}
	if index >= len(assign.Lhs) {
		return false
	}
	id, ok := assign.Lhs[index].(*ast.Ident)
	if !ok || id.Name == "_" {
		return false
	}
	if gorm {
		return errorUsed(body, nil, objectOf(id, info), assign.End(), info)
	}
	return errorUsed(body, objectOf(id, info), nil, assign.End(), info)
}

// errorUsed 判断 after 之后 body 中是否有比较表达式或 return 使用了 errObj，
// 或使用了 resultObj 的 Error 字段。
func errorUsed(body ast.Node, errObj, resultObj types.Object, after token.Pos, info *types.Info) bool {
	matches := func(node ast.Node) bool {
		found := false
		inspectBody(node, func(n ast.Node) {
			if id, ok := n.(*ast.Ident); ok && errObj != nil && info.Uses[id] == errObj {
				found = true
			}
			if sel, ok := n.(*ast.SelectorExpr); ok && resultObj != nil && sel.Sel.Name == "Error" && rootedIn(sel.X, resultObj, info) {
				found = true
			}
		})
		return found
	}
	checked := false
	inspectBody(body, func(n ast.Node) {
		if n.Pos() <= after {
			return
		}
		switch n.(type) {
		case *ast.BinaryExpr, *ast.ReturnStmt:
			checked = checked || matches(n)
		}
	})
	return checked
}
