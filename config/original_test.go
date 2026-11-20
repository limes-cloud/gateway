package config

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestRequestHostWithoutPort(t *testing.T) {
	for input, want := range map[string]string{
		"asou.fun:8080": "asou.fun",
		"asou.fun":      "asou.fun",
		"[::1]:8080":    "::1",
		"[::1]":         "::1",
		"   ":           "",
	} {
		if got := RequestHostWithoutPort(input); got != want {
			t.Fatalf("RequestHostWithoutPort(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResponseHeadersContextRoundTrip(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if _, ok := ResponseHeadersFrom(req); ok {
		t.Fatal("policy should not exist before injection")
	}
	policy := &ResponseHeaderPolicy{Set: map[string]string{"X-Test": "1"}}
	req = WithResponseHeaders(req, policy)
	got, ok := ResponseHeadersFrom(req)
	if !ok || got != policy {
		t.Fatalf("policy round-trip = %#v/%v", got, ok)
	}
}

func TestLoadBoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`
debug: true
addr: ":9000"
name: gateway
version: test
managerAddr: "manager:7080"
routes:
  - name: file-route
    enabled: true
    priority: 10
    match:
      expression: 'req_path_in("/")'
    upstream:
      protocol: HTTP
      backends:
        - target: 127.0.0.1:7202
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadBoot(path)
	if err != nil {
		t.Fatalf("load boot: %v", err)
	}
	if !cfg.Debug || cfg.Addr != ":9000" || cfg.Name != "gateway" || cfg.Version != "test" {
		t.Fatalf("boot fields = %#v", cfg)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Name != "file-route" {
		t.Fatalf("boot routes = %#v", cfg.Routes)
	}
	if _, err := LoadBoot(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing config should return error")
	}
}
