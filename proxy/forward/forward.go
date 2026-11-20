package forward

import (
	"net/http"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/middleware"
)

// New 创建转发响应处理中间件。请求头与客户端 Forwarded 头保持透传，不做 Gateway 侧重算。
func New(response config.Response) (middleware.Middleware, error) {
	return func(next http.RoundTripper) http.RoundTripper {
		return middleware.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			resp, err := next.RoundTrip(req)
			if err != nil {
				return nil, err
			}
			if resp.Header == nil {
				resp.Header = make(http.Header)
			}
			applyHeaderPolicy(resp.Header, response.Headers)
			if policy, ok := config.ResponseHeadersFrom(req); ok {
				applyResponsePolicy(resp.Header, policy)
			}
			return resp, nil
		})
	}, nil
}

func applyHeaderPolicy(header http.Header, policy config.HeaderPolicy) {
	for key, value := range policy.Set {
		header.Set(key, value)
	}
	for key, value := range policy.Add {
		header.Add(key, value)
	}
	for _, key := range policy.Remove {
		header.Del(key)
	}
}

func applyResponsePolicy(header http.Header, policy *config.ResponseHeaderPolicy) {
	if policy == nil {
		return
	}
	for key, value := range policy.Set {
		header.Set(key, value)
	}
	for key, value := range policy.Add {
		header.Add(key, value)
	}
	for _, key := range policy.Remove {
		header.Del(key)
	}
}
