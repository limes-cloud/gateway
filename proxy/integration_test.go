package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/limes-cloud/gateway/client"
	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/middleware"
)

func TestProxyMatchesRouteThenAppliesConditionalRewrites(t *testing.T) {
	var gotPath, gotQuery, gotForwardedHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		gotQuery = req.URL.RawQuery
		gotForwardedHost = req.Header.Get("X-Forwarded-Host")
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	pxy, err := New(client.NewFactory(nil), middleware.Create)
	if err != nil {
		t.Fatal(err)
	}
	upstreamTarget := strings.TrimPrefix(upstream.URL, "http://")
	if err := pxy.Update(&config.Config{
		Routes: []config.Route{
			{
				ID:       1,
				Name:     "spider seo",
				Enabled:  true,
				Priority: 1000,
				Match:    config.Match{Expression: `req_header_value_contain("User-Agent", "spider", true)`},
				Rewrites: []config.RewriteRule{
					{
						Name:     "to manager seo",
						Priority: 100,
						Match:    config.Match{Expression: `req_method_in("GET", "HEAD")`, Terminal: true},
						Actions: []config.Action{
							{Type: config.ActionPathSet, Params: map[string]any{"value": "/manager/seo"}},
						},
					},
				},
				Upstream: config.Upstream{Protocol: "HTTP", Backends: []config.Backend{{Target: upstreamTarget}}},
			},
			{
				ID:       2,
				Name:     "legacy redirect",
				Enabled:  true,
				Priority: 200,
				Match:    config.Match{Expression: `req_path_in("/legacy")`},
				Rewrites: []config.RewriteRule{
					{
						Name:  "redirect",
						Match: config.Match{Expression: `req_path_in("/legacy")`, Terminal: true},
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
				},
			},
			{
				ID:       3,
				Name:     "old api",
				Enabled:  true,
				Priority: 100,
				Match:    config.Match{Expression: `req_path_prefix_in("/old-api/", false)`},
				Rewrites: []config.RewriteRule{
					{
						Name:     "trim old api",
						Priority: 100,
						Match:    config.Match{Expression: `req_path_prefix_in("/old-api/", false)`, Terminal: true},
						Actions: []config.Action{
							{Type: config.ActionPathPrefixTrim, Params: map[string]any{"prefix": "/old-api"}},
						},
					},
				},
				Upstream: config.Upstream{Protocol: "HTTP", Backends: []config.Backend{{Target: upstreamTarget}}},
			},
			{
				ID:       4,
				Name:     "static fallback",
				Enabled:  true,
				Priority: -100,
				Match:    config.Match{Expression: `req_host_in("asou.fun")`},
				Upstream: config.Upstream{Protocol: "HTTP", Backends: []config.Backend{{Target: upstreamTarget}}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	gateway := httptest.NewServer(pxy)
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/old-api/users/42?x=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if gotPath != "/users/42" || gotQuery != "x=1" {
		t.Fatalf("upstream got %s?%s", gotPath, gotQuery)
	}

	redirectClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = redirectClient.Get(gateway.URL + "/legacy?x=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "https://new.example.com/legacy?x=1" {
		t.Fatalf("location = %q", got)
	}
	if got := resp.Header.Get("X-Rewrite"); got != "1" {
		t.Fatalf("redirect response header = %q", got)
	}

	req, err := http.NewRequest(http.MethodGet, gateway.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "asou.fun"
	req.Header.Set("User-Agent", "Baiduspider")
	req.Header.Set("X-Forwarded-Host", "asou.fun")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if gotPath != "/manager/seo" {
		t.Fatalf("seo upstream path = %q", gotPath)
	}
	if gotForwardedHost != "asou.fun" {
		t.Fatalf("x-forwarded-host = %q", gotForwardedHost)
	}

	req, err = http.NewRequest(http.MethodGet, gateway.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "asou.fun"
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if gotPath != "/" {
		t.Fatalf("normal user upstream path = %q", gotPath)
	}
}
