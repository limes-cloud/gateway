package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/limes-cloud/gateway/client"
	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/middleware"
)

type capturedRequest struct {
	Host            string
	Path            string
	RawQuery        string
	ClientHeader    string
	ForwardedFor    string
	ForwardedHost   string
	RewrittenHeader string
	ChainedHeader   string
	LateHeader      string
}

func newCapturingUpstream(t *testing.T, captured *capturedRequest, calls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if calls != nil {
			atomic.AddInt32(calls, 1)
		}
		if captured != nil {
			captured.Host = req.Host
			captured.Path = req.URL.Path
			captured.RawQuery = req.URL.RawQuery
			captured.ClientHeader = req.Header.Get("X-Client")
			captured.ForwardedFor = req.Header.Get("X-Forwarded-For")
			captured.ForwardedHost = req.Header.Get("X-Forwarded-Host")
			captured.RewrittenHeader = req.Header.Get("X-Rewritten")
			captured.ChainedHeader = req.Header.Get("X-Chained")
			captured.LateHeader = req.Header.Get("X-Late")
		}
		w.Header().Set("X-Origin", "yes")
		w.Header().Set("X-Remove-Me", "yes")
		_, _ = io.WriteString(w, "upstream")
	}))
}

func targetOf(server *httptest.Server) string {
	return strings.TrimPrefix(server.URL, "http://")
}

func newTestProxy(t *testing.T) *Proxy {
	t.Helper()
	pxy, err := New(client.NewFactory(nil), middleware.Create)
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	return pxy
}

func httpRoute(name string, priority int, expression, upstream string) config.Route {
	return config.Route{
		Name:     name,
		Enabled:  true,
		Priority: priority,
		Match:    config.Match{Expression: expression},
		Upstream: config.Upstream{Protocol: "HTTP", Backends: []config.Backend{{Target: upstream}}},
	}
}

func TestProxyRouteIsolationAndHeaderPassthrough(t *testing.T) {
	var captured capturedRequest
	upstream := newCapturingUpstream(t, &captured, nil)
	defer upstream.Close()
	target := targetOf(upstream)

	pxy := newTestProxy(t)
	routeA := httpRoute("a", 100, `req_path_prefix_in("/a/", false)`, target)
	routeA.Rewrites = []config.RewriteRule{{
		Name:     "rewrite a",
		Priority: 100,
		Match:    config.Match{Expression: `req_path_prefix_in("/a/", false)`, Terminal: true},
		Actions: []config.Action{
			{Type: config.ActionPathPrefixTrim, Params: map[string]any{"prefix": "/a"}},
			{Type: config.ActionRequestHeaderSet, Params: map[string]any{"key": "X-Rewritten", "value": "a"}},
		},
	}}
	routeB := httpRoute("b", 100, `req_path_prefix_in("/b/", false)`, target)
	routeB.Rewrites = []config.RewriteRule{{
		Name:     "rewrite b",
		Priority: 100,
		Match:    config.Match{Expression: `req_path_prefix_in("/b/", false)`, Terminal: true},
		Actions: []config.Action{
			{Type: config.ActionPathSet, Params: map[string]any{"value": "/b-rewritten"}},
			{Type: config.ActionRequestHeaderSet, Params: map[string]any{"key": "X-Rewritten", "value": "b"}},
		},
	}}
	if err := pxy.Update(&config.Config{Routes: []config.Route{routeA, routeB}}); err != nil {
		t.Fatalf("update routes: %v", err)
	}
	gateway := httptest.NewServer(pxy)
	defer gateway.Close()

	req, _ := http.NewRequest(http.MethodGet, gateway.URL+"/b/users/1?token=abc", nil)
	req.Host = "asou.fun"
	req.Header.Set("X-Client", "client-value")
	req.Header.Set("X-Forwarded-For", "203.0.113.8")
	req.Header.Set("X-Forwarded-Host", "asou.fun")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request b: %v", err)
	}
	_ = resp.Body.Close()
	if captured.Path != "/b-rewritten" || captured.RawQuery != "token=abc" {
		t.Fatalf("b upstream got %s?%s", captured.Path, captured.RawQuery)
	}
	if captured.Host != "asou.fun" {
		t.Fatalf("host passthrough = %q", captured.Host)
	}
	if captured.ClientHeader != "client-value" {
		t.Fatalf("client header = %q", captured.ClientHeader)
	}
	if captured.ForwardedFor != "203.0.113.8" || captured.ForwardedHost != "asou.fun" {
		t.Fatalf("forwarded headers changed: for=%q host=%q", captured.ForwardedFor, captured.ForwardedHost)
	}
	if captured.RewrittenHeader != "b" {
		t.Fatalf("route b rewrite header = %q", captured.RewrittenHeader)
	}

	// 只命中路由 A，不能执行路由 B 的 rewrite
	req, _ = http.NewRequest(http.MethodGet, gateway.URL+"/a/users/1", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request a: %v", err)
	}
	_ = resp.Body.Close()
	if captured.Path != "/users/1" || captured.RewrittenHeader != "a" {
		t.Fatalf("route a got path=%q header=%q", captured.Path, captured.RewrittenHeader)
	}

	// 未命中任何路由时直接 404，不能访问上游
	before := captured.Path
	resp, err = http.Get(gateway.URL + "/unmatched")
	if err != nil {
		t.Fatalf("unmatched request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unmatched status = %d", resp.StatusCode)
	}
	if captured.Path != before {
		t.Fatalf("unmatched request reached upstream: %q", captured.Path)
	}
}

func TestProxyChainsNonTerminalRewritesAndStopsAtTerminal(t *testing.T) {
	var captured capturedRequest
	upstream := newCapturingUpstream(t, &captured, nil)
	defer upstream.Close()

	pxy := newTestProxy(t)
	route := httpRoute("chain", 100, `req_path_in("/chain")`, targetOf(upstream))
	route.Rewrites = []config.RewriteRule{
		{
			Name:     "first",
			Priority: 100,
			Match:    config.Match{Expression: `req_path_in("/chain")`},
			Actions: []config.Action{
				{Type: config.ActionPathSet, Params: map[string]any{"value": "/step1"}},
				{Type: config.ActionRequestHeaderSet, Params: map[string]any{"key": "X-Chained", "value": "first"}},
				{Type: config.ActionRawQueryAdd, Params: map[string]any{"key": "from", "value": "chain"}},
			},
		},
		{
			Name:     "second sees first result",
			Priority: 50,
			Match:    config.Match{Expression: `req_path_in("/step1")`, Terminal: true},
			Actions: []config.Action{
				{Type: config.ActionPathSet, Params: map[string]any{"value": "/final"}},
				{Type: config.ActionRequestHeaderSet, Params: map[string]any{"key": "X-Chained", "value": "second"}},
			},
		},
		{
			Name:     "must not run",
			Priority: 10,
			Match:    config.Match{Expression: `req_path_in("/final")`},
			Actions: []config.Action{
				{Type: config.ActionRequestHeaderSet, Params: map[string]any{"key": "X-Late", "value": "yes"}},
			},
		},
	}
	if err := pxy.Update(&config.Config{Routes: []config.Route{route}}); err != nil {
		t.Fatalf("update routes: %v", err)
	}
	gateway := httptest.NewServer(pxy)
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/chain")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if captured.Path != "/final" || captured.RawQuery != "from=chain" {
		t.Fatalf("chained rewrite got %s?%s", captured.Path, captured.RawQuery)
	}
	if captured.ChainedHeader != "second" || captured.LateHeader != "" {
		t.Fatalf("chained headers = %q/%q", captured.ChainedHeader, captured.LateHeader)
	}
}

func TestProxyRewriteResponseHeaders(t *testing.T) {
	upstream := newCapturingUpstream(t, nil, nil)
	defer upstream.Close()

	pxy := newTestProxy(t)
	route := httpRoute("headers", 100, `req_path_in("/headers")`, targetOf(upstream))
	route.Response.Headers = config.HeaderPolicy{Set: map[string]string{"X-Route": "static"}}
	route.Rewrites = []config.RewriteRule{{
		Name:  "response headers",
		Match: config.Match{Expression: `req_path_in("/headers")`, Terminal: true},
		Actions: []config.Action{
			{Type: config.ActionResponseHeaderSet, Params: map[string]any{"key": "X-Rewrite", "value": "dynamic"}},
			{Type: config.ActionResponseHeaderAdd, Params: map[string]any{"key": "X-Multi", "value": "one"}},
			{Type: config.ActionResponseHeaderAdd, Params: map[string]any{"key": "X-Multi", "value": "two"}},
			{Type: config.ActionResponseHeaderDel, Params: map[string]any{"key": "X-Remove-Me"}},
		},
	}}
	if err := pxy.Update(&config.Config{Routes: []config.Route{route}}); err != nil {
		t.Fatalf("update routes: %v", err)
	}
	gateway := httptest.NewServer(pxy)
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/headers")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.Header.Get("X-Route") != "static" || resp.Header.Get("X-Rewrite") != "dynamic" {
		t.Fatalf("response headers = %#v", resp.Header)
	}
	if got := resp.Header.Values("X-Multi"); len(got) != 1 || got[0] != "two" {
		// Add 在响应策略中按 key 存储，重复 key 后者覆盖前者
		t.Fatalf("x-multi = %#v", got)
	}
	if resp.Header.Get("X-Remove-Me") != "" {
		t.Fatal("x-remove-me should be deleted")
	}
}

func TestProxyRedirectWithoutUpstream(t *testing.T) {
	pxy := newTestProxy(t)
	route := config.Route{
		Name:     "redirect only",
		Enabled:  true,
		Priority: 100,
		Match:    config.Match{Expression: `req_path_in("/legacy")`},
		Rewrites: []config.RewriteRule{{
			Name:  "redirect",
			Match: config.Match{Expression: `req_path_in("/legacy")`, Terminal: true},
			Actions: []config.Action{
				{Type: config.ActionResponseHeaderSet, Params: map[string]any{"key": "X-Redirected", "value": "1"}},
				{Type: config.ActionRedirect, Params: map[string]any{
					"statusCode":    http.StatusMovedPermanently,
					"location":      "https://new.example.com${request.path}",
					"preserveQuery": true,
				}},
			},
		}},
	}
	if err := pxy.Update(&config.Config{Routes: []config.Route{route}}); err != nil {
		t.Fatalf("update routes: %v", err)
	}
	gateway := httptest.NewServer(pxy)
	defer gateway.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(gateway.URL + "/legacy?x=1")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("Location") != "https://new.example.com/legacy?x=1" {
		t.Fatalf("location = %q", resp.Header.Get("Location"))
	}
	if resp.Header.Get("X-Redirected") != "1" {
		t.Fatal("redirect response header missing")
	}
}

func TestProxyRejectsInvalidRouteConfigurations(t *testing.T) {
	upstream := newCapturingUpstream(t, nil, nil)
	defer upstream.Close()
	target := targetOf(upstream)

	valid := httpRoute("valid", 100, `req_path_in("/")`, target)
	for _, tc := range []struct {
		name  string
		route config.Route
	}{
		{"empty expression", httpRoute("empty", 100, "", target)},
		{"unknown protocol", config.Route{
			Name:     "protocol",
			Match:    config.Match{Expression: `req_path_in("/")`},
			Upstream: config.Upstream{Protocol: "TCP", Backends: []config.Backend{{Target: target}}},
		}},
		{"no upstream no redirect", config.Route{
			Name:  "no upstream",
			Match: config.Match{Expression: `req_path_in("/")`},
		}},
		{"invalid rewrite action", func() config.Route {
			route := httpRoute("bad rewrite", 100, `req_path_in("/")`, target)
			route.Rewrites = []config.RewriteRule{{
				Name:    "bad",
				Match:   config.Match{Expression: `req_path_in("/")`},
				Actions: []config.Action{{Type: "NOPE"}},
			}}
			return route
		}()},
		{"invalid rewrite expression", func() config.Route {
			route := httpRoute("bad expr", 100, `req_path_in("/")`, target)
			route.Rewrites = []config.RewriteRule{{
				Name:    "bad",
				Match:   config.Match{Expression: `req_path_in("/"`},
				Actions: []config.Action{{Type: config.ActionPathSet, Params: map[string]any{"value": "/x"}}},
			}}
			return route
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pxy := newTestProxy(t)
			if err := pxy.Update(&config.Config{Routes: []config.Route{valid, tc.route}}); err == nil {
				t.Fatal("expected invalid configuration to be rejected")
			}
		})
	}
}

func TestProxyUpdateSwapsRouteTable(t *testing.T) {
	var calls int32
	upstream := newCapturingUpstream(t, nil, &calls)
	defer upstream.Close()

	pxy := newTestProxy(t)
	gateway := httptest.NewServer(pxy)
	defer gateway.Close()

	old := httpRoute("old", 100, `req_path_in("/old")`, targetOf(upstream))
	if err := pxy.Update(&config.Config{Routes: []config.Route{old}}); err != nil {
		t.Fatalf("update old: %v", err)
	}
	resp, err := http.Get(gateway.URL + "/old")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("old route status=%v err=%v", resp, err)
	}
	_ = resp.Body.Close()

	next := httpRoute("new", 100, `req_path_in("/new")`, targetOf(upstream))
	if err := pxy.Update(&config.Config{Routes: []config.Route{next}}); err != nil {
		t.Fatalf("update new: %v", err)
	}
	resp, err = http.Get(gateway.URL + "/old")
	if err != nil {
		t.Fatalf("old after update: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("old route should be gone, status=%d", resp.StatusCode)
	}
	resp, err = http.Get(gateway.URL + "/new")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("new route status=%v err=%v", resp, err)
	}
	_ = resp.Body.Close()
}
