package rewrite

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/limes-cloud/gateway/config"
)

func TestApplyPriorityTerminalAndParams(t *testing.T) {
	rules, err := CompileRules([]config.RewriteRule{
		{
			Name:     "trim",
			Priority: 100,
			Match: config.Match{
				Expression: `req_path_prefix_in("/old/", false)`,
				Terminal:   true,
			},
			Actions: []config.Action{
				{Type: config.ActionPathPrefixTrim, Params: map[string]any{"prefix": "/old"}},
				{Type: config.ActionRawQueryAdd, Params: map[string]any{"key": "rewritten", "value": "1"}},
				{Type: config.ActionRequestHeaderSet, Params: map[string]any{"key": "X-Rewrite", "value": "1"}},
			},
		},
		{
			Name:     "later",
			Priority: 10,
			Match: config.Match{
				Expression: `req_path_prefix_in("/users/", false)`,
				Terminal:   true,
			},
			Actions: []config.Action{
				{Type: config.ActionPathSet, Params: map[string]any{"value": "/should-not-run"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/old/users?x=1", nil)
	result, err := Apply(req, rules)
	if err != nil {
		t.Fatal(err)
	}
	if result.Redirect != nil {
		t.Fatal("unexpected redirect")
	}
	if req.URL.Path != "/users" || req.URL.RawQuery != "rewritten=1&x=1" {
		t.Fatalf("rewritten request = %s?%s", req.URL.Path, req.URL.RawQuery)
	}
	if req.Header.Get("X-Rewrite") != "1" {
		t.Fatal("request header not rewritten")
	}
}

func TestApplyRedirectAndResponseHeaders(t *testing.T) {
	rules, err := CompileRules([]config.RewriteRule{
		{
			Name:     "redirect",
			Priority: 10,
			Match: config.Match{
				Expression: `req_path_in("/legacy")`,
				Terminal:   true,
			},
			Actions: []config.Action{
				{Type: config.ActionResponseHeaderSet, Params: map[string]any{"key": "X-Rewrite", "value": "1"}},
				{
					Type: config.ActionRedirect,
					Params: map[string]any{
						"statusCode": http.StatusFound,
						"location":   "https://new.example.com${request.path}",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/legacy?x=1", nil)
	result, err := Apply(req, rules)
	if err != nil {
		t.Fatal(err)
	}
	if result.Redirect == nil || result.Redirect.Location != "https://new.example.com/legacy?x=1" {
		t.Fatalf("redirect = %#v", result.Redirect)
	}
	if result.ResponseHeaders == nil || result.ResponseHeaders.Set["X-Rewrite"] != "1" {
		t.Fatalf("response headers = %#v", result.ResponseHeaders)
	}
}

func TestValidateRejectsRedirectBeforeLastAction(t *testing.T) {
	rule := config.RewriteRule{
		Name:  "bad redirect",
		Match: config.Match{Expression: `req_path_in("/")`},
		Actions: []config.Action{
			{
				Type: config.ActionRedirect,
				Params: map[string]any{
					"statusCode": http.StatusFound,
					"location":   "/next",
				},
			},
			{Type: config.ActionPathSet, Params: map[string]any{"value": "/unreachable"}},
		},
	}
	if err := ValidateRule(&rule); err == nil {
		t.Fatal("expected redirect not at end to be rejected")
	}
}
