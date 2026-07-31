package runneldb

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	jNull   byte = 0
	jFalse  byte = 1
	jTrue   byte = 2
	jInt    byte = 3
	jFloat  byte = 4
	jString byte = 5
	jArray  byte = 6
	jObject byte = 7

	jsonMaxDepth = 32
)

// encodeJSON parses JSON text and encodes it into the binary format.
func encodeJSON(text []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(text)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON: %v", ErrSQL, err)
	}
	var out []byte
	out, err := appendJSONBin(out, v, 0)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// decodeJSON decodes binary JSON into a Go value suitable for re-marshaling.
func decodeJSON(bin []byte) (any, error) {
	v, n, err := readJSONBin(bin, 0)
	if err != nil {
		return nil, err
	}
	if n != len(bin) {
		return nil, fmt.Errorf("%w: trailing bytes in binary JSON", ErrSQL)
	}
	return v, nil
}

// jsonToText re-serializes binary JSON as text.
func jsonToText(bin []byte) (string, error) {
	v, err := decodeJSON(bin)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSQL, err)
	}
	return string(b), nil
}

// parseJSONPath validates and splits a path of the form $.a.b into segments.
func parseJSONPath(path string) ([]string, error) {
	if path == "" || path[0] != '$' {
		return nil, fmt.Errorf("%w: JSON path must start with $", ErrSQL)
	}
	if path == "$" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "$.") {
		return nil, fmt.Errorf("%w: JSON path must use $.ident segments", ErrSQL)
	}
	rest := path[2:]
	if rest == "" {
		return nil, fmt.Errorf("%w: empty JSON path segment", ErrSQL)
	}
	parts := strings.Split(rest, ".")
	for _, p := range parts {
		if p == "" || !isJSONIdent(p) {
			return nil, fmt.Errorf("%w: invalid JSON path segment %q", ErrSQL, p)
		}
		if strings.ContainsAny(p, "[]*") {
			return nil, fmt.Errorf("%w: JSON path wildcards and array indexes are not supported", ErrSQL)
		}
	}
	return parts, nil
}

func isJSONIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// jsonExtract walks binary JSON along path and returns a SQL Value.
// ok is false when the path is missing. Arrays/objects at the leaf are returned as TEXT JSON.
func jsonExtract(bin []byte, path string) (Value, bool, error) {
	segs, err := parseJSONPath(path)
	if err != nil {
		return Value{}, false, err
	}
	v, _, err := readJSONBin(bin, 0)
	if err != nil {
		return Value{}, false, err
	}
	cur := v
	for _, seg := range segs {
		obj, ok := cur.(map[string]any)
		if !ok {
			return Value{}, false, nil
		}
		next, ok := obj[seg]
		if !ok {
			return Value{}, false, nil
		}
		cur = next
	}
	return jsonLeafToValue(cur)
}

// indexableExtract returns INTEGER/TEXT for path-index keys; ok=false if missing or non-indexable.
func indexableExtract(bin []byte, path string) (Value, bool, error) {
	v, ok, err := jsonExtract(bin, path)
	if err != nil || !ok {
		return Value{}, false, err
	}
	if v.Null {
		return Value{}, false, nil
	}
	switch v.Typ {
	case TypeInteger, TypeText:
		return v, true, nil
	default:
		return Value{}, false, nil
	}
}

func jsonLeafToValue(v any) (Value, bool, error) {
	if v == nil {
		return Value{Null: true}, true, nil
	}
	switch x := v.(type) {
	case bool:
		if x {
			return Value{Typ: TypeText, Text: "true"}, true, nil
		}
		return Value{Typ: TypeText, Text: "false"}, true, nil
	case int64:
		return Value{Typ: TypeInteger, Int: x}, true, nil
	case float64:
		if x == math.Trunc(x) && x >= math.MinInt64 && x <= math.MaxInt64 {
			return Value{Typ: TypeInteger, Int: int64(x)}, true, nil
		}
		return Value{Typ: TypeText, Text: formatFloat(x)}, true, nil
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return Value{Typ: TypeInteger, Int: i}, true, nil
		}
		if f, err := x.Float64(); err == nil {
			if f == math.Trunc(f) && f >= math.MinInt64 && f <= math.MaxInt64 {
				return Value{Typ: TypeInteger, Int: int64(f)}, true, nil
			}
			return Value{Typ: TypeText, Text: x.String()}, true, nil
		}
		return Value{Typ: TypeText, Text: x.String()}, true, nil
	case string:
		return Value{Typ: TypeText, Text: x}, true, nil
	case map[string]any, []any:
		b, err := json.Marshal(x)
		if err != nil {
			return Value{}, false, fmt.Errorf("%w: %v", ErrSQL, err)
		}
		return Value{Typ: TypeText, Text: string(b)}, true, nil
	default:
		return Value{}, false, fmt.Errorf("%w: unsupported JSON value type %T", ErrSQL, v)
	}
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func appendJSONBin(out []byte, v any, depth int) ([]byte, error) {
	if depth > jsonMaxDepth {
		return nil, fmt.Errorf("%w: JSON nesting too deep", ErrSQL)
	}
	if v == nil {
		return append(out, jNull), nil
	}
	switch x := v.(type) {
	case bool:
		if x {
			return append(out, jTrue), nil
		}
		return append(out, jFalse), nil
	case json.Number:
		if i, err := x.Int64(); err == nil {
			out = append(out, jInt)
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], uint64(i))
			return append(out, buf[:]...), nil
		}
		f, err := x.Float64()
		if err != nil {
			return nil, fmt.Errorf("%w: bad JSON number", ErrSQL)
		}
		out = append(out, jFloat)
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(f))
		return append(out, buf[:]...), nil
	case float64:
		if x == math.Trunc(x) && x >= math.MinInt64 && x <= math.MaxInt64 {
			out = append(out, jInt)
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], uint64(int64(x)))
			return append(out, buf[:]...), nil
		}
		out = append(out, jFloat)
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(x))
		return append(out, buf[:]...), nil
	case string:
		b := []byte(x)
		out = append(out, jString)
		var hdr [4]byte
		binary.LittleEndian.PutUint32(hdr[:], uint32(len(b)))
		out = append(out, hdr[:]...)
		return append(out, b...), nil
	case []any:
		out = append(out, jArray)
		var hdr [4]byte
		binary.LittleEndian.PutUint32(hdr[:], uint32(len(x)))
		out = append(out, hdr[:]...)
		for _, el := range x {
			var err error
			out, err = appendJSONBin(out, el, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		seen := make(map[string]struct{}, len(x))
		for k := range x {
			if _, ok := seen[k]; ok {
				return nil, fmt.Errorf("%w: duplicate JSON object key %q", ErrSQL, k)
			}
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
		// Stable order for determinism.
		sort.Strings(keys)
		out = append(out, jObject)
		var hdr [4]byte
		binary.LittleEndian.PutUint32(hdr[:], uint32(len(keys)))
		out = append(out, hdr[:]...)
		for _, k := range keys {
			kb := []byte(k)
			var kh [4]byte
			binary.LittleEndian.PutUint32(kh[:], uint32(len(kb)))
			out = append(out, kh[:]...)
			out = append(out, kb...)
			var err error
			out, err = appendJSONBin(out, x[k], depth+1)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: unsupported JSON type %T", ErrSQL, v)
	}
}

func readJSONBin(data []byte, depth int) (any, int, error) {
	if depth > jsonMaxDepth {
		return nil, 0, fmt.Errorf("%w: JSON nesting too deep", ErrSQL)
	}
	if len(data) < 1 {
		return nil, 0, fmt.Errorf("%w: truncated binary JSON", ErrSQL)
	}
	switch data[0] {
	case jNull:
		return nil, 1, nil
	case jFalse:
		return false, 1, nil
	case jTrue:
		return true, 1, nil
	case jInt:
		if len(data) < 9 {
			return nil, 0, fmt.Errorf("%w: truncated JSON int", ErrSQL)
		}
		return int64(binary.LittleEndian.Uint64(data[1:9])), 9, nil
	case jFloat:
		if len(data) < 9 {
			return nil, 0, fmt.Errorf("%w: truncated JSON float", ErrSQL)
		}
		bits := binary.LittleEndian.Uint64(data[1:9])
		return math.Float64frombits(bits), 9, nil
	case jString:
		if len(data) < 5 {
			return nil, 0, fmt.Errorf("%w: truncated JSON string header", ErrSQL)
		}
		n := int(binary.LittleEndian.Uint32(data[1:5]))
		if len(data) < 5+n {
			return nil, 0, fmt.Errorf("%w: truncated JSON string", ErrSQL)
		}
		return string(data[5 : 5+n]), 5 + n, nil
	case jArray:
		if len(data) < 5 {
			return nil, 0, fmt.Errorf("%w: truncated JSON array header", ErrSQL)
		}
		n := int(binary.LittleEndian.Uint32(data[1:5]))
		off := 5
		arr := make([]any, 0, n)
		for i := 0; i < n; i++ {
			el, used, err := readJSONBin(data[off:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			arr = append(arr, el)
			off += used
		}
		return arr, off, nil
	case jObject:
		if len(data) < 5 {
			return nil, 0, fmt.Errorf("%w: truncated JSON object header", ErrSQL)
		}
		n := int(binary.LittleEndian.Uint32(data[1:5]))
		off := 5
		obj := make(map[string]any, n)
		for i := 0; i < n; i++ {
			if len(data) < off+4 {
				return nil, 0, fmt.Errorf("%w: truncated JSON key header", ErrSQL)
			}
			kn := int(binary.LittleEndian.Uint32(data[off : off+4]))
			off += 4
			if len(data) < off+kn {
				return nil, 0, fmt.Errorf("%w: truncated JSON key", ErrSQL)
			}
			key := string(data[off : off+kn])
			off += kn
			if _, exists := obj[key]; exists {
				return nil, 0, fmt.Errorf("%w: duplicate JSON object key %q", ErrSQL, key)
			}
			val, used, err := readJSONBin(data[off:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			obj[key] = val
			off += used
		}
		return obj, off, nil
	default:
		return nil, 0, fmt.Errorf("%w: unknown JSON tag %d", ErrSQL, data[0])
	}
}
