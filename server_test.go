package runneldb

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPAPI(t *testing.T) {
	db := openTestDB(t)
	server := httptest.NewServer(Handler(db))
	defer server.Close()

	req, _ := http.NewRequest(http.MethodPut, server.URL+"/v1/kv/hello", strings.NewReader("world"))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status: %d", res.StatusCode)
	}

	res, err = http.Get(server.URL + "/v1/kv/hello")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != "world" {
		t.Fatalf("GET: status %d, body %q", res.StatusCode, body)
	}

	req, _ = http.NewRequest(http.MethodDelete, server.URL+"/v1/kv/hello", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status: %d", res.StatusCode)
	}

	res, err = http.Get(server.URL + "/v1/kv/hello")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("GET after delete: %d", res.StatusCode)
	}

	res, err = http.Get(server.URL + "/v1/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"commits"`) {
		t.Fatalf("metrics: %d %s", res.StatusCode, body)
	}
}

func TestCompactHTTP(t *testing.T) {
	db := openTestDB(t)
	server := httptest.NewServer(Handler(db))
	defer server.Close()

	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest(http.MethodPut, server.URL+"/v1/kv/k", strings.NewReader(strings.Repeat("x", i+1)))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
	}
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/compact", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("compact status: %d", res.StatusCode)
	}
	if db.Metrics().Compactions != 1 {
		t.Fatalf("compactions=%d", db.Metrics().Compactions)
	}
}

func TestCheckpointHTTP(t *testing.T) {
	db := openTestDB(t)
	server := httptest.NewServer(Handler(db))
	defer server.Close()

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/checkpoint", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("checkpoint status: %d", res.StatusCode)
	}
}

func TestSQLHTTP(t *testing.T) {
	db := openTestDB(t)
	server := httptest.NewServer(Handler(db))
	defer server.Close()

	post := func(body string) (int, string) {
		t.Helper()
		res, err := http.Post(server.URL+"/v1/sql", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return res.StatusCode, string(b)
	}
	code, _ := post(`{"sql":"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)"}`)
	if code != http.StatusOK {
		t.Fatalf("create %d", code)
	}
	code, _ = post(`{"sql":"INSERT INTO users VALUES (?, ?)","args":[1,"ada"]}`)
	if code != http.StatusOK {
		t.Fatalf("insert %d", code)
	}
	code, body := post(`{"sql":"SELECT name FROM users WHERE id = 1"}`)
	if code != http.StatusOK || !strings.Contains(body, "ada") {
		t.Fatalf("select %d %s", code, body)
	}

	code, _ = post(`{"sql":"CREATE TABLE docs (id INTEGER PRIMARY KEY, doc JSON)"}`)
	if code != http.StatusOK {
		t.Fatalf("create json %d", code)
	}
	code, _ = post(`{"sql":"INSERT INTO docs VALUES (1, ?)","args":["{\"name\":\"ada\"}"]}`)
	if code != http.StatusOK {
		t.Fatalf("insert json %d", code)
	}
	code, body = post(`{"sql":"SELECT json_extract(doc, '$.name') FROM docs WHERE json_extract(doc, '$.name') = 'ada'"}`)
	if code != http.StatusOK || !strings.Contains(body, "ada") {
		t.Fatalf("json select %d %s", code, body)
	}
}
