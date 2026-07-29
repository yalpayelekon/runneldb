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
}
