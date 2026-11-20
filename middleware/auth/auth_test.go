package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/middleware"
)

func TestMiddleware(t *testing.T) {
	type item = struct {
		Path   string
		Method string
	}

	object := Auth{
		Whitelist: []item{
			{
				Path:   "/hello/*",
				Method: "POST",
			},
			{
				Path:   "/welcome/lihua",
				Method: "POST",
			},
		},
	}

	tests := []struct {
		input  item
		result bool
	}{
		{
			input: item{
				Path:   "/hello/lihua",
				Method: "POST",
			},
			result: true,
		},
		{
			input: item{
				Path:   "/welcome/lihua1",
				Method: "POST",
			},
			result: false,
		},
		{
			input: item{
				Path:   "/welcome/li",
				Method: "POST",
			},
			result: false,
		},
		{
			input: item{
				Path:   "/hello/",
				Method: "POST",
			},
			result: false,
		},
	}

	for _, item := range tests {
		if object.isWhitelist(item.input.Method, item.input.Path) != item.result {
			t.Error("result error " + item.input.Path)
		}
	}
}

// 上游鉴权失败（非 200）时，中间件必须原样透传上游响应头，
// 否则前端拿不到 application/json 的响应体，无法识别 reason=UNAUTHORIZED 并触发续期。
func TestMiddlewarePassThroughAuthResponse(t *testing.T) {
	const unauthorizedBody = `{"code":401,"reason":"UNAUTHORIZED","message":"token已过期"}`

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json;charset=utf8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(unauthorizedBody))
	}))
	defer authServer.Close()

	md, err := Middleware(&config.Middleware{
		Name: "auth",
		Options: map[string]interface{}{
			"url":         authServer.URL,
			"method":      http.MethodPost,
			"contentType": "application/json",
		},
	})
	if err != nil {
		t.Fatalf("build middleware: %v", err)
	}

	var nextCalled bool
	next := middleware.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		nextCalled = true
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/psou/admin/categories", nil)
	req.Header.Set("Authorization", "Bearer expired-token")

	resp, err := md(next).RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	defer resp.Body.Close()

	if nextCalled {
		t.Fatal("auth failed but next round tripper was called")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != unauthorizedBody {
		t.Fatalf("body = %q, want %q", string(body), unauthorizedBody)
	}
}
