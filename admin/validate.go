package admin

import (
	"fmt"
	"strings"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/matcher"
	"github.com/limes-cloud/gateway/rewrite"
)

// 已注册中间件白名单（与 middleware 注册一致）。
var knownMiddlewares = map[string]bool{
	"bbr": true, "cors": true, "tracing": true, "logging": true,
	"transcoder": true, "auth": true, "signature": true,
	"circuitbreaker": true,
}

// ValidateRoutes 发布前全量校验路由。
func ValidateRoutes(routes []config.Route) error {
	seen := make(map[string]struct{}, len(routes))
	for i := range routes {
		routes[i].Normalize()
		if err := ValidateRoute(&routes[i]); err != nil {
			return fmt.Errorf("route[%d] %s: %w", i, routes[i].Name, err)
		}
		key := routeKey(&routes[i])
		if _, ok := seen[key]; ok {
			return fmt.Errorf("route[%d] %s: duplicate priority/expression", i, routes[i].Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// ValidateRoute 单条路由校验。
func ValidateRoute(route *config.Route) error {
	route.Normalize()
	if strings.TrimSpace(route.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(route.Match.Expression) == "" {
		return fmt.Errorf("match.expression is required")
	}
	if _, err := matcher.Compile(route.Match.Expression); err != nil {
		return err
	}
	if err := validateUpstream(route.Upstream); err != nil {
		return err
	}
	if err := rewrite.ValidateRules(route.Rewrites); err != nil {
		return err
	}
	if len(route.Upstream.Backends) == 0 && !hasRedirectAction(route.Rewrites) {
		return fmt.Errorf("upstream.backends is required when no redirect action exists")
	}
	if err := validateHeaderPolicy("response.headers", route.Response.Headers); err != nil {
		return err
	}
	for _, m := range route.Middlewares {
		if m.Name == "" {
			return fmt.Errorf("middleware name is required")
		}
		if !knownMiddlewares[m.Name] {
			return fmt.Errorf("unknown middleware: %s", m.Name)
		}
	}
	return nil
}

// ValidateRewriteRule 单条重写规则校验。
func ValidateRewriteRule(rule *config.RewriteRule) error {
	return rewrite.ValidateRule(rule)
}

func validateUpstream(upstream config.Upstream) error {
	if len(upstream.Backends) == 0 {
		return nil
	}
	switch upstream.Protocol {
	case "HTTP", "GRPC":
	default:
		return fmt.Errorf("unknown upstream.protocol: %s", upstream.Protocol)
	}
	if len(upstream.Backends) == 0 {
		return fmt.Errorf("upstream.backends is required")
	}
	for _, backend := range upstream.Backends {
		if strings.TrimSpace(backend.Target) == "" {
			return fmt.Errorf("upstream backend target is required")
		}
	}
	if upstream.TimeoutMs < 0 {
		return fmt.Errorf("upstream.timeoutMs must not be negative")
	}
	if upstream.Retry != nil {
		if upstream.Retry.Count < 0 {
			return fmt.Errorf("upstream.retry.count must not be negative")
		}
		if upstream.Retry.TimeoutMs < 0 {
			return fmt.Errorf("upstream.retry.timeoutMs must not be negative")
		}
	}
	return nil
}

func hasRedirectAction(rules []config.RewriteRule) bool {
	for i := range rules {
		for j := range rules[i].Actions {
			if rules[i].Actions[j].Type == config.ActionRedirect {
				return true
			}
		}
	}
	return false
}

func validateHeaderPolicy(prefix string, policy config.HeaderPolicy) error {
	for key, value := range policy.Set {
		if err := validateHeader(prefix, key, value); err != nil {
			return err
		}
	}
	for key, value := range policy.Add {
		if err := validateHeader(prefix, key, value); err != nil {
			return err
		}
	}
	for _, key := range policy.Remove {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n:") {
			return fmt.Errorf("%s.remove contains invalid header name: %q", prefix, key)
		}
	}
	return nil
}

func validateHeader(prefix, key, value string) error {
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n:") {
		return fmt.Errorf("%s contains invalid header name: %q", prefix, key)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s contains invalid value for header %s", prefix, key)
	}
	return nil
}

func routeKey(route *config.Route) string {
	return fmt.Sprintf("%d\x00%s", route.Priority, strings.TrimSpace(route.Match.Expression))
}
