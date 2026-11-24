package sql

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/cfg"
)

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

type transaction struct {
	object   types.Object
	err      types.Object
	begin    *ast.CallExpr
	callback bool
}

const (
	txStarted = 1 << iota
	txActive
	txDeferred
	txUnknownError
	txDeferredCommit
)

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

func objectOf(id *ast.Ident, info *types.Info) types.Object {
	if obj := info.Defs[id]; obj != nil {
		return obj
	}
	return info.Uses[id]
}

func isMethod(call *ast.CallExpr, info *types.Info, path, name string) bool {
	fn, _ := calledFunc(call, info)
	if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != path || fn.Name() != name {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	return ok && sig.Recv() != nil
}

func isBegin(call *ast.CallExpr, info *types.Info) bool {
	return isMethod(call, info, "database/sql", "Begin") ||
		isMethod(call, info, "database/sql", "BeginTx") ||
		isMethod(call, info, "gorm.io/gorm", "Begin")
}

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

type txWork struct {
	block       *cfg.Block
	state       int
	connections string
}

type txBlockState struct {
	block *cfg.Block
	state int
}

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

func endsTransaction(expr ast.Expr, tx transaction, info *types.Info) bool {
	expr = ast.Unparen(expr)
	if sel, ok := expr.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" {
		expr = ast.Unparen(sel.X)
	}
	call, ok := expr.(*ast.CallExpr)
	return ok && (isEnd(call, tx, info, "Commit") || isEnd(call, tx, info, "Rollback"))
}

func isEnd(call *ast.CallExpr, tx transaction, info *types.Info, name string) bool {
	return (isMethod(call, info, "database/sql", name) || isMethod(call, info, "gorm.io/gorm", name)) &&
		rootedIn(receiver(call, info), tx.object, info)
}

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
			// A non-nil destination is itself a query result.
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
