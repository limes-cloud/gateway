// Package rewrite compiles and executes route-scoped request rewrite rules.
package rewrite

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/matcher"
)

// CompiledRule 一条已完成表达式编译和动作校验的重写规则。
type CompiledRule struct {
	Rule    config.RewriteRule
	Expr    *matcher.Expression
	Actions []compiledAction
}

type compiledAction struct {
	Action config.Action
	Regex  *regexp.Regexp
}

// Result 重写阶段结果。
type Result struct {
	Redirect        *RedirectResult
	ResponseHeaders *config.ResponseHeaderPolicy
}

// RedirectResult 需要直接返回给客户端的重定向。
type RedirectResult struct {
	StatusCode int
	Location   string
}

// CompileRules 编译路由内全部条件重写规则；任何一条不合法都会返回错误。
func CompileRules(rules []config.RewriteRule) ([]CompiledRule, error) {
	out := make([]CompiledRule, 0, len(rules))
	for i := range rules {
		rule := rules[i]
		rule.Normalize()
		if err := ValidateRule(&rule); err != nil {
			return nil, fmt.Errorf("rewrite rule %q: %w", rule.Name, err)
		}
		expr, err := matcher.Compile(rule.Match.Expression)
		if err != nil {
			return nil, fmt.Errorf("rewrite rule %q: %w", rule.Name, err)
		}
		actions := make([]compiledAction, 0, len(rule.Actions))
		for _, action := range rule.Actions {
			action.Normalize()
			compiled := compiledAction{Action: action}
			if action.Type == config.ActionPathRegexReplace {
				compiled.Regex = regexp.MustCompile(paramString(action.Params, "pattern"))
			}
			actions = append(actions, compiled)
		}
		out = append(out, CompiledRule{Rule: rule, Expr: expr, Actions: actions})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Rule.Priority > out[j].Rule.Priority
	})
	return out, nil
}

// ValidateRules 校验同一路由下的全部重写规则。
func ValidateRules(rules []config.RewriteRule) error {
	for i := range rules {
		if err := ValidateRule(&rules[i]); err != nil {
			return fmt.Errorf("rewrite[%d] %s: %w", i, rules[i].Name, err)
		}
	}
	return nil
}

// ValidateRule 校验单条重写规则。
func ValidateRule(rule *config.RewriteRule) error {
	rule.Normalize()
	if strings.TrimSpace(rule.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if _, err := matcher.Compile(rule.Match.Expression); err != nil {
		return err
	}
	if len(rule.Actions) == 0 {
		return fmt.Errorf("actions must not be empty")
	}
	return ValidateActions(rule.Actions)
}

// ValidateActions 校验动作参数。发布前调用，运行时不再做类型猜测。
func ValidateActions(actions []config.Action) error {
	for i := range actions {
		actions[i].Normalize()
		if err := validateAction(&actions[i]); err != nil {
			return fmt.Errorf("action[%d] %s: %w", i, actions[i].Type, err)
		}
		if actions[i].Type == config.ActionRedirect && i != len(actions)-1 {
			return fmt.Errorf("action[%d] REDIRECT must be the last action", i)
		}
	}
	return nil
}

func validateAction(action *config.Action) error {
	if !action.Type.Valid() {
		return fmt.Errorf("unknown action type")
	}
	if _, err := paramsMap(action.Params); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	switch action.Type {
	case config.ActionHostSet:
		if strings.TrimSpace(paramString(action.Params, "value")) == "" {
			return fmt.Errorf("params.value is required")
		}
		return validateHeaderValue(paramString(action.Params, "value"))
	case config.ActionPathSet:
		return validatePath(paramString(action.Params, "value"), "params.value")
	case config.ActionPathPrefixAdd, config.ActionPathPrefixTrim:
		return validatePath(paramString(action.Params, "prefix"), "params.prefix")
	case config.ActionPathRegexReplace:
		pattern := paramString(action.Params, "pattern")
		if pattern == "" || paramString(action.Params, "replacement") == "" {
			return fmt.Errorf("params.pattern and params.replacement are required")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("invalid params.pattern: %w", err)
		}
		return nil
	case config.ActionRawQueryAdd:
		if strings.TrimSpace(paramString(action.Params, "key")) == "" {
			return fmt.Errorf("params.key is required")
		}
		return nil
	case config.ActionRawQueryRename:
		key, newKey := paramString(action.Params, "key"), paramString(action.Params, "newKey")
		if strings.TrimSpace(key) == "" || strings.TrimSpace(newKey) == "" {
			return fmt.Errorf("params.key and params.newKey are required")
		}
		if key == newKey {
			return fmt.Errorf("params.key and params.newKey must be different")
		}
		return nil
	case config.ActionRawQueryDel:
		if strings.TrimSpace(paramString(action.Params, "key")) == "" {
			return fmt.Errorf("params.key is required")
		}
		return nil
	case config.ActionRawQueryDelAllEx:
		keys := paramStrings(action.Params, "keys")
		if len(keys) == 0 {
			return fmt.Errorf("params.keys must not be empty")
		}
		for _, key := range keys {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("params.keys contains empty value")
			}
		}
		return nil
	case config.ActionRawQueryClear:
		return nil
	case config.ActionRequestHeaderSet, config.ActionRequestHeaderAdd,
		config.ActionResponseHeaderSet, config.ActionResponseHeaderAdd:
		if err := validateHeaderName(paramString(action.Params, "key")); err != nil {
			return err
		}
		return validateHeaderValue(paramString(action.Params, "value"))
	case config.ActionRequestHeaderDel, config.ActionResponseHeaderDel:
		return validateHeaderName(paramString(action.Params, "key"))
	case config.ActionRedirect:
		if !allowedRedirectCode(paramInt(action.Params, "statusCode")) {
			return fmt.Errorf("unsupported status code: %d", paramInt(action.Params, "statusCode"))
		}
		return validateLocation(paramString(action.Params, "location"))
	default:
		return fmt.Errorf("unknown action type")
	}
}

func validatePath(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if !strings.HasPrefix(value, "/") {
		return fmt.Errorf("%s must start with /", field)
	}
	return nil
}

func allowedRedirectCode(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func validateHeaderName(name string) error {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n:") {
		return fmt.Errorf("invalid header name %q", name)
	}
	return nil
}

func validateHeaderValue(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("header value must not contain line breaks")
	}
	return nil
}

func validateLocation(location string) error {
	location = strings.TrimSpace(location)
	if location == "" {
		return fmt.Errorf("location is required")
	}
	if strings.ContainsAny(location, "\r\n") {
		return fmt.Errorf("location must not contain line breaks")
	}
	lower := strings.ToLower(location)
	if !strings.HasPrefix(location, "/") && !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return fmt.Errorf("location must start with /, http:// or https://")
	}
	return nil
}

// Apply 按优先级执行命中的重写规则。条件基于当前请求，前一条非 terminal 规则的动作会影响后一条规则。
func Apply(req *http.Request, rules []CompiledRule) (*Result, error) {
	result := &Result{}
	for i := range rules {
		rule := &rules[i]
		if !rule.Expr.Eval(req) {
			continue
		}
		for j := range rule.Actions {
			redirect, err := applyAction(req, &rule.Actions[j], result)
			if err != nil {
				return nil, fmt.Errorf("rewrite rule %q action[%d]: %w", rule.Rule.Name, j, err)
			}
			if redirect != nil {
				result.Redirect = redirect
				return result, nil
			}
		}
		if rule.Rule.Match.Terminal {
			break
		}
	}
	return result, nil
}

func applyAction(req *http.Request, action *compiledAction, result *Result) (*RedirectResult, error) {
	params := action.Action.Params
	switch action.Action.Type {
	case config.ActionHostSet:
		req.Host = paramString(params, "value")
	case config.ActionPathSet:
		setPath(req, paramString(params, "value"))
	case config.ActionPathPrefixAdd:
		setPath(req, joinPath(paramString(params, "prefix"), requestPath(req)))
	case config.ActionPathPrefixTrim:
		prefix := paramString(params, "prefix")
		path := requestPath(req)
		if strings.HasPrefix(path, prefix) {
			setPath(req, strings.TrimPrefix(path, prefix))
		}
	case config.ActionPathRegexReplace:
		setPath(req, action.Regex.ReplaceAllString(requestPath(req), paramString(params, "replacement")))
	case config.ActionRawQueryAdd:
		query, err := parseQuery(req)
		if err != nil {
			return nil, err
		}
		query.Add(paramString(params, "key"), paramString(params, "value"))
		req.URL.RawQuery = query.Encode()
	case config.ActionRawQueryRename:
		query, err := parseQuery(req)
		if err != nil {
			return nil, err
		}
		key, newKey := paramString(params, "key"), paramString(params, "newKey")
		values, ok := query[key]
		if ok {
			query.Del(key)
			query[newKey] = values
			req.URL.RawQuery = query.Encode()
		}
	case config.ActionRawQueryDel:
		query, err := parseQuery(req)
		if err != nil {
			return nil, err
		}
		query.Del(paramString(params, "key"))
		req.URL.RawQuery = query.Encode()
	case config.ActionRawQueryDelAllEx:
		query, err := parseQuery(req)
		if err != nil {
			return nil, err
		}
		keep := make(map[string]struct{}, len(paramStrings(params, "keys")))
		for _, key := range paramStrings(params, "keys") {
			keep[key] = struct{}{}
		}
		for key := range query {
			if _, ok := keep[key]; !ok {
				query.Del(key)
			}
		}
		req.URL.RawQuery = query.Encode()
	case config.ActionRawQueryClear:
		req.URL.RawQuery = ""
	case config.ActionRequestHeaderSet:
		req.Header.Set(paramString(params, "key"), paramString(params, "value"))
	case config.ActionRequestHeaderAdd:
		req.Header.Add(paramString(params, "key"), paramString(params, "value"))
	case config.ActionRequestHeaderDel:
		req.Header.Del(paramString(params, "key"))
	case config.ActionResponseHeaderSet:
		responsePolicy(result).Set[paramString(params, "key")] = paramString(params, "value")
	case config.ActionResponseHeaderAdd:
		responsePolicy(result).Add[paramString(params, "key")] = paramString(params, "value")
	case config.ActionResponseHeaderDel:
		policy := responsePolicy(result)
		policy.Remove = append(policy.Remove, paramString(params, "key"))
	case config.ActionRedirect:
		location, err := resolveLocation(req, params)
		if err != nil {
			return nil, err
		}
		return &RedirectResult{StatusCode: paramInt(params, "statusCode"), Location: location}, nil
	default:
		return nil, fmt.Errorf("unknown action type %s", action.Action.Type)
	}
	return nil, nil
}

func responsePolicy(result *Result) *config.ResponseHeaderPolicy {
	if result.ResponseHeaders == nil {
		result.ResponseHeaders = &config.ResponseHeaderPolicy{}
	}
	if result.ResponseHeaders.Set == nil {
		result.ResponseHeaders.Set = map[string]string{}
	}
	if result.ResponseHeaders.Add == nil {
		result.ResponseHeaders.Add = map[string]string{}
	}
	return result.ResponseHeaders
}

func paramsMap(params any) (map[string]any, error) {
	switch value := params.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return value, nil
	case json.RawMessage:
		out := map[string]any{}
		if err := json.Unmarshal(value, &out); err != nil {
			return nil, err
		}
		return out, nil
	case []byte:
		out := map[string]any{}
		if err := json.Unmarshal(value, &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		body, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		out := map[string]any{}
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

func paramString(params any, key string) string {
	values, err := paramsMap(params)
	if err != nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func paramStrings(params any, key string) []string {
	values, err := paramsMap(params)
	if err != nil {
		return nil
	}
	switch value := values[key].(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func paramInt(params any, key string) int {
	values, err := paramsMap(params)
	if err != nil {
		return 0
	}
	switch value := values[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(value)
		return parsed
	default:
		return 0
	}
}

func paramBoolDefault(params any, key string, defaultValue bool) bool {
	values, err := paramsMap(params)
	if err != nil {
		return defaultValue
	}
	if value, ok := values[key].(bool); ok {
		return value
	}
	return defaultValue
}

func parseQuery(req *http.Request) (url.Values, error) {
	if req.URL == nil {
		return nil, fmt.Errorf("request URL is nil")
	}
	query, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid raw query: %w", err)
	}
	return query, nil
}

func resolveLocation(req *http.Request, params any) (string, error) {
	raw := paramString(params, "location")
	location := strings.NewReplacer(
		"${request.scheme}", requestScheme(req),
		"${request.host}", config.RequestHostWithoutPort(req.Host),
		"${request.hostWithPort}", req.Host,
		"${request.path}", requestPath(req),
		"${request.query}", req.URL.RawQuery,
	).Replace(raw)
	if paramBoolDefault(params, "preserveQuery", true) && req.URL.RawQuery != "" && !strings.Contains(raw, "${request.query}") {
		location = appendQuery(location, req.URL.RawQuery)
	}
	if err := validateLocation(location); err != nil {
		return "", err
	}
	return location, nil
}

func appendQuery(location, rawQuery string) string {
	base, fragment := location, ""
	if index := strings.IndexByte(location, '#'); index >= 0 {
		base, fragment = location[:index], location[index:]
	}
	separator := "?"
	if strings.Contains(base, "?") {
		separator = "&"
	}
	return base + separator + rawQuery + fragment
}

func requestScheme(req *http.Request) string {
	if scheme := strings.ToLower(strings.TrimSpace(req.Header.Get("X-Forwarded-Proto"))); scheme == "http" || scheme == "https" {
		return scheme
	}
	if req.TLS != nil {
		return "https"
	}
	return "http"
}

func requestPath(req *http.Request) string {
	if req.URL == nil || req.URL.Path == "" {
		return "/"
	}
	return req.URL.Path
}

func setPath(req *http.Request, path string) {
	if req.URL == nil {
		return
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req.URL.Path = path
	req.URL.RawPath = ""
}

func joinPath(prefix, path string) string {
	prefix = strings.TrimRight(prefix, "/")
	if path == "" || path == "/" {
		return prefix
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return prefix + path
}
