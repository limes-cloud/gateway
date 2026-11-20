package config

import (
	"strings"
	"time"
)

// Match 路由或重写规则的匹配条件。
type Match struct {
	// Expression 布尔表达式，例如 req_host_in("asou.fun") && req_path_in("/")
	Expression string `json:"expression"`
	// Terminal 仅供路由内 rewrite 使用：命中并执行完当前规则后停止后续 rewrite。
	Terminal bool `json:"terminal,omitempty"`
}

// ActionType 请求重写动作类型。
type ActionType string

const (
	ActionHostSet           ActionType = "HOST_SET"
	ActionPathSet           ActionType = "PATH_SET"
	ActionPathPrefixAdd     ActionType = "PATH_PREFIX_ADD"
	ActionPathPrefixTrim    ActionType = "PATH_PREFIX_TRIM"
	ActionPathRegexReplace  ActionType = "PATH_REGEX_REPLACE"
	ActionRawQueryAdd       ActionType = "RAW_QUERY_ADD"
	ActionRawQueryRename    ActionType = "RAW_QUERY_RENAME"
	ActionRawQueryDel       ActionType = "RAW_QUERY_DEL"
	ActionRawQueryDelAllEx  ActionType = "RAW_QUERY_DEL_ALL_EXCEPT"
	ActionRawQueryClear     ActionType = "RAW_QUERY_CLEAR"
	ActionRequestHeaderSet  ActionType = "REQUEST_HEADER_SET"
	ActionRequestHeaderAdd  ActionType = "REQUEST_HEADER_ADD"
	ActionRequestHeaderDel  ActionType = "REQUEST_HEADER_DEL"
	ActionResponseHeaderSet ActionType = "RESPONSE_HEADER_SET"
	ActionResponseHeaderAdd ActionType = "RESPONSE_HEADER_ADD"
	ActionResponseHeaderDel ActionType = "RESPONSE_HEADER_DEL"
	ActionRedirect          ActionType = "REDIRECT"
)

func (a ActionType) Valid() bool {
	switch a {
	case ActionHostSet, ActionPathSet, ActionPathPrefixAdd, ActionPathPrefixTrim,
		ActionPathRegexReplace, ActionRawQueryAdd, ActionRawQueryRename,
		ActionRawQueryDel, ActionRawQueryDelAllEx, ActionRawQueryClear,
		ActionRequestHeaderSet, ActionRequestHeaderAdd, ActionRequestHeaderDel,
		ActionResponseHeaderSet, ActionResponseHeaderAdd, ActionResponseHeaderDel,
		ActionRedirect:
		return true
	default:
		return false
	}
}

// Action 一条请求重写动作。
type Action struct {
	Type   ActionType `json:"type"`
	Params any        `json:"params"`
}

func (a *Action) Normalize() {
	a.Type = ActionType(strings.ToUpper(strings.TrimSpace(string(a.Type))))
}

// Route 流量路由：表达式命中后选择一个上游转发。
type Route struct {
	ID          uint64            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Enabled     bool              `json:"enabled"`
	Priority    int               `json:"priority"`
	Match       Match             `json:"match"`
	Rewrites    []RewriteRule     `json:"rewrites"`
	Upstream    Upstream          `json:"upstream"`
	Response    Response          `json:"response"`
	Middlewares []Middleware      `json:"middlewares"`
	Metadata    map[string]string `json:"metadata"`
	CreatedAt   time.Time         `json:"-"`
	UpdatedAt   time.Time         `json:"-"`
}

// Normalize 补齐可省略的默认值。
func (r *Route) Normalize() {
	r.Match.Expression = strings.TrimSpace(r.Match.Expression)
	if r.Rewrites == nil {
		r.Rewrites = []RewriteRule{}
	}
	for i := range r.Rewrites {
		r.Rewrites[i].Normalize()
	}
}

// RewriteRule 路由内条件重写规则：表达式命中后执行动作。
type RewriteRule struct {
	Name     string   `json:"name"`
	Priority int      `json:"priority"`
	Match    Match    `json:"match"`
	Actions  []Action `json:"actions"`
}

func (r *RewriteRule) Normalize() {
	r.Match.Expression = strings.TrimSpace(r.Match.Expression)
	if r.Actions == nil {
		r.Actions = []Action{}
	}
	for i := range r.Actions {
		r.Actions[i].Normalize()
	}
}

// Upstream 上游服务配置。
type Upstream struct {
	Protocol  string    `json:"protocol"`
	TimeoutMs int64     `json:"timeoutMs"`
	Backends  []Backend `json:"backends"`
	Retry     *Retry    `json:"retry,omitempty"`
}

func (u Upstream) Timeout() time.Duration {
	return time.Duration(u.TimeoutMs) * time.Millisecond
}

// HeaderPolicy 请求头/响应头改写策略。
type HeaderPolicy struct {
	Set    map[string]string `json:"set,omitempty"`
	Add    map[string]string `json:"add,omitempty"`
	Remove []string          `json:"remove,omitempty"`
}

// Response 返回给客户端时的响应处理策略。
type Response struct {
	Format  bool         `json:"format"`
	Headers HeaderPolicy `json:"headers"`
}

// Middleware 路由级中间件配置。
type Middleware struct {
	Name     string                 `json:"name"`
	Options  map[string]interface{} `json:"options"`
	Required bool                   `json:"required"`
}

// Backend 上游节点。
type Backend struct {
	Target string `json:"target"`
	Weight *int64 `json:"weight,omitempty"`
}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Condition struct {
	Header     *Header `json:"header,omitempty"`
	StatusCode string  `json:"statusCode,omitempty"`
}

type Retry struct {
	Count      int         `json:"count"`
	TimeoutMs  int64       `json:"timeoutMs,omitempty"`
	Conditions []Condition `json:"conditions"`
}

type Tracing struct {
	Endpoint    string
	SampleRatio float64
	Timeout     time.Duration
	Insecure    bool
}

type CircuitBreaker struct {
	Trigger *struct {
		SuccessRatio *struct {
			Success float64
			Request int
			Bucket  int
			Window  time.Duration
		}
		Ratio int64
	}
	Action struct {
		ResponseData *struct {
			StatusCode int
			Header     []struct {
				Key   string
				Value []string
			}
			Body []byte
		}
		BackupService *struct {
			Route Route
		}
	}
	Conditions []Condition
}

type Cors struct {
	AllowCredentials    bool
	AllowOrigins        []string
	AllowMethods        []string
	AllowHeaders        []string
	ExposeHeaders       []string
	MaxAge              time.Duration
	AllowPrivateNetwork bool
}
