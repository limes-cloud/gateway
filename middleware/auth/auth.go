package auth

import (
	"bytes"
	"encoding/json"
	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/utils"
	"io"
	"net/http"

	"github.com/limes-cloud/gateway/middleware"
)

func init() {
	middleware.Register("auth", Middleware)
}

type AuthServer struct {
	URL    string
	Method string
}

type RequestInfo struct {
	Path   string `json:"path"`
	Method string `json:"method"`
}

var _nopBody = io.NopCloser(&bytes.Buffer{})

func Middleware(c *config.Middleware) (middleware.Middleware, error) {
	options := &AuthServer{}
	if c.Options != nil {
		if err := utils.Copy(c.Options, options); err != nil {
			return nil, err
		}
	}
	return func(next http.RoundTripper) http.RoundTripper {
		return middleware.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			body := RequestInfo{
				Path:   req.URL.Path,
				Method: req.Method,
			}
			byteBody, _ := json.Marshal(body)

			options.URL = "http://localhost:7080/basic/v1/auth"
			request, err := http.NewRequest("POST", options.URL, bytes.NewReader(byteBody))
			if err != nil {
				return &http.Response{
					Status:     http.StatusText(http.StatusUnauthorized),
					StatusCode: http.StatusUnauthorized,
					Body:       _nopBody,
				}, nil
			}

			request.Header = req.Header.Clone()
			request.Header.Add("Content-Type", "application/json;charset=utf8")

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

			resp, err := next.RoundTrip(req)
			return resp, err
		})
	}, nil
}
