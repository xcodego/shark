package sql

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

// connectionTracker 在事务函数体内跟踪 gorm.DB 和 sql.Stmt 变量究竟绑定在事务上还是非事务连接上。
// 流分析状态是一个字符串，每个被跟踪的变量占一个字节（取值为 connection* 位集合）。
type connectionTracker struct {
	// indices 记录变量在状态字符串中的下标。
	indices map[types.Object]int
}

// 变量可能绑定的连接类型位标志；多条路径汇合时按位或合并。
const (
	connectionTx byte = 1 << iota
	connectionNonTx
	connectionUnknown
)

// newConnectionTracker 收集 body 中出现的 gorm.DB 与 sql.Stmt 变量（以及 tx）作为被跟踪对象。
func newConnectionTracker(body *ast.BlockStmt, tx types.Object, info *types.Info) *connectionTracker {
	tracker := &connectionTracker{indices: make(map[types.Object]int)}
	add := func(obj types.Object) {
		if obj == nil {
			return
		}
		path, name := namedType(obj.Type())
		if (path == "gorm.io/gorm" && name == "DB") || (path == "database/sql" && name == "Stmt") {
			if _, exists := tracker.indices[obj]; !exists {
				tracker.indices[obj] = len(tracker.indices)
			}
		}
	}
	add(tx)
	inspectBody(body, func(n ast.Node) {
		if id, ok := n.(*ast.Ident); ok {
			add(objectOf(id, info))
		}
	})
	return tracker
}

// namedType 返回 t（可为指针）对应命名类型的包路径和类型名，不是命名类型时返回空串。
func namedType(t types.Type) (string, string) {
	if t == nil {
		return "", ""
	}
	t = types.Unalias(t)
	if ptr, ok := t.(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return "", ""
	}
	return named.Obj().Pkg().Path(), named.Obj().Name()
}

// empty 返回初始状态，所有变量均为未知连接。
func (c *connectionTracker) empty() string {
	return strings.Repeat("\x04", len(c.indices))
}

// join 合并两个分支上的状态，按位取并集。
func (c *connectionTracker) join(a, b string) string {
	joined := []byte(a)
	for i := range joined {
		joined[i] |= b[i]
	}
	return string(joined)
}

// set 返回将 obj 的连接类型设为 value 后的新状态；未被跟踪的变量保持不变。
func (c *connectionTracker) set(state string, obj types.Object, value byte) string {
	index, ok := c.indices[obj]
	if !ok {
		return state
	}
	updated := []byte(state)
	updated[index] = value
	return string(updated)
}

// get 返回 obj 在 state 中的连接类型，未被跟踪时为 connectionUnknown。
func (c *connectionTracker) get(state string, obj types.Object) byte {
	if index, ok := c.indices[obj]; ok {
		return state[index]
	}
	return connectionUnknown
}

// isTransaction 判断 obj 此刻是否确定绑定在事务上：*sql.Tx 类型，或状态为 connectionTx。
func (c *connectionTracker) isTransaction(obj types.Object, state string) bool {
	path, name := namedType(obj.Type())
	return path == "database/sql" && name == "Tx" || c.get(state, obj) == connectionTx
}

// source 推断 expr 产生的连接类型：已跟踪变量沿用其状态，gorm Begin 与 Tx.Stmt/Prepare 为事务，
// DB/Conn 上的 Prepare 为非事务，gorm 链式调用继承接收者，其余为未知。
func (c *connectionTracker) source(expr ast.Expr, state string, info *types.Info) byte {
	if expr == nil {
		return connectionUnknown
	}
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return c.get(state, info.Uses[e])
	case *ast.CallExpr:
		fn, _ := calledFunc(e, info)
		if fn == nil || fn.Pkg() == nil {
			return connectionUnknown
		}
		switch fn.Pkg().Path() {
		case "gorm.io/gorm":
			if fn.Name() == "Begin" {
				return connectionTx
			}
			return c.source(receiver(e, info), state, info)
		case "database/sql":
			if fn.Name() != "Prepare" && fn.Name() != "PrepareContext" &&
				fn.Name() != "Stmt" && fn.Name() != "StmtContext" {
				return connectionUnknown
			}
			path, name := namedType(info.TypeOf(receiver(e, info)))
			if path == "database/sql" {
				switch name {
				case "Tx":
					return connectionTx
				case "DB", "Conn":
					return connectionNonTx
				}
			}
		}
	}
	return connectionUnknown
}

// transfer 处理赋值与变量声明语句，把右值的连接类型传递给左侧标识符，返回新状态。
func (c *connectionTracker) transfer(node ast.Node, state string, info *types.Info) string {
	var lhs []ast.Expr
	var rhs []ast.Expr
	switch n := node.(type) {
	case *ast.AssignStmt:
		if n.Tok != token.ASSIGN && n.Tok != token.DEFINE {
			return state
		}
		lhs, rhs = n.Lhs, n.Rhs
	case *ast.ValueSpec:
		for _, id := range n.Names {
			lhs = append(lhs, id)
		}
		rhs = n.Values
	default:
		return state
	}
	values := make([]byte, len(lhs))
	for i := range lhs {
		values[i] = connectionUnknown
		if len(rhs) == len(lhs) {
			values[i] = c.source(rhs[i], state, info)
		} else if len(rhs) == 1 && i == 0 {
			values[i] = c.source(rhs[0], state, info)
		}
	}
	for i, expr := range lhs {
		if id, ok := expr.(*ast.Ident); ok {
			state = c.set(state, objectOf(id, info), values[i])
		}
	}
	return state
}

// nonTransactionConnection 判断 call 是否在非事务连接上执行了数据库操作
// （database/sql 的 DB、Conn、非事务 Stmt，或接收者不是事务的 gorm.DB）。
func nonTransactionConnection(call *ast.CallExpr, fn *types.Func, info *types.Info, tracker *connectionTracker, state string) bool {
	if fn.Pkg() == nil {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return false
	}
	path, name := namedType(sig.Recv().Type())
	switch path {
	case "database/sql":
		switch fn.Name() {
		case "Exec", "ExecContext", "Query", "QueryContext", "QueryRow", "QueryRowContext",
			"Prepare", "PrepareContext", "Ping", "PingContext", "Raw", "Conn":
			switch name {
			case "DB", "Conn":
				return true
			case "Stmt":
				return tracker.source(receiver(call, info), state, info)&connectionNonTx != 0
			}
		}
	case "gorm.io/gorm":
		if name != "DB" {
			return false
		}
		switch fn.Name() {
		case "Create", "CreateInBatches", "Save", "Update", "Updates", "UpdateColumn", "UpdateColumns",
			"Delete", "Exec", "First", "Last", "Take", "Find", "FindInBatches", "Scan", "Pluck",
			"Count", "Row", "Rows", "AutoMigrate", "Migrator", "Connection", "DB":
			return tracker.source(receiver(call, info), state, info) != connectionTx
		}
	}
	return false
}
