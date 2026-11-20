package matcher

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const nonBrowserHomeExpression = `req_method_in("GET", "HEAD") && req_path_in("/") && req_host_in("asou.fun", "www.asou.fun", "poposo.cc", "www.poposo.cc", "psou.us", "www.psou.us") && (req_ua_contains_any("spider", "bot", "crawler", "slurp") || !req_header_value_contain("User-Agent", "Mozilla", true))`

func TestNonBrowserHomeExpression(t *testing.T) {
	expr, err := Compile(nonBrowserHomeExpression)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		method string
		url    string
		host   string
		ua     string
		want   bool
	}{
		{name: "curl homepage", method: http.MethodGet, url: "http://psou.us/", host: "psou.us", ua: "curl/8.0", want: true},
		{name: "empty ua homepage", method: http.MethodHead, url: "http://www.asou.fun/", host: "www.asou.fun", want: true},
		{name: "browser homepage", method: http.MethodGet, url: "http://psou.us/", host: "psou.us", ua: "Mozilla/5.0 Chrome/140", want: false},
		{name: "googlebot homepage", method: http.MethodGet, url: "http://psou.us/", host: "psou.us", ua: "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", want: true},
		{name: "bingbot homepage", method: http.MethodGet, url: "http://psou.us/", host: "psou.us", ua: "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)", want: true},
		{name: "non browser api", method: http.MethodGet, url: "http://psou.us/psou/api/categories", host: "psou.us", ua: "curl/8.0", want: false},
		{name: "non browser admin", method: http.MethodGet, url: "http://admin.psou.us/", host: "admin.psou.us", ua: "curl/8.0", want: false},
		{name: "non browser gateway root", method: http.MethodGet, url: "http://gw.psou.us/", host: "gw.psou.us", ua: "curl/8.0", want: false},
		{name: "post homepage", method: http.MethodPost, url: "http://psou.us/", host: "psou.us", ua: "curl/8.0", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.url, nil)
			req.Host = tt.host
			if tt.ua != "" {
				req.Header.Set("User-Agent", tt.ua)
			}
			if got := expr.Eval(req); got != tt.want {
				t.Fatalf("Eval() = %v, want %v", got, tt.want)
			}
		})
	}
}
