package matcher

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExpressionFunctions(t *testing.T) {
	tests := []struct {
		name   string
		expr   string
		setup  func(*http.Request)
		result bool
	}{
		{name: "host strips port", expr: `req_host_in("example.com")`, setup: func(req *http.Request) { req.Host = "example.com:8080" }, result: true},
		{name: "host suffix", expr: `req_host_suffix_in("example.com")`, setup: func(req *http.Request) { req.Host = "api.example.com" }, result: true},
		{name: "host suffix exact", expr: `req_host_suffix_in("example.com")`, setup: func(req *http.Request) { req.Host = "example.com" }, result: true},
		{name: "host suffix false", expr: `req_host_suffix_in("example.com")`, setup: func(req *http.Request) { req.Host = "badexample.com" }, result: false},
		{name: "host port", expr: `req_host_port_in("8080")`, setup: func(req *http.Request) { req.Host = "example.com:8080" }, result: true},
		{name: "method", expr: `req_method_in("get")`, setup: func(req *http.Request) { req.Method = http.MethodGet }, result: true},
		{name: "path suffix", expr: `req_path_suffix_in(".json")`, setup: func(req *http.Request) { req.URL.Path = "/data/file.json" }, result: true},
		{name: "path contain ignore case", expr: `req_path_contain("/API/", true)`, setup: func(req *http.Request) { req.URL.Path = "/v1/api/users" }, result: true},
		{name: "header key", expr: `req_header_key_in("x-trace-id")`, setup: func(req *http.Request) { req.Header.Set("X-Trace-Id", "1") }, result: true},
		{name: "header value exact", expr: `req_header_value_in("X-Env", "prod", "staging")`, setup: func(req *http.Request) { req.Header.Set("X-Env", "staging") }, result: true},
		{name: "header value prefix ignore case", expr: `req_header_value_prefix_in("Accept", "text/html", true)`, setup: func(req *http.Request) { req.Header.Set("Accept", "TEXT/HTML,application/json") }, result: true},
		{name: "header value suffix", expr: `req_header_value_suffix_in("X-File", ".json")`, setup: func(req *http.Request) { req.Header.Set("X-File", "data.json") }, result: true},
		{name: "ua contains any", expr: `req_ua_contains_any("baiduspider", "googlebot")`, setup: func(req *http.Request) { req.Header.Set("User-Agent", "Mozilla Baiduspider/2.0") }, result: true},
		{name: "query exists", expr: `req_query_exist("from")`, setup: func(req *http.Request) { req.URL.RawQuery = "from=seo" }, result: true},
		{name: "query key", expr: `req_query_key_in("from", "to")`, setup: func(req *http.Request) { req.URL.RawQuery = "to=a" }, result: true},
		{name: "query key prefix", expr: `req_query_key_prefix_in("utm_")`, setup: func(req *http.Request) { req.URL.RawQuery = "utm_source=seo" }, result: true},
		{name: "query value", expr: `req_query_value_in("from", "seo")`, setup: func(req *http.Request) { req.URL.RawQuery = "from=seo" }, result: true},
		{name: "query value contain", expr: `req_query_value_contain("q", "hello")`, setup: func(req *http.Request) { req.URL.RawQuery = "q=say-hello-world" }, result: true},
		{name: "query value prefix ignore case", expr: `req_query_value_prefix_in("q", "hello", true)`, setup: func(req *http.Request) { req.URL.RawQuery = "q=HELLO-world" }, result: true},
		{name: "query value suffix", expr: `req_query_value_suffix_in("file", ".json")`, setup: func(req *http.Request) { req.URL.RawQuery = "file=data.json" }, result: true},
		{name: "cookie key", expr: `req_cookie_key_in("session")`, setup: func(req *http.Request) { req.AddCookie(&http.Cookie{Name: "session", Value: "x"}) }, result: true},
		{name: "cookie value", expr: `req_cookie_value_in("session", "abc")`, setup: func(req *http.Request) { req.AddCookie(&http.Cookie{Name: "session", Value: "abc"}) }, result: true},
		{
			name: "client ip from forwarded for",
			expr: `req_client_ip_in("203.0.113.8")`,
			setup: func(req *http.Request) {
				req.Header.Set("X-Forwarded-For", "unknown, 203.0.113.8, 10.0.0.1")
				req.RemoteAddr = "127.0.0.1:1234"
			},
			result: true,
		},
		{
			name: "client ip from real ip",
			expr: `req_client_ip_range("203.0.113.0/24")`,
			setup: func(req *http.Request) {
				req.Header.Set("X-Real-IP", "203.0.113.9")
				req.RemoteAddr = "127.0.0.1:1234"
			},
			result: true,
		},
		{
			name: "client ip fallback remote addr",
			expr: `req_client_ip_in("127.0.0.1")`,
			setup: func(req *http.Request) {
				req.RemoteAddr = "127.0.0.1:1234"
			},
			result: true,
		},
		{
			name: "client ip forwarded miss does not use proxy ip",
			expr: `req_client_ip_in("127.0.0.1")`,
			setup: func(req *http.Request) {
				req.Header.Set("X-Forwarded-For", "203.0.113.8")
				req.RemoteAddr = "127.0.0.1:1234"
			},
			result: false,
		},
		{
			name: "secure forwarded proto",
			expr: `req_proto_secure()`,
			setup: func(req *http.Request) {
				req.Header.Set("X-Forwarded-Proto", "HTTPS")
			},
			result: true,
		},
		{
			name: "secure tls",
			expr: `req_proto_secure()`,
			setup: func(req *http.Request) {
				req.TLS = &tls.ConnectionState{}
			},
			result: true,
		},
		{name: "secure false", expr: `req_proto_secure()`, result: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Compile(tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
			if tt.setup != nil {
				tt.setup(req)
			}
			if got := expr.Eval(req); got != tt.result {
				t.Fatalf("Eval() = %v, want %v", got, tt.result)
			}
		})
	}
}

func TestCompileAndEvalEdgeCases(t *testing.T) {
	var nilExpr *Expression
	if nilExpr.Source() != "" {
		t.Fatal("nil source should be empty")
	}
	if nilExpr.Eval(httptest.NewRequest(http.MethodGet, "http://example.com/", nil)) {
		t.Fatal("nil expression should not match")
	}
	expr, err := Compile(`req_path_in("/")`)
	if err != nil {
		t.Fatal(err)
	}
	if expr.Source() != `req_path_in("/")` {
		t.Fatalf("source = %q", expr.Source())
	}
	if expr.Eval(nil) {
		t.Fatal("nil request should not match")
	}

	invalid := []string{
		"", strings.Repeat("a", maxExpressionLength+1), `req_path_in("/a") &&`,
		`req_path_in("/a") & req_method_in("GET")`, `req_path_in("/a") | req_method_in("GET")`,
		`req_path_in("/a") @`, `req_path_in("/a") req_method_in("GET")`, `req_path_in("/a\q")`,
		`req_path_in("/a`, `req_query_exist()`, `req_proto_secure("x")`, `req_client_ip_in("bad-ip")`,
		`req_header_value_in("X-Env")`, `req_header_value_contain("X-Env")`, `req_query_prefix_in("x")`,
		`(req_path_in("/a")`, `req_path_in("/a"))`, `req_path_in("/a", "/b") &&`, `!`,
	}
	for _, source := range invalid {
		if _, err := Compile(source); err == nil {
			t.Fatalf("Compile(%q) returned nil error", source)
		}
	}
}

func TestSplitHostPortAndParsePort(t *testing.T) {
	tests := []struct {
		input    string
		wantHost string
		wantPort string
	}{
		{input: "example.com:8080", wantHost: "example.com", wantPort: "8080"},
		{input: "[::1]:8080", wantHost: "::1", wantPort: "8080"},
		{input: "[::1]", wantHost: "::1"},
		{input: "example.com", wantHost: "example.com"},
	}
	for _, tt := range tests {
		host, port := splitHostPort(tt.input)
		if host != tt.wantHost || port != tt.wantPort {
			t.Fatalf("splitHostPort(%q) = %q, %q", tt.input, host, port)
		}
	}
	if got, ok := ParsePort("8080"); !ok || got != 8080 {
		t.Fatalf("ParsePort valid = %d, %v", got, ok)
	}
	for _, raw := range []string{"", "-1", "70000", "bad"} {
		if _, ok := ParsePort(raw); ok {
			t.Fatalf("ParsePort(%q) should fail", raw)
		}
	}
}
