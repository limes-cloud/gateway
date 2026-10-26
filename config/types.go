package config

import "time"

type Endpoint struct {
	ID             uint64            `json:"id"`
	Path           string            `json:"path"`
	Title          string            `json:"title"` // 路由名称
	Method         string            `json:"method"`
	Description    string            `json:"description"`
	Protocol       string            `json:"protocol"`
	ResponseFormat bool              `json:"responseFormat"`
	Timeout        time.Duration     `json:"-"`
	TimeoutMs      int64             `json:"timeoutMs"`
	Metadata       map[string]string `json:"metadata"`
	Host           string            `json:"host"`
	Middlewares    []Middleware      `json:"middlewares"`
	Backends       []Backend         `json:"backends"`
	Retry          *Retry            `json:"retry"`
	// Enabled 软开关：false 即不下发（管理 API 可编辑；运行时读库用）
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

type Middleware struct {
	Name     string                 `json:"name"`
	Options  map[string]interface{} `json:"options"`
	Required bool                   `json:"required"`
}

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
	Count      int           `json:"count"`
	Timeout    time.Duration `json:"-"`
	TimeoutMs  int64         `json:"timeoutMs,omitempty"`
	Conditions []Condition   `json:"conditions"`
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
			Endpoint Endpoint
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

type RewriteHeadersPolicy struct {
	Set    map[string]string
	Add    map[string]string
	Remove []string
}

type Rewrite struct {
	PathRewrite            string
	RequestHeadersRewrite  *RewriteHeadersPolicy
	ResponseHeadersRewrite *RewriteHeadersPolicy
	StripPrefix            string
	HostRewrite            string
}
