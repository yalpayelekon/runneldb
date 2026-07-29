package runneldb

import (
	"encoding/binary"
	"fmt"
)

// Value is a typed SQL value.
type Value struct {
	Typ  ColType
	Int  int64
	Text string
	Blob []byte
	Null bool
}

func (v Value) encode() []byte {
	if v.Null {
		return []byte{0}
	}
	switch v.Typ {
	case TypeInteger:
		buf := make([]byte, 1+8)
		buf[0] = 1
		binary.LittleEndian.PutUint64(buf[1:], uint64(v.Int))
		return buf
	case TypeText:
		b := []byte(v.Text)
		buf := make([]byte, 1+4+len(b))
		buf[0] = 2
		binary.LittleEndian.PutUint32(buf[1:5], uint32(len(b)))
		copy(buf[5:], b)
		return buf
	case TypeBlob:
		buf := make([]byte, 1+4+len(v.Blob))
		buf[0] = 3
		binary.LittleEndian.PutUint32(buf[1:5], uint32(len(v.Blob)))
		copy(buf[5:], v.Blob)
		return buf
	default:
		return []byte{0}
	}
}

func decodeValue(data []byte) (Value, int, error) {
	if len(data) < 1 {
		return Value{}, 0, fmt.Errorf("%w: truncated value", ErrSQL)
	}
	switch data[0] {
	case 0:
		return Value{Null: true}, 1, nil
	case 1:
		if len(data) < 9 {
			return Value{}, 0, fmt.Errorf("%w: truncated int", ErrSQL)
		}
		return Value{Typ: TypeInteger, Int: int64(binary.LittleEndian.Uint64(data[1:9]))}, 9, nil
	case 2:
		if len(data) < 5 {
			return Value{}, 0, fmt.Errorf("%w: truncated text header", ErrSQL)
		}
		n := int(binary.LittleEndian.Uint32(data[1:5]))
		if len(data) < 5+n {
			return Value{}, 0, fmt.Errorf("%w: truncated text", ErrSQL)
		}
		return Value{Typ: TypeText, Text: string(data[5 : 5+n])}, 5 + n, nil
	case 3:
		if len(data) < 5 {
			return Value{}, 0, fmt.Errorf("%w: truncated blob header", ErrSQL)
		}
		n := int(binary.LittleEndian.Uint32(data[1:5]))
		if len(data) < 5+n {
			return Value{}, 0, fmt.Errorf("%w: truncated blob", ErrSQL)
		}
		b := append([]byte(nil), data[5:5+n]...)
		return Value{Typ: TypeBlob, Blob: b}, 5 + n, nil
	default:
		return Value{}, 0, fmt.Errorf("%w: unknown value tag %d", ErrSQL, data[0])
	}
}

func encodeRow(values []Value) []byte {
	var out []byte
	hdr := make([]byte, 2)
	binary.LittleEndian.PutUint16(hdr, uint16(len(values)))
	out = append(out, hdr...)
	for _, v := range values {
		out = append(out, v.encode()...)
	}
	return out
}

func decodeRow(data []byte) ([]Value, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: truncated row", ErrSQL)
	}
	n := int(binary.LittleEndian.Uint16(data[:2]))
	rest := data[2:]
	out := make([]Value, 0, n)
	for i := 0; i < n; i++ {
		v, used, err := decodeValue(rest)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		rest = rest[used:]
	}
	return out, nil
}

func packRow(values []Value) ([]byte, error) {
	payload := encodeRow(values)
	return EncodeRowPage(payload)
}

func unpackRow(pageBytes []byte) ([]Value, error) {
	payload, err := DecodeRowPage(pageBytes)
	if err != nil {
		return nil, err
	}
	return decodeRow(payload)
}
