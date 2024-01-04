package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/middleware"
	"github.com/limes-cloud/gateway/utils"
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
}

func (as *Auth) isWhitelist(method, path string) bool {
	for _, item := range as.Whitelist {
		if method != item.Method {
			continue
		}

		// 将*替换为匹配任意多字符的正则表达式
		pattern := "^" + item.Path + "$"
		pattern = regexp.MustCompile(`/\*`).ReplaceAllString(pattern, "/.+")

		// 编译正则表达式
		re := regexp.MustCompile(pattern)

		// 检查输入是否匹配正则表达式
		if re.MatchString(path) {
			return true
		}
	}

	return false
}

type RequestInfo struct {
	Path   string `json:"path"`
	Method string `json:"method"`
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

	return func(next http.RoundTripper) http.RoundTripper {
		return middleware.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if auth.isWhitelist(req.Method, req.URL.Path) {
				return next.RoundTrip(req)
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
				}, nil
			}

			if response.StatusCode != http.StatusOK {
				return &http.Response{
					Status:     http.StatusText(response.StatusCode),
					StatusCode: response.StatusCode,
					Body:       response.Body,
					Header:     make(http.Header),
				}, nil
			}

			return next.RoundTrip(req)
		})
	}, nil
}
