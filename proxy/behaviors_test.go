package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/limes-cloud/gateway/config"
)

func TestProxyRetriesConfiguredStatus(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "first")
			return
		}
		_, _ = io.WriteString(w, "second")
	}))
	defer upstream.Close()

	pxy := newTestProxy(t)
	route := httpRoute("retry", 100, `req_path_in("/retry")`, targetOf(upstream))
	route.Upstream.Retry = &config.Retry{
		Count:      2,
		Conditions: []config.Condition{{StatusCode: "500"}},
	}
	if err := pxy.Update(&config.Config{Routes: []config.Route{route}}); err != nil {
		t.Fatalf("update routes: %v", err)
	}
	gateway := httptest.NewServer(pxy)
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/retry")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "second" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("upstream calls = %d", calls)
	}
}

func TestProxyReturnsBadGatewayWhenUpstreamUnavailable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	target := targetOf(upstream)
	upstream.Close()

	pxy := newTestProxy(t)
	route := httpRoute("dead", 100, `req_path_in("/dead")`, target)
	if err := pxy.Update(&config.Config{Routes: []config.Route{route}}); err != nil {
		t.Fatalf("update routes: %v", err)
	}
	gateway := httptest.NewServer(pxy)
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/dead")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestProxyResponseFormat(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":500,"message":"bad","reason":"ERR","data":{"x":1}}`)
	}))
	defer upstream.Close()

	pxy := newTestProxy(t)
	route := httpRoute("format", 100, `req_path_in("/format")`, targetOf(upstream))
	route.Response.Format = true
	if err := pxy.Update(&config.Config{Routes: []config.Route{route}}); err != nil {
		t.Fatalf("update routes: %v", err)
	}
	gateway := httptest.NewServer(pxy)
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/format")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response %q: %v", body, err)
	}
	if out["code"].(float64) != 500 || out["reason"] != "ERR" {
		t.Fatalf("formatted response = %#v", out)
	}
}

func TestProxyDebugHandler(t *testing.T) {
	pxy := newTestProxy(t)
	if err := pxy.Update(&config.Config{Routes: []config.Route{
		httpRoute("manager", 100, `req_path_in("/manager")`, "127.0.0.1:7202"),
	}}); err != nil {
		t.Fatalf("update routes: %v", err)
	}
	handler := pxy.DebugHandler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/proxy/router/inspect", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("debug status = %d", rec.Code)
	}
	var inspect []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &inspect); err != nil {
		t.Fatalf("decode inspect: %v", err)
	}
	if len(inspect) == 0 {
		t.Fatal("inspect should not be empty")
	}
}
