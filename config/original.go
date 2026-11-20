package config

import (
	"context"
	"net"
	"net/http"
	"strings"
)

type responseHeadersKey struct{}

// ResponseHeaderPolicy 重写规则中声明的响应头动作，在收到上游响应后执行。
type ResponseHeaderPolicy struct {
	Set    map[string]string
	Add    map[string]string
	Remove []string
}

// WithResponseHeaders 把响应头策略写入 context。
func WithResponseHeaders(req *http.Request, policy *ResponseHeaderPolicy) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), responseHeadersKey{}, policy))
}

// ResponseHeadersFrom 读取响应头策略。
func ResponseHeadersFrom(req *http.Request) (*ResponseHeaderPolicy, bool) {
	policy, ok := req.Context().Value(responseHeadersKey{}).(*ResponseHeaderPolicy)
	return policy, ok
}

// RequestHostWithoutPort 返回不带端口的 Host。
func RequestHostWithoutPort(hostPort string) string {
	hostPort = strings.TrimSpace(hostPort)
	if host, _, err := net.SplitHostPort(hostPort); err == nil {
		return host
	}
	return strings.Trim(hostPort, "[]")
}
