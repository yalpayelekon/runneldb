package runneldb

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

type parser struct {
	l    *lexer
	tok  token
	peek token
	hasP bool
	nArg int
}

func parseSQL(src string) (stmt, error) {
	p := &parser{l: newLexer(src)}
	if err := p.advance(); err != nil {
		return nil, err
	}
	st, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	if p.tok.kind == tokSemi {
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if p.tok.kind != tokEOF {
		return nil, fmt.Errorf("%w: unexpected token %q at %d", ErrSQL, p.tok.lit, p.tok.pos)
	}
	return st, nil
}

func (p *parser) advance() error {
	if p.hasP {
		p.tok = p.peek
		p.hasP = false
		return nil
	}
	t, err := p.l.next()
	if err != nil {
		return err
	}
	p.tok = t
	return nil
}

func (p *parser) expectKeyword(kw string) error {
	if p.tok.kind != tokKeyword || p.tok.kw != kw {
		return fmt.Errorf("%w: expected %s at %d", ErrSQL, kw, p.tok.pos)
	}
	return p.advance()
}

func (p *parser) expect(kind tokenKind, label string) error {
	if p.tok.kind != kind {
		return fmt.Errorf("%w: expected %s at %d", ErrSQL, label, p.tok.pos)
	}
	return p.advance()
}

func (p *parser) parseStmt() (stmt, error) {
	if p.tok.kind != tokKeyword {
		return nil, fmt.Errorf("%w: expected statement at %d", ErrSQL, p.tok.pos)
	}
	switch p.tok.kw {
	case "CREATE":
		return p.parseCreate()
	case "DROP":
		return p.parseDrop()
	case "INSERT":
		return p.parseInsert()
	case "SELECT":
		return p.parseSelect()
	case "UPDATE":
		return p.parseUpdate()
	case "DELETE":
		return p.parseDelete()
	default:
		return nil, fmt.Errorf("%w: unsupported statement %s", ErrSQL, p.tok.kw)
	}
}

func (p *parser) parseCreate() (stmt, error) {
	if err := p.expectKeyword("CREATE"); err != nil {
		return nil, err
	}
	if p.tok.kind == tokKeyword && p.tok.kw == "INDEX" {
		return p.parseCreateIndex()
	}
	if err := p.expectKeyword("TABLE"); err != nil {
		return nil, err
	}
	name, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expect(tokLParen, "("); err != nil {
		return nil, err
	}
	var cols []ColumnDef
	pkCount := 0
	for {
		col, err := p.parseColumnDef()
		if err != nil {
			return nil, err
		}
		if col.PK {
			pkCount++
		}
		cols = append(cols, col)
		if p.tok.kind == tokComma {
			if err := p.advance(); err != nil {
				return nil, err
			}
			continue
		}
		break
	}
	if err := p.expect(tokRParen, ")"); err != nil {
		return nil, err
	}
	if pkCount != 1 {
		return nil, fmt.Errorf("%w: CREATE TABLE requires exactly one PRIMARY KEY", ErrSQL)
	}
	return createTableStmt{name: name, columns: cols}, nil
}

func (p *parser) parseColumnDef() (ColumnDef, error) {
	name, err := p.parseIdent()
	if err != nil {
		return ColumnDef{}, err
	}
	if p.tok.kind != tokKeyword {
		return ColumnDef{}, fmt.Errorf("%w: expected type at %d", ErrSQL, p.tok.pos)
	}
	var typ ColType
	switch p.tok.kw {
	case "INTEGER":
		typ = TypeInteger
	case "TEXT":
		typ = TypeText
	case "BLOB":
		typ = TypeBlob
	case "JSON":
		typ = TypeJSON
	default:
		return ColumnDef{}, fmt.Errorf("%w: unknown type %s", ErrSQL, p.tok.kw)
	}
	if err := p.advance(); err != nil {
		return ColumnDef{}, err
	}
	pk := false
	if p.tok.kind == tokKeyword && p.tok.kw == "PRIMARY" {
		if err := p.advance(); err != nil {
			return ColumnDef{}, err
		}
		if err := p.expectKeyword("KEY"); err != nil {
			return ColumnDef{}, err
		}
		pk = true
	}
	if pk && typ != TypeInteger && typ != TypeText {
		return ColumnDef{}, fmt.Errorf("%w: PRIMARY KEY must be INTEGER or TEXT", ErrSQL)
	}
	return ColumnDef{Name: name, Type: typ, PK: pk}, nil
}

func (p *parser) parseCreateIndex() (stmt, error) {
	if err := p.expectKeyword("INDEX"); err != nil {
		return nil, err
	}
	name, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("ON"); err != nil {
		return nil, err
	}
	table, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expect(tokLParen, "("); err != nil {
		return nil, err
	}
	col, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	if p.tok.kind == tokComma {
		return nil, fmt.Errorf("%w: multi-column indexes are not supported", ErrSQL)
	}
	if err := p.expect(tokRParen, ")"); err != nil {
		return nil, err
	}
	path := ""
	if p.tok.kind == tokKeyword && p.tok.kw == "PATH" {
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.tok.kind != tokString {
			return nil, fmt.Errorf("%w: expected path string at %d", ErrSQL, p.tok.pos)
		}
		path = p.tok.lit
		if err := p.advance(); err != nil {
			return nil, err
		}
		if _, err := parseJSONPath(path); err != nil {
			return nil, err
		}
	}
	return createIndexStmt{index: name, table: table, column: col, path: path}, nil
}

func (p *parser) parseDrop() (stmt, error) {
	if err := p.expectKeyword("DROP"); err != nil {
		return nil, err
	}
	if p.tok.kind == tokKeyword && p.tok.kw == "INDEX" {
		return p.parseDropIndex()
	}
	if err := p.expectKeyword("TABLE"); err != nil {
		return nil, err
	}
	name, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	return dropTableStmt{name: name}, nil
}

func (p *parser) parseDropIndex() (stmt, error) {
	if err := p.expectKeyword("INDEX"); err != nil {
		return nil, err
	}
	name, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	return dropIndexStmt{index: name}, nil
}

func (p *parser) parseInsert() (stmt, error) {
	if err := p.expectKeyword("INSERT"); err != nil {
		return nil, err
	}
	if err := p.expectKeyword("INTO"); err != nil {
		return nil, err
	}
	table, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	var cols []string
	if p.tok.kind == tokLParen {
		if err := p.advance(); err != nil {
			return nil, err
		}
		for {
			c, err := p.parseIdent()
			if err != nil {
				return nil, err
			}
			cols = append(cols, c)
			if p.tok.kind == tokComma {
				if err := p.advance(); err != nil {
					return nil, err
				}
				continue
			}
			break
		}
		if err := p.expect(tokRParen, ")"); err != nil {
			return nil, err
		}
	}
	if err := p.expectKeyword("VALUES"); err != nil {
		return nil, err
	}
	if err := p.expect(tokLParen, "("); err != nil {
		return nil, err
	}
	var vals []expr
	for {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		vals = append(vals, e)
		if p.tok.kind == tokComma {
			if err := p.advance(); err != nil {
				return nil, err
			}
			continue
		}
		break
	}
	if err := p.expect(tokRParen, ")"); err != nil {
		return nil, err
	}
	return insertStmt{table: table, columns: cols, values: vals}, nil
}

func (p *parser) parseSelect() (stmt, error) {
	if err := p.expectKeyword("SELECT"); err != nil {
		return nil, err
	}
	var cols []selectItem
	if p.tok.kind == tokStar {
		cols = []selectItem{{kind: selectStar}}
		if err := p.advance(); err != nil {
			return nil, err
		}
	} else {
		for {
			item, err := p.parseSelectItem()
			if err != nil {
				return nil, err
			}
			cols = append(cols, item)
			if p.tok.kind == tokComma {
				if err := p.advance(); err != nil {
					return nil, err
				}
				continue
			}
			break
		}
	}
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	table, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	where, err := p.parseWhereOptional()
	if err != nil {
		return nil, err
	}
	return selectStmt{table: table, columns: cols, where: where}, nil
}

func (p *parser) parseSelectItem() (selectItem, error) {
	if p.tok.kind == tokKeyword && p.tok.kw == "JSON_EXTRACT" {
		col, path, err := p.parseJSONExtractCall()
		if err != nil {
			return selectItem{}, err
		}
		return selectItem{kind: selectExtract, column: col, path: path}, nil
	}
	c, err := p.parseIdent()
	if err != nil {
		return selectItem{}, err
	}
	return selectItem{kind: selectColumn, column: c}, nil
}

func (p *parser) parseJSONExtractCall() (col, path string, err error) {
	if err := p.expectKeyword("JSON_EXTRACT"); err != nil {
		return "", "", err
	}
	if err := p.expect(tokLParen, "("); err != nil {
		return "", "", err
	}
	col, err = p.parseIdent()
	if err != nil {
		return "", "", err
	}
	if err := p.expect(tokComma, ","); err != nil {
		return "", "", err
	}
	if p.tok.kind != tokString {
		return "", "", fmt.Errorf("%w: expected path string at %d", ErrSQL, p.tok.pos)
	}
	path = p.tok.lit
	if err := p.advance(); err != nil {
		return "", "", err
	}
	if _, err := parseJSONPath(path); err != nil {
		return "", "", err
	}
	if err := p.expect(tokRParen, ")"); err != nil {
		return "", "", err
	}
	return col, path, nil
}

func (p *parser) parseUpdate() (stmt, error) {
	if err := p.expectKeyword("UPDATE"); err != nil {
		return nil, err
	}
	table, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("SET"); err != nil {
		return nil, err
	}
	var sets []assign
	for {
		col, err := p.parseIdent()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tokEq, "="); err != nil {
			return nil, err
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		sets = append(sets, assign{column: col, value: e})
		if p.tok.kind == tokComma {
			if err := p.advance(); err != nil {
				return nil, err
			}
			continue
		}
		break
	}
	where, err := p.parseWhereOptional()
	if err != nil {
		return nil, err
	}
	return updateStmt{table: table, sets: sets, where: where}, nil
}

func (p *parser) parseDelete() (stmt, error) {
	if err := p.expectKeyword("DELETE"); err != nil {
		return nil, err
	}
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	table, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	where, err := p.parseWhereOptional()
	if err != nil {
		return nil, err
	}
	return deleteStmt{table: table, where: where}, nil
}

func (p *parser) parseWhereOptional() ([]pred, error) {
	if p.tok.kind != tokKeyword || p.tok.kw != "WHERE" {
		return nil, nil
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	var preds []pred
	for {
		pr, err := p.parsePred()
		if err != nil {
			return nil, err
		}
		preds = append(preds, pr)
		if p.tok.kind == tokKeyword && p.tok.kw == "AND" {
			if err := p.advance(); err != nil {
				return nil, err
			}
			continue
		}
		break
	}
	return preds, nil
}

func (p *parser) parsePred() (pred, error) {
	if p.tok.kind == tokKeyword && p.tok.kw == "JSON_EXTRACT" {
		col, path, err := p.parseJSONExtractCall()
		if err != nil {
			return pred{}, err
		}
		if err := p.expect(tokEq, "="); err != nil {
			return pred{}, err
		}
		e, err := p.parseExpr()
		if err != nil {
			return pred{}, err
		}
		return pred{kind: predExtract, column: col, path: path, value: e}, nil
	}
	col, err := p.parseIdent()
	if err != nil {
		return pred{}, err
	}
	if err := p.expect(tokEq, "="); err != nil {
		return pred{}, err
	}
	e, err := p.parseExpr()
	if err != nil {
		return pred{}, err
	}
	return pred{kind: predColumn, column: col, value: e}, nil
}

func (p *parser) parseExpr() (expr, error) {
	switch p.tok.kind {
	case tokParam:
		idx := p.nArg
		p.nArg++
		if err := p.advance(); err != nil {
			return expr{}, err
		}
		return expr{kind: exprParam, param: idx}, nil
	case tokNumber:
		v, err := strconv.ParseInt(p.tok.lit, 10, 64)
		if err != nil {
			return expr{}, fmt.Errorf("%w: bad integer %q", ErrSQL, p.tok.lit)
		}
		if err := p.advance(); err != nil {
			return expr{}, err
		}
		return expr{kind: exprInt, i: v}, nil
	case tokString:
		s := p.tok.lit
		if err := p.advance(); err != nil {
			return expr{}, err
		}
		return expr{kind: exprText, s: s}, nil
	case tokBlob:
		b, err := hex.DecodeString(p.tok.lit)
		if err != nil {
			return expr{}, fmt.Errorf("%w: bad blob hex", ErrSQL)
		}
		if err := p.advance(); err != nil {
			return expr{}, err
		}
		return expr{kind: exprBlob, blob: b}, nil
	case tokKeyword:
		if p.tok.kw == "NULL" {
			if err := p.advance(); err != nil {
				return expr{}, err
			}
			return expr{kind: exprNull}, nil
		}
	}
	return expr{}, fmt.Errorf("%w: expected expression at %d", ErrSQL, p.tok.pos)
}

func (p *parser) parseIdent() (string, error) {
	switch p.tok.kind {
	case tokIdent:
		s := p.tok.lit
		if err := p.advance(); err != nil {
			return "", err
		}
		return s, nil
	case tokKeyword:
		// allow keywords as identifiers in some positions
		s := p.tok.lit
		if err := p.advance(); err != nil {
			return "", err
		}
		return s, nil
	default:
		return "", fmt.Errorf("%w: expected identifier at %d", ErrSQL, p.tok.pos)
	}
}

func normalizeName(s string) string {
	return strings.ToLower(s)
}
