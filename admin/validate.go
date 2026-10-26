package admin

import (
	"fmt"
	"strings"

	"github.com/limes-cloud/gateway/config"
)

// 已注册中间件白名单（与 middleware 注册一致；未知中间件在网关启动构建时会被跳过）
var knownMiddlewares = map[string]bool{
	"bbr": true, "cors": true, "tracing": true, "logging": true,
	"transcoder": true, "auth": true, "signature": true, "rewrite": true,
	"circuitbreaker": true,
}

// ValidateEndpoints 发布前全量校验：任意一条不合法即拒绝（配置错一个字就是对线上 503）
func ValidateEndpoints(eps []config.Endpoint) error {
	for i := range eps {
		if err := ValidateEndpoint(&eps[i]); err != nil {
			return fmt.Errorf("endpoint[%d] %s: %w", i, eps[i].Path, err)
		}
	}
	return nil
}

// ValidateEndpoint 单条路由校验（path 格式 / 协议枚举 / 后端非空 / 超时非负 / 中间件已注册）
func ValidateEndpoint(e *config.Endpoint) error {
	if e.Path == "" {
		return fmt.Errorf("path is required")
	}
	if !strings.HasPrefix(e.Path, "/") {
		return fmt.Errorf("path must start with /: %s", e.Path)
	}
	switch e.Protocol {
	case "", "HTTP", "GRPC":
	default:
		return fmt.Errorf("unknown protocol: %s", e.Protocol)
	}
	if len(e.Backends) == 0 {
		return fmt.Errorf("backends is required")
	}
	for _, b := range e.Backends {
		if b.Target == "" {
			return fmt.Errorf("backend target is required")
		}
	}
	if e.Timeout < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	for _, m := range e.Middlewares {
		if m.Name == "" {
			return fmt.Errorf("middleware name is required")
		}
		if !knownMiddlewares[m.Name] {
			return fmt.Errorf("unknown middleware: %s", m.Name)
		}
	}
	if e.Retry != nil {
		if e.Retry.Count < 0 {
			return fmt.Errorf("retry count must not be negative")
		}
		if e.Retry.Timeout < 0 {
			return fmt.Errorf("retry timeout must not be negative")
		}
	}
	return nil
}
