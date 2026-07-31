package runneldb

import (
	"bytes"
	"testing"
)

func TestJSONBinRoundTrip(t *testing.T) {
	cases := []string{
		`null`,
		`true`,
		`false`,
		`42`,
		`-7`,
		`3.5`,
		`"hello"`,
		`[]`,
		`{}`,
		`{"a":1,"b":"x"}`,
		`{"nested":{"x":true},"arr":[1,2,"z"]}`,
	}
	for _, c := range cases {
		bin, err := encodeJSON([]byte(c))
		if err != nil {
			t.Fatalf("%s encode: %v", c, err)
		}
		text, err := jsonToText(bin)
		if err != nil {
			t.Fatalf("%s toText: %v", c, err)
		}
		// Re-encode text and compare structural equality via decode.
		bin2, err := encodeJSON([]byte(text))
		if err != nil {
			t.Fatalf("%s re-encode: %v", c, err)
		}
		if !bytes.Equal(bin, bin2) {
			t.Fatalf("%s: binary not stable after round-trip\n got %v\nwant %v\ntext %s", c, bin, bin2, text)
		}
	}
}

func TestJSONExtract(t *testing.T) {
	bin, err := encodeJSON([]byte(`{"name":"ada","age":36,"meta":{"ok":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	v, ok, err := jsonExtract(bin, "$.name")
	if err != nil || !ok || v.Text != "ada" {
		t.Fatalf("name: %#v ok=%v err=%v", v, ok, err)
	}
	v, ok, err = jsonExtract(bin, "$.age")
	if err != nil || !ok || v.Int != 36 {
		t.Fatalf("age: %#v ok=%v err=%v", v, ok, err)
	}
	v, ok, err = jsonExtract(bin, "$.meta.ok")
	if err != nil || !ok || v.Text != "true" {
		t.Fatalf("meta.ok: %#v ok=%v err=%v", v, ok, err)
	}
	_, ok, err = jsonExtract(bin, "$.missing")
	if err != nil || ok {
		t.Fatalf("missing should be absent, ok=%v err=%v", ok, err)
	}
}

func TestJSONPathRejects(t *testing.T) {
	bad := []string{"", "name", "$[", "$.a[0]", "$.*", "$..a", "$."}
	for _, p := range bad {
		if _, err := parseJSONPath(p); err == nil {
			t.Fatalf("expected error for path %q", p)
		}
	}
}

func TestJSONDepthLimit(t *testing.T) {
	s := "{"
	for i := 0; i < jsonMaxDepth+2; i++ {
		s += `"a":{`
	}
	s += `"x":1`
	for i := 0; i < jsonMaxDepth+2; i++ {
		s += "}"
	}
	s += "}"
	_, err := encodeJSON([]byte(s))
	if err == nil {
		t.Fatal("expected depth error")
	}
}

func TestInvalidJSON(t *testing.T) {
	_, err := encodeJSON([]byte(`{bad`))
	if err == nil {
		t.Fatal("expected error")
	}
}
