package runneldb

import "testing"

func FuzzJSONBin(f *testing.F) {
	seeds := []string{
		`null`, `true`, `0`, `"x"`, `[]`, `{}`,
		`{"a":1}`, `{"a":{"b":"c"}}`, `[1,2,3]`,
		`{"name":"ada","n":42}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		bin, err := encodeJSON(data)
		if err != nil {
			return
		}
		v, err := decodeJSON(bin)
		if err != nil {
			t.Fatalf("decode after encode: %v", err)
		}
		text, err := jsonToText(bin)
		if err != nil {
			t.Fatalf("toText: %v", err)
		}
		bin2, err := encodeJSON([]byte(text))
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		_ = v
		_, _, _ = jsonExtract(bin2, "$.a")
		_, _, _ = jsonExtract(bin2, "$.a.b")
	})
}
