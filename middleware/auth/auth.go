package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/limes-cloud/gateway/proxy"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/middleware"
	"github.com/limes-cloud/gateway/utils"
)

const (
	infoKey  = "x-md-global-info"
	tokenKey = "x-md-global-token"
)

func init() {
	middleware.Register("auth", Middleware)
}

type Auth struct {
	URL         string
	Method      string
	ContentType string
	Whitelist   []struct {
		Path   string
		Method string
	}
	// 白名单正则，Middleware 构造时预编译，避免每个请求重复编译
	whitelistRegexps []*regexp.Regexp
}

// compileWhitelist 将白名单路径预编译为正则，供 isWhitelist 匹配使用
func (as *Auth) compileWhitelist() {
	if len(as.Whitelist) == 0 {
		return
	}
	as.whitelistRegexps = make([]*regexp.Regexp, 0, len(as.Whitelist))
	for _, item := range as.Whitelist {
		// 将*替换为匹配任意多字符的正则表达式
		pattern := regexp.MustCompile(`/\*`).ReplaceAllString("^"+item.Path+"$", "/.+")
		as.whitelistRegexps = append(as.whitelistRegexps, regexp.MustCompile(pattern))
	}
}

func (as *Auth) isWhitelist(method, path string) bool {
	for i, item := range as.Whitelist {
		if method != item.Method {
			continue
		}

		// 优先使用预编译的正则
		if i < len(as.whitelistRegexps) && as.whitelistRegexps[i] != nil {
			if as.whitelistRegexps[i].MatchString(path) {
				return true
			}
			continue
		}

		// 未预编译（如直接构造 Auth）时按需编译
		pattern := regexp.MustCompile(`/\*`).ReplaceAllString("^"+item.Path+"$", "/.+")
		if regexp.MustCompile(pattern).MatchString(path) {
			return true
		}
	}

	return false
}

type RequestInfo struct {
	Path   string `json:"path"`
	Method string `json:"method"`
	Data   any    `json:"data"`
}

var _nopBody = io.NopCloser(&bytes.Buffer{})

func Middleware(c *config.Middleware) (middleware.Middleware, error) {
	auth := &Auth{}
	if c.Options != nil {
		if err := utils.Copy(c.Options, auth); err != nil {
			return nil, err
		}
		if auth.ContentType == "" {
			auth.ContentType = "application/json;charset=utf8"
		}
	}
	auth.compileWhitelist()

	return func(next http.RoundTripper) http.RoundTripper {
		return middleware.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if auth.isWhitelist(req.Method, req.URL.Path) {
				return next.RoundTrip(req)
			}

			// 获取token
			token := req.Header.Get("Authorization")
			token = strings.TrimPrefix(token, "Bearer ")
			if token != "" {
				req.Header.Set(tokenKey, token)
			}

			body := RequestInfo{
				Path:   req.URL.Path,
				Method: req.Method,
			}
			byteBody, _ := json.Marshal(body)
			request, err := http.NewRequest(auth.Method, auth.URL, bytes.NewReader(byteBody))
			if err != nil {
				return &http.Response{
					Status:     http.StatusText(http.StatusUnauthorized),
					StatusCode: http.StatusUnauthorized,
					Body:       _nopBody,
					// Header:     jsonHeader,
				}, nil
			}

			request.Header = req.Header.Clone()
			request.Header.Add("Content-Type", auth.ContentType)

			client := http.Client{}
			response, err := client.Do(request)
			if err != nil {
				return &http.Response{
					Status:     http.StatusText(http.StatusUnauthorized),
					StatusCode: http.StatusUnauthorized,
					Body:       _nopBody,
					// Header:     jsonHeader,
				}, nil
			}

			if response.StatusCode != http.StatusOK {
				return &http.Response{
					Status:     http.StatusText(response.StatusCode),
					StatusCode: response.StatusCode,
					Body:       response.Body,
					// Header:     jsonHeader,
				}, nil
			}

			req.Header.Set(infoKey, string(proxy.GetData(response)))

			return next.RoundTrip(req)
		})
	}, nil
}
