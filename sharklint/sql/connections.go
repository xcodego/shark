package sql

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

type connectionTracker struct {
	indices map[types.Object]int
}

const (
	connectionTx byte = 1 << iota
	connectionNonTx
	connectionUnknown
)

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

func (c *connectionTracker) empty() string {
	return strings.Repeat("\x04", len(c.indices))
}

func (c *connectionTracker) join(a, b string) string {
	joined := []byte(a)
	for i := range joined {
		joined[i] |= b[i]
	}
	return string(joined)
}

func (c *connectionTracker) set(state string, obj types.Object, value byte) string {
	index, ok := c.indices[obj]
	if !ok {
		return state
	}
	updated := []byte(state)
	updated[index] = value
	return string(updated)
}

func (c *connectionTracker) get(state string, obj types.Object) byte {
	if index, ok := c.indices[obj]; ok {
		return state[index]
	}
	return connectionUnknown
}

func (c *connectionTracker) isTransaction(obj types.Object, state string) bool {
	path, name := namedType(obj.Type())
	return path == "database/sql" && name == "Tx" || c.get(state, obj) == connectionTx
}

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
