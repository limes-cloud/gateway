package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/limes-cloud/kratosx"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/middleware"
	"github.com/limes-cloud/gateway/proxy"
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

	return func(next http.RoundTripper) http.RoundTripper {
		return middleware.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if auth.isWhitelist(req.Method, req.URL.Path) {
				return next.RoundTrip(req)
			}

			var data any
			if req.Method == http.MethodGet || req.Method == http.MethodDelete {
				data = req.URL.Query()
			} else if strings.Contains(req.Header.Get("Content-Type"), "json") {
				dataBody, _ := io.ReadAll(req.Body)
				req.Body = io.NopCloser(bytes.NewBuffer(dataBody))
				_ = json.Unmarshal(dataBody, &data)
			}

			body := RequestInfo{
				Path:   req.URL.Path,
				Method: req.Method,
				Data:   data,
			}
			byteBody, _ := json.Marshal(body)
			request, err := http.NewRequest(auth.Method, auth.URL, bytes.NewReader(byteBody))
			//
			// jsonHeader := http.Header{}
			// jsonHeader.Add("Content-Type", auth.ContentType)
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

			kratosx.MustContext(req.Context()).
				Authentication().
				SetAuth(req, string(proxy.GetData(response)))

			return next.RoundTrip(req)
		})
	}, nil
}
