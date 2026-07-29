package runneldb

// AST nodes for the minimal SQL dialect.

type stmt interface {
	stmtNode()
}

type createTableStmt struct {
	name    string
	columns []ColumnDef
}

func (createTableStmt) stmtNode() {}

type dropTableStmt struct {
	name string
}

func (dropTableStmt) stmtNode() {}

type insertStmt struct {
	table   string
	columns []string // empty => all columns in order
	values  []expr
}

func (insertStmt) stmtNode() {}

type selectStmt struct {
	table   string
	columns []string // empty or ["*"] means all
	where   []pred
}

func (selectStmt) stmtNode() {}

type updateStmt struct {
	table string
	sets  []assign
	where []pred
}

func (updateStmt) stmtNode() {}

type deleteStmt struct {
	table string
	where []pred
}

func (deleteStmt) stmtNode() {}

type assign struct {
	column string
	value  expr
}

type pred struct {
	column string
	value  expr
}

type exprKind int

const (
	exprNull exprKind = iota
	exprInt
	exprText
	exprBlob
	exprParam
)

type expr struct {
	kind  exprKind
	i     int64
	s     string
	blob  []byte
	param int // 0-based arg index assigned during bind
}

type createIndexStmt struct {
	index  string
	table  string
	column string
}

func (createIndexStmt) stmtNode() {}

type dropIndexStmt struct {
	index string
}

func (dropIndexStmt) stmtNode() {}
