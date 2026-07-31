package runneldb

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokNumber
	tokString
	tokBlob
	tokParam // ?
	tokComma
	tokSemi
	tokLParen
	tokRParen
	tokStar
	tokEq
	tokKeyword
)

type token struct {
	kind tokenKind
	lit  string
	pos  int
	kw   string // upper keyword when kind==tokKeyword or ident matched as kw
}

type lexer struct {
	src string
	i   int
	pos int
}

func newLexer(src string) *lexer {
	return &lexer{src: src}
}

func (l *lexer) next() (token, error) {
	l.skipSpace()
	if l.i >= len(l.src) {
		return token{kind: tokEOF, pos: l.pos}, nil
	}
	start := l.i
	l.pos = start
	ch, w := utf8.DecodeRuneInString(l.src[l.i:])

	switch ch {
	case ',':
		l.i += w
		return token{kind: tokComma, lit: ",", pos: start}, nil
	case ';':
		l.i += w
		return token{kind: tokSemi, lit: ";", pos: start}, nil
	case '(':
		l.i += w
		return token{kind: tokLParen, lit: "(", pos: start}, nil
	case ')':
		l.i += w
		return token{kind: tokRParen, lit: ")", pos: start}, nil
	case '*':
		l.i += w
		return token{kind: tokStar, lit: "*", pos: start}, nil
	case '=':
		l.i += w
		return token{kind: tokEq, lit: "=", pos: start}, nil
	case '?':
		l.i += w
		return token{kind: tokParam, lit: "?", pos: start}, nil
	case '\'':
		return l.lexString(start)
	case 'X', 'x':
		if l.i+1 < len(l.src) && l.src[l.i+1] == '\'' {
			l.i++ // skip X
			return l.lexBlob(start)
		}
	}

	if ch == '-' || unicode.IsDigit(ch) {
		return l.lexNumber(start)
	}
	if ch == '_' || unicode.IsLetter(ch) {
		return l.lexIdent(start)
	}
	return token{}, fmt.Errorf("%w: unexpected character %q at %d", ErrSQL, string(ch), start)
}

func (l *lexer) skipSpace() {
	for l.i < len(l.src) {
		ch, w := utf8.DecodeRuneInString(l.src[l.i:])
		if !unicode.IsSpace(ch) {
			return
		}
		l.i += w
	}
}

func (l *lexer) lexString(start int) (token, error) {
	l.i++ // opening quote
	var b strings.Builder
	for l.i < len(l.src) {
		ch, w := utf8.DecodeRuneInString(l.src[l.i:])
		if ch == '\'' {
			if l.i+1 < len(l.src) && l.src[l.i+1] == '\'' {
				b.WriteByte('\'')
				l.i += 2
				continue
			}
			l.i += w
			return token{kind: tokString, lit: b.String(), pos: start}, nil
		}
		b.WriteRune(ch)
		l.i += w
	}
	return token{}, fmt.Errorf("%w: unterminated string at %d", ErrSQL, start)
}

func (l *lexer) lexBlob(start int) (token, error) {
	// X'started after X, still need opening quote
	if l.i >= len(l.src) || l.src[l.i] != '\'' {
		return token{}, fmt.Errorf("%w: bad blob literal at %d", ErrSQL, start)
	}
	l.i++
	var hex strings.Builder
	for l.i < len(l.src) {
		ch := l.src[l.i]
		if ch == '\'' {
			l.i++
			return token{kind: tokBlob, lit: hex.String(), pos: start}, nil
		}
		hex.WriteByte(ch)
		l.i++
	}
	return token{}, fmt.Errorf("%w: unterminated blob at %d", ErrSQL, start)
}

func (l *lexer) lexNumber(start int) (token, error) {
	if l.src[l.i] == '-' {
		l.i++
	}
	for l.i < len(l.src) && unicode.IsDigit(rune(l.src[l.i])) {
		l.i++
	}
	return token{kind: tokNumber, lit: l.src[start:l.i], pos: start}, nil
}

func (l *lexer) lexIdent(start int) (token, error) {
	for l.i < len(l.src) {
		ch, w := utf8.DecodeRuneInString(l.src[l.i:])
		if ch == '_' || unicode.IsLetter(ch) || unicode.IsDigit(ch) {
			l.i += w
			continue
		}
		break
	}
	lit := l.src[start:l.i]
	upper := strings.ToUpper(lit)
	if isKeyword(upper) {
		return token{kind: tokKeyword, lit: lit, kw: upper, pos: start}, nil
	}
	return token{kind: tokIdent, lit: lit, pos: start}, nil
}

func isKeyword(s string) bool {
	switch s {
	case "CREATE", "TABLE", "DROP", "INSERT", "INTO", "VALUES", "SELECT", "FROM",
		"UPDATE", "SET", "DELETE", "WHERE", "AND", "PRIMARY", "KEY",
		"INTEGER", "TEXT", "BLOB", "JSON", "NULL", "INDEX", "ON", "PATH", "JSON_EXTRACT":
		return true
	default:
		return false
	}
}
