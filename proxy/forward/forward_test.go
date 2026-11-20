package forward

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/middleware"
)

func TestNewPreservesClientForwardedHeadersAndAppliesResponsePolicies(t *testing.T) {
	var captured *http.Request
	next := middleware.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		captured = req
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Cache-Control": []string{"public"}},
			Body:       http.NoBody,
		}, nil
	})
	handler, err := New(config.Response{Headers: config.HeaderPolicy{
		Set: map[string]string{"Cache-Control": "no-store"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://origin.example.com/users/42", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.8")
	req.Header.Set("X-Forwarded-Host", "asou.fun")
	req.Header.Set("X-Forwarded-Proto", "https")
	req = config.WithResponseHeaders(req, &config.ResponseHeaderPolicy{Set: map[string]string{"X-Rewrite": "1"}})

	resp, err := handler(next).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Header.Get("X-Forwarded-For") != "203.0.113.8" {
		t.Fatalf("x-forwarded-for = %q", captured.Header.Get("X-Forwarded-For"))
	}
	if captured.Header.Get("X-Forwarded-Host") != "asou.fun" {
		t.Fatalf("x-forwarded-host = %q", captured.Header.Get("X-Forwarded-Host"))
	}
	if captured.Header.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("x-forwarded-proto = %q", captured.Header.Get("X-Forwarded-Proto"))
	}
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Rewrite") != "1" {
		t.Fatalf("response headers not applied: %#v", resp.Header)
	}
}

func TestNewAppliesAddRemoveAndInitializesNilHeader(t *testing.T) {
	next := middleware.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     nil,
			Body:       http.NoBody,
		}, nil
	})
	handler, err := New(config.Response{Headers: config.HeaderPolicy{
		Set:    map[string]string{"Cache-Control": "no-store"},
		Add:    map[string]string{"X-Route": "one"},
		Remove: []string{"Server"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://origin.example.com/", nil)
	req = config.WithResponseHeaders(req, &config.ResponseHeaderPolicy{
		Add:    map[string]string{"X-Rewrite": "one"},
		Remove: []string{"X-Origin"},
	})
	resp, err := handler(next).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header == nil {
		t.Fatal("response header should be initialized")
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q", got)
	}
	if got := resp.Header.Get("X-Route"); got != "one" {
		t.Fatalf("x-route = %q", got)
	}
	if got := resp.Header.Get("X-Rewrite"); got != "one" {
		t.Fatalf("x-rewrite = %q", got)
	}
	if resp.Header.Get("Server") != "" || resp.Header.Get("X-Origin") != "" {
		t.Fatalf("removed headers still present: %#v", resp.Header)
	}
}

func TestNewReturnsNextError(t *testing.T) {
	want := errors.New("upstream failed")
	next := middleware.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, want
	})
	handler, err := New(config.Response{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://origin.example.com/", nil)
	if _, err := handler(next).RoundTrip(req); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}
