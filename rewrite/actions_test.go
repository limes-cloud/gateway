package rewrite

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/limes-cloud/gateway/config"
)

func applyActions(t *testing.T, req *http.Request, actions ...config.Action) (*Result, error) {
	t.Helper()
	rules, err := CompileRules([]config.RewriteRule{
		{
			Name:    "test",
			Match:   config.Match{Expression: `req_method_in("GET")`},
			Actions: actions,
		},
	})
	if err != nil {
		return nil, err
	}
	return Apply(req, rules)
}

func TestApplyActions(t *testing.T) {
	tests := []struct {
		name   string
		target string
		action config.Action
		check  func(t *testing.T, req *http.Request, result *Result)
	}{
		{
			name:   "host set",
			target: "http://origin.example.com/users",
			action: config.Action{Type: config.ActionHostSet, Params: map[string]any{"value": "internal.example.com:8080"}},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				if req.Host != "internal.example.com:8080" {
					t.Fatalf("host = %q", req.Host)
				}
			},
		},
		{
			name:   "path set",
			target: "http://example.com/users",
			action: config.Action{Type: config.ActionPathSet, Params: map[string]any{"value": "/manager/seo"}},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				if req.URL.Path != "/manager/seo" {
					t.Fatalf("path = %q", req.URL.Path)
				}
			},
		},
		{
			name:   "path prefix add",
			target: "http://example.com/users/42",
			action: config.Action{Type: config.ActionPathPrefixAdd, Params: map[string]any{"prefix": "/sale"}},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				if req.URL.Path != "/sale/users/42" {
					t.Fatalf("path = %q", req.URL.Path)
				}
			},
		},
		{
			name:   "path prefix trim",
			target: "http://example.com/old-api/users",
			action: config.Action{Type: config.ActionPathPrefixTrim, Params: map[string]any{"prefix": "/old-api"}},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				if req.URL.Path != "/users" {
					t.Fatalf("path = %q", req.URL.Path)
				}
			},
		},
		{
			name:   "path regex replace",
			target: "http://example.com/v1/users/42",
			action: config.Action{Type: config.ActionPathRegexReplace, Params: map[string]any{"pattern": `^/v1/(.*)$`, "replacement": "/v2/$1"}},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				if req.URL.Path != "/v2/users/42" {
					t.Fatalf("path = %q", req.URL.Path)
				}
			},
		},
		{
			name:   "raw query add",
			target: "http://example.com/search?x=1",
			action: config.Action{Type: config.ActionRawQueryAdd, Params: map[string]any{"key": "from", "value": "gateway"}},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				if got := req.URL.Query().Get("from"); got != "gateway" {
					t.Fatalf("from = %q", got)
				}
			},
		},
		{
			name:   "raw query rename",
			target: "http://example.com/search?old=1&keep=2",
			action: config.Action{Type: config.ActionRawQueryRename, Params: map[string]any{"key": "old", "newKey": "new"}},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				query := req.URL.Query()
				if query.Has("old") || query.Get("new") != "1" || query.Get("keep") != "2" {
					t.Fatalf("query = %v", query)
				}
			},
		},
		{
			name:   "raw query delete",
			target: "http://example.com/search?drop=1&keep=2",
			action: config.Action{Type: config.ActionRawQueryDel, Params: map[string]any{"key": "drop"}},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				query := req.URL.Query()
				if query.Has("drop") || query.Get("keep") != "2" {
					t.Fatalf("query = %v", query)
				}
			},
		},
		{
			name:   "raw query keep only",
			target: "http://example.com/search?keep=1&drop=2&other=3",
			action: config.Action{Type: config.ActionRawQueryDelAllEx, Params: map[string]any{"keys": []any{"keep"}}},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				query := req.URL.Query()
				if len(query) != 1 || query.Get("keep") != "1" {
					t.Fatalf("query = %v", query)
				}
			},
		},
		{
			name:   "raw query clear",
			target: "http://example.com/search?x=1",
			action: config.Action{Type: config.ActionRawQueryClear},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				if req.URL.RawQuery != "" {
					t.Fatalf("raw query = %q", req.URL.RawQuery)
				}
			},
		},
		{
			name:   "request header actions",
			target: "http://example.com/",
			action: config.Action{Type: config.ActionRequestHeaderSet, Params: map[string]any{"key": "X-Env", "value": "prod"}},
			check: func(t *testing.T, req *http.Request, _ *Result) {
				if req.Header.Get("X-Env") != "prod" {
					t.Fatalf("x-env = %q", req.Header.Get("X-Env"))
				}
			},
		},
		{
			name:   "response header set",
			target: "http://example.com/",
			action: config.Action{Type: config.ActionResponseHeaderSet, Params: map[string]any{"key": "Cache-Control", "value": "no-store"}},
			check: func(t *testing.T, _ *http.Request, result *Result) {
				if result.ResponseHeaders == nil || result.ResponseHeaders.Set["Cache-Control"] != "no-store" {
					t.Fatalf("response headers = %#v", result.ResponseHeaders)
				}
			},
		},
		{
			name:   "redirect resolves placeholders",
			target: "http://example.com:8080/legacy?x=1",
			action: config.Action{
				Type: config.ActionRedirect,
				Params: map[string]any{
					"statusCode":    308,
					"location":      "https://${request.hostWithPort}${request.path}?from=rewrite#frag",
					"preserveQuery": false,
				},
			},
			check: func(t *testing.T, _ *http.Request, result *Result) {
				if result.Redirect == nil {
					t.Fatal("missing redirect")
				}
				if result.Redirect.StatusCode != 308 || result.Redirect.Location != "https://example.com:8080/legacy?from=rewrite#frag" {
					t.Fatalf("redirect = %#v", result.Redirect)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			result, err := applyActions(t, req, tt.action)
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, req, result)
		})
	}
}

func TestApplyRequestHeaderAddAndDelete(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Header.Set("X-Drop", "1")
	_, err := applyActions(t, req,
		config.Action{Type: config.ActionRequestHeaderAdd, Params: map[string]any{"key": "X-Multi", "value": "a"}},
		config.Action{Type: config.ActionRequestHeaderAdd, Params: map[string]any{"key": "X-Multi", "value": "b"}},
		config.Action{Type: config.ActionRequestHeaderDel, Params: map[string]any{"key": "X-Drop"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Values("X-Multi"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("x-multi = %v", got)
	}
	if req.Header.Get("X-Drop") != "" {
		t.Fatal("x-drop was not deleted")
	}
}

func TestApplyResponseHeaderAddAndDelete(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	result, err := applyActions(t, req,
		config.Action{Type: config.ActionResponseHeaderAdd, Params: map[string]any{"key": "X-Multi", "value": "a"}},
		config.Action{Type: config.ActionResponseHeaderAdd, Params: map[string]any{"key": "X-Multi", "value": "b"}},
		config.Action{Type: config.ActionResponseHeaderDel, Params: map[string]any{"key": "Server"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ResponseHeaders.Add["X-Multi"] != "b" {
		t.Fatalf("response add = %#v", result.ResponseHeaders.Add)
	}
	if len(result.ResponseHeaders.Remove) != 1 || result.ResponseHeaders.Remove[0] != "Server" {
		t.Fatalf("response remove = %#v", result.ResponseHeaders.Remove)
	}
}

func TestApplyRedirectPreservesQueryBeforeFragment(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/legacy?x=1", nil)
	result, err := applyActions(t, req, config.Action{
		Type:   config.ActionRedirect,
		Params: map[string]any{"statusCode": "302", "location": "/new#frag"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Redirect == nil || result.Redirect.Location != "/new?x=1#frag" {
		t.Fatalf("redirect = %#v", result.Redirect)
	}
}

func TestApplyNonTerminalRuleAffectsNextRule(t *testing.T) {
	rules, err := CompileRules([]config.RewriteRule{
		{
			Name:     "trim",
			Priority: 100,
			Match:    config.Match{Expression: `req_path_prefix_in("/old/", false)`},
			Actions:  []config.Action{{Type: config.ActionPathPrefixTrim, Params: map[string]any{"prefix": "/old"}}},
		},
		{
			Name:     "after trim",
			Priority: 90,
			Match:    config.Match{Expression: `req_path_prefix_in("/users/", false)`},
			Actions:  []config.Action{{Type: config.ActionRequestHeaderSet, Params: map[string]any{"key": "X-Chained", "value": "1"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/old/users/42", nil)
	if _, err := Apply(req, rules); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("X-Chained") != "1" {
		t.Fatal("later rule did not observe previous mutation")
	}
}

func TestCompileRulesStablePriorityOrder(t *testing.T) {
	rules, err := CompileRules([]config.RewriteRule{
		{Name: "low", Priority: 1, Match: config.Match{Expression: `req_path_in("/")`}, Actions: []config.Action{{Type: config.ActionRawQueryClear}}},
		{Name: "high-1", Priority: 3, Match: config.Match{Expression: `req_path_in("/")`}, Actions: []config.Action{{Type: config.ActionRawQueryClear}}},
		{Name: "high-2", Priority: 3, Match: config.Match{Expression: `req_path_in("/")`}, Actions: []config.Action{{Type: config.ActionRawQueryClear}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rules[0].Rule.Name != "high-1" || rules[1].Rule.Name != "high-2" || rules[2].Rule.Name != "low" {
		t.Fatalf("order = %s, %s, %s", rules[0].Rule.Name, rules[1].Rule.Name, rules[2].Rule.Name)
	}
}

func TestValidateActions(t *testing.T) {
	tests := []struct {
		name    string
		actions []config.Action
		wantErr string
	}{
		{name: "unknown", actions: []config.Action{{Type: "NOPE"}}, wantErr: "unknown action type"},
		{name: "host missing", actions: []config.Action{{Type: config.ActionHostSet, Params: map[string]any{}}}, wantErr: "params.value"},
		{name: "host newline", actions: []config.Action{{Type: config.ActionHostSet, Params: map[string]any{"value": "a\nb"}}}, wantErr: "line breaks"},
		{name: "path missing", actions: []config.Action{{Type: config.ActionPathSet, Params: map[string]any{}}}, wantErr: "required"},
		{name: "prefix add invalid", actions: []config.Action{{Type: config.ActionPathPrefixAdd, Params: map[string]any{"prefix": "api"}}}, wantErr: "must start with"},
		{name: "prefix trim empty", actions: []config.Action{{Type: config.ActionPathPrefixTrim, Params: map[string]any{"prefix": ""}}}, wantErr: "required"},
		{name: "regex missing", actions: []config.Action{{Type: config.ActionPathRegexReplace, Params: map[string]any{}}}, wantErr: "required"},
		{name: "regex invalid", actions: []config.Action{{Type: config.ActionPathRegexReplace, Params: map[string]any{"pattern": "(", "replacement": "x"}}}, wantErr: "invalid"},
		{name: "query add key missing", actions: []config.Action{{Type: config.ActionRawQueryAdd, Params: map[string]any{"value": "x"}}}, wantErr: "params.key"},
		{name: "query rename missing", actions: []config.Action{{Type: config.ActionRawQueryRename, Params: map[string]any{"key": "a"}}}, wantErr: "newKey"},
		{name: "query rename same", actions: []config.Action{{Type: config.ActionRawQueryRename, Params: map[string]any{"key": "a", "newKey": "a"}}}, wantErr: "different"},
		{name: "query delete missing", actions: []config.Action{{Type: config.ActionRawQueryDel, Params: map[string]any{}}}, wantErr: "params.key"},
		{name: "query keep empty", actions: []config.Action{{Type: config.ActionRawQueryDelAllEx, Params: map[string]any{"keys": []string{}}}}, wantErr: "must not be empty"},
		{name: "query keep blank item", actions: []config.Action{{Type: config.ActionRawQueryDelAllEx, Params: map[string]any{"keys": []string{" "}}}}, wantErr: "empty value"},
		{name: "request header invalid name", actions: []config.Action{{Type: config.ActionRequestHeaderSet, Params: map[string]any{"key": "Bad:Header", "value": "x"}}}, wantErr: "invalid header name"},
		{name: "request header invalid value", actions: []config.Action{{Type: config.ActionRequestHeaderAdd, Params: map[string]any{"key": "X-Test", "value": "a\rb"}}}, wantErr: "line breaks"},
		{name: "request header delete", actions: []config.Action{{Type: config.ActionRequestHeaderDel, Params: map[string]any{"key": "X-Test"}}}},
		{name: "response header invalid name", actions: []config.Action{{Type: config.ActionResponseHeaderSet, Params: map[string]any{"key": "", "value": "x"}}}, wantErr: "invalid header name"},
		{name: "response header add", actions: []config.Action{{Type: config.ActionResponseHeaderAdd, Params: map[string]any{"key": "X-Test", "value": "x"}}}},
		{name: "response header delete", actions: []config.Action{{Type: config.ActionResponseHeaderDel, Params: map[string]any{"key": "X-Test"}}}},
		{name: "redirect status", actions: []config.Action{{Type: config.ActionRedirect, Params: map[string]any{"statusCode": 200, "location": "/"}}}, wantErr: "unsupported status"},
		{name: "redirect location", actions: []config.Action{{Type: config.ActionRedirect, Params: map[string]any{"statusCode": 302, "location": "javascript:alert(1)"}}}, wantErr: "location"},
		{name: "redirect valid", actions: []config.Action{{Type: config.ActionRedirect, Params: map[string]any{"statusCode": "307", "location": "https://example.com"}}}},
		{name: "clear valid", actions: []config.Action{{Type: config.ActionRawQueryClear}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateActions(tt.actions)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRuleAndRules(t *testing.T) {
	tests := []struct {
		name string
		rule config.RewriteRule
	}{
		{name: "missing name", rule: config.RewriteRule{Match: config.Match{Expression: `req_path_in("/")`}, Actions: []config.Action{{Type: config.ActionRawQueryClear}}}},
		{name: "invalid expression", rule: config.RewriteRule{Name: "x", Match: config.Match{Expression: `req_path_in(`}, Actions: []config.Action{{Type: config.ActionRawQueryClear}}}},
		{name: "no actions", rule: config.RewriteRule{Name: "x", Match: config.Match{Expression: `req_path_in("/")`}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateRule(&tt.rule); err == nil {
				t.Fatal("expected validation error")
			}
			if err := ValidateRules([]config.RewriteRule{tt.rule}); err == nil {
				t.Fatal("expected rules validation error")
			}
		})
	}
}

func TestParamsAnyDecoding(t *testing.T) {
	if got, err := paramsMap(nil); err != nil || len(got) != 0 {
		t.Fatalf("nil params = %#v, err=%v", got, err)
	}
	if got := paramString(map[string]string{"value": "x"}, "value"); got != "x" {
		t.Fatalf("map string value = %q", got)
	}
	if got, err := paramsMap([]byte(`{"value":"x","keys":["a"],"statusCode":"307","flag":true}`)); err != nil || got["value"] != "x" {
		t.Fatalf("bytes params = %#v, err=%v", got, err)
	}
	if _, err := paramsMap(json.RawMessage(`{`)); err == nil {
		t.Fatal("expected invalid raw message error")
	}
	if _, err := paramsMap([]byte(`{`)); err == nil {
		t.Fatal("expected invalid bytes error")
	}
	if _, err := paramsMap(struct{ Value chan int }{Value: make(chan int)}); err == nil {
		t.Fatal("expected marshal error")
	}
	if got := paramStrings([]byte(`{"keys":["a",2,"b"]}`), "keys"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("strings = %v", got)
	}
	if got := paramStrings(map[string]any{"keys": "a"}, "keys"); got != nil {
		t.Fatalf("non-array strings = %v", got)
	}
	if got := paramInt(map[string]any{"n": int64(7)}, "n"); got != 7 {
		t.Fatalf("int64 = %d", got)
	}
	if got := paramInt(map[string]any{"n": json.Number("8")}, "n"); got != 8 {
		t.Fatalf("json number = %d", got)
	}
	if got := paramInt(map[string]any{"n": "bad"}, "n"); got != 0 {
		t.Fatalf("bad int = %d", got)
	}
	if got := paramBoolDefault(map[string]any{"b": false}, "b", true); got {
		t.Fatal("bool default should preserve explicit false")
	}
	if got := paramBoolDefault(map[string]any{"b": "false"}, "b", true); !got {
		t.Fatal("wrong bool type should use default")
	}
}

func TestApplyQueryParseError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.URL.RawQuery = "%zz"
	_, err := applyActions(t, req, config.Action{Type: config.ActionRawQueryAdd, Params: map[string]any{"key": "x", "value": "1"}})
	if err == nil || !strings.Contains(err.Error(), "invalid raw query") {
		t.Fatalf("error = %v", err)
	}
}

func TestPathHelpers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	setPath(req, "")
	if req.URL.Path != "/" {
		t.Fatalf("empty path = %q", req.URL.Path)
	}
	setPath(req, "users")
	if req.URL.Path != "/users" {
		t.Fatalf("relative path = %q", req.URL.Path)
	}
	if got := joinPath("/sale/", "/users"); got != "/sale/users" {
		t.Fatalf("join = %q", got)
	}
	if got := joinPath("/sale", "/"); got != "/sale" {
		t.Fatalf("join root = %q", got)
	}
	if got := joinPath("", "/users"); got != "/users" {
		t.Fatalf("join empty prefix = %q", got)
	}
	if got := joinPath("/sale", "users"); got != "/sale/users" {
		t.Fatalf("join relative = %q", got)
	}
}

func TestRequestScheme(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	if got := requestScheme(req); got != "http" {
		t.Fatalf("default scheme = %q", got)
	}
	req.Header.Set("X-Forwarded-Proto", "HTTPS")
	if got := requestScheme(req); got != "https" {
		t.Fatalf("forwarded scheme = %q", got)
	}
	req.Header.Del("X-Forwarded-Proto")
	req.TLS = &tls.ConnectionState{}
	if got := requestScheme(req); got != "https" {
		t.Fatalf("tls scheme = %q", got)
	}
}

func TestAppendQuery(t *testing.T) {
	tests := []struct {
		location string
		query    string
		want     string
	}{
		{location: "/next", query: "x=1", want: "/next?x=1"},
		{location: "/next?a=1", query: "x=1", want: "/next?a=1&x=1"},
		{location: "/next#frag", query: "x=1", want: "/next?x=1#frag"},
	}
	for _, tt := range tests {
		if got := appendQuery(tt.location, tt.query); got != tt.want {
			t.Fatalf("appendQuery(%q) = %q", tt.location, got)
		}
	}
}

func TestParseQueryNilURL(t *testing.T) {
	if _, err := parseQuery(&http.Request{}); err == nil {
		t.Fatal("expected nil URL error")
	}
}

func TestRawQueryMultipleValues(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	_, err := applyActions(t, req, config.Action{Type: config.ActionRawQueryAdd, Params: map[string]any{"key": "tag", "value": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.URL.Query(); got.Get("tag") != "a" {
		t.Fatalf("query = %v", got)
	}
	if _, err := url.ParseQuery(req.URL.RawQuery); err != nil {
		t.Fatalf("invalid raw query after rewrite: %v", err)
	}
}
