package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/limes-cloud/gateway/config"
)

func TestValidateRoutesRejectsDuplicates(t *testing.T) {
	route := config.Route{
		Name:  "manager",
		Match: config.Match{Expression: `req_path_prefix_in("/manager/", false)`},
		Upstream: config.Upstream{
			Protocol: "HTTP",
			Backends: []config.Backend{{Target: "127.0.0.1:7202"}},
		},
	}
	if err := ValidateRoutes([]config.Route{route, route}); err == nil {
		t.Fatal("expected duplicate route to be rejected")
	}
}

func validTestRoute() config.Route {
	return config.Route{
		Name:     "manager",
		Enabled:  true,
		Priority: 10,
		Match:    config.Match{Expression: `req_path_prefix_in("/manager/", false)`},
		Upstream: config.Upstream{
			Protocol: "HTTP",
			Backends: []config.Backend{{Target: "127.0.0.1:7202"}},
		},
	}
}

func TestValidateRouteBranches(t *testing.T) {
	redirectRule := config.RewriteRule{
		Name:    "redirect",
		Match:   config.Match{Expression: `req_path_in("/legacy")`, Terminal: true},
		Actions: []config.Action{{Type: config.ActionRedirect, Params: map[string]any{"statusCode": 302, "location": "/new"}}},
	}
	for _, tc := range []struct {
		name   string
		mutate func(*config.Route)
		ok     bool
	}{
		{"valid", func(*config.Route) {}, true},
		{"missing name", func(r *config.Route) { r.Name = "  " }, false},
		{"missing expression", func(r *config.Route) { r.Match.Expression = "" }, false},
		{"invalid expression", func(r *config.Route) { r.Match.Expression = `req_path_in("/a"` }, false},
		{"unknown protocol", func(r *config.Route) { r.Upstream.Protocol = "TCP" }, false},
		{"empty backend", func(r *config.Route) { r.Upstream.Backends[0].Target = " " }, false},
		{"negative timeout", func(r *config.Route) { r.Upstream.TimeoutMs = -1 }, false},
		{"negative retry count", func(r *config.Route) {
			r.Upstream.Retry = &config.Retry{Count: -1}
		}, false},
		{"negative retry timeout", func(r *config.Route) {
			r.Upstream.Retry = &config.Retry{Count: 1, TimeoutMs: -1}
		}, false},
		{"no upstream no redirect", func(r *config.Route) { r.Upstream.Backends = nil }, false},
		{"no upstream with redirect", func(r *config.Route) {
			r.Upstream.Backends = nil
			r.Rewrites = []config.RewriteRule{redirectRule}
		}, true},
		{"bad response header name", func(r *config.Route) {
			r.Response.Headers.Set = map[string]string{"Bad:Name": "x"}
		}, false},
		{"bad response header value", func(r *config.Route) {
			r.Response.Headers.Set = map[string]string{"X-Test": "a\r\nb"}
		}, false},
		{"bad response header remove", func(r *config.Route) {
			r.Response.Headers.Remove = []string{"Bad:Name"}
		}, false},
		{"empty middleware name", func(r *config.Route) {
			r.Middlewares = []config.Middleware{{Name: ""}}
		}, false},
		{"unknown middleware", func(r *config.Route) {
			r.Middlewares = []config.Middleware{{Name: "nope"}}
		}, false},
		{"known middleware", func(r *config.Route) {
			r.Middlewares = []config.Middleware{{Name: "cors"}}
		}, true},
		{"invalid rewrite", func(r *config.Route) {
			r.Rewrites = []config.RewriteRule{{
				Name:    "bad",
				Match:   config.Match{Expression: `req_path_in("/")`},
				Actions: []config.Action{{Type: "NOPE"}},
			}}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route := validTestRoute()
			tc.mutate(&route)
			err := ValidateRoute(&route)
			if tc.ok && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected invalid")
			}
		})
	}
}

func TestValidateRoutesRejectsBadRouteIndex(t *testing.T) {
	first := validTestRoute()
	second := validTestRoute()
	second.Name = "bad"
	second.Upstream.Backends = nil
	err := ValidateRoutes([]config.Route{first, second})
	if err == nil {
		t.Fatal("expected invalid route to be rejected")
	}
}

func TestRouteDTODefaultsAndRoundTrip(t *testing.T) {
	dto := routeDTO{Name: "x"}
	route := dto.toRoute()
	if !route.Enabled {
		t.Fatal("enabled should default to true")
	}

	route = &config.Route{
		Name:     "manager",
		Enabled:  false,
		Priority: 10,
		Match:    config.Match{Expression: `req_host_in("asou.fun")`},
		Rewrites: []config.RewriteRule{{
			Name:     "spider seo",
			Priority: 100,
			Match:    config.Match{Expression: `req_header_value_contain("User-Agent", "spider", true)`, Terminal: true},
			Actions: []config.Action{
				{Type: config.ActionPathSet, Params: map[string]any{"value": "/manager/seo"}},
				{Type: config.ActionRedirect, Params: map[string]any{"statusCode": 302, "location": "/login"}},
			},
		}},
		Upstream: config.Upstream{Protocol: "HTTP", Backends: []config.Backend{{Target: "127.0.0.1:7202"}}},
		Response: config.Response{Headers: config.HeaderPolicy{Set: map[string]string{"X-Test": "1"}}},
		Metadata: map[string]string{"owner": "manager"},
	}
	encoded, err := json.Marshal(toRouteDTO(route))
	if err != nil {
		t.Fatalf("marshal dto: %v", err)
	}
	var decoded routeDTO
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal dto: %v", err)
	}
	got := decoded.toRoute()
	if got.Enabled || len(got.Rewrites) != 1 || len(got.Rewrites[0].Actions) != 2 {
		t.Fatalf("dto round-trip route = %#v", got)
	}
	if got.Rewrites[0].Actions[0].Type != config.ActionPathSet || got.Metadata["owner"] != "manager" {
		t.Fatalf("dto round-trip details = %#v", got)
	}
}

func TestAdminHelpers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?page=0&size=999&enabled=1", nil)
	page, size := parsePaging(req)
	if page != 1 || size != 20 {
		t.Fatalf("paging = %d/%d", page, size)
	}
	status := parseStatus(req)
	if status == nil || !*status {
		t.Fatalf("status = %#v", status)
	}
	req = httptest.NewRequest(http.MethodGet, "/?enabled=false", nil)
	status = parseStatus(req)
	if status == nil || *status {
		t.Fatalf("status false = %#v", status)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	if parseStatus(req) != nil {
		t.Fatal("empty enabled should mean all")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-md-global-info", `{"username":"alice"}`)
	if got := (&Handler{}).operator(req); got != "alice" {
		t.Fatalf("operator = %q", got)
	}
	req.Header.Set("x-md-global-info", `{"nickname":"bob"}`)
	if got := (&Handler{}).operator(req); got != "bob" {
		t.Fatalf("operator = %q", got)
	}
	req.Header.Set("x-md-global-info", `{`)
	if got := (&Handler{}).operator(req); got != "console" {
		t.Fatalf("operator fallback = %q", got)
	}
	if reasonOf(http.StatusUnauthorized) != "UNAUTHORIZED" {
		t.Fatal("reasonOf unauthorized mismatch")
	}
}

func TestValidateRouteRejectsInvalidExpression(t *testing.T) {
	route := config.Route{
		Name:  "bad",
		Match: config.Match{Expression: `req_path_in("/a"`},
		Upstream: config.Upstream{
			Protocol: "HTTP",
			Backends: []config.Backend{{Target: "127.0.0.1:7202"}},
		},
	}
	if err := ValidateRoute(&route); err == nil {
		t.Fatal("expected invalid expression to be rejected")
	}
}

func TestValidateRewriteRuleRejectsInvalidAction(t *testing.T) {
	rule := config.RewriteRule{
		Name:  "bad action",
		Match: config.Match{Expression: `req_path_in("/")`, Terminal: true},
		Actions: []config.Action{
			{Type: config.ActionPathSet, Params: map[string]any{"value": "no-leading-slash"}},
		},
	}
	if err := ValidateRewriteRule(&rule); err == nil {
		t.Fatal("expected invalid path action to be rejected")
	}
}
