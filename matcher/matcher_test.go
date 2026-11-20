package matcher

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompileAndEval(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		host       string
		path       string
		method     string
		ua         string
		query      string
		headerKey  string
		headerVal  string
		want       bool
	}{
		{
			name:       "host path and ua",
			expression: `req_host_in("asou.fun") && req_path_in("/") && req_ua_contains_any("baiduspider", "googlebot")`,
			host:       "asou.fun", path: "/", ua: "Mozilla Baiduspider/2.0", want: true,
		},
		{
			name:       "or group",
			expression: `(req_path_in("/a") || req_path_in("/b")) && req_method_in("GET")`,
			path:       "/b", method: http.MethodGet, want: true,
		},
		{
			name:       "path prefix case sensitive",
			expression: `req_path_prefix_in("/old/", false)`,
			path:       "/OLD/x", want: false,
		},
		{
			name:       "path prefix ignore case",
			expression: `req_path_prefix_in("/old/", true)`,
			path:       "/OLD/x", want: true,
		},
		{
			name:       "header contains",
			expression: `req_header_value_contain("X-Env", "pro", false)`,
			headerKey:  "X-Env", headerVal: "prod", want: true,
		},
		{
			name:       "query value",
			expression: `req_query_value_in("from", "seo")`,
			query:      "from=seo", want: true,
		},
		{
			name:       "not",
			expression: `!req_path_in("/health")`,
			path:       "/health", want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Compile(tt.expression)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tt.method, "http://"+tt.host+tt.path+"?"+tt.query, nil)
			if req.URL.Host == "" {
				req.Host = tt.host
			}
			if tt.ua != "" {
				req.Header.Set("User-Agent", tt.ua)
			}
			if tt.headerKey != "" {
				req.Header.Set(tt.headerKey, tt.headerVal)
			}
			if got := expr.Eval(req); got != tt.want {
				t.Fatalf("Eval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompileRejectsInvalidExpressions(t *testing.T) {
	for _, source := range []string{
		`req_path_in("/a"`,
		`unknown_func("a")`,
		`req_path_in(true)`,
		`req_path_prefix_in("/a", "x")`,
		`req_path_in("/a") &&`,
	} {
		if _, err := Compile(source); err == nil {
			t.Fatalf("Compile(%q) error = nil", source)
		}
	}
}
