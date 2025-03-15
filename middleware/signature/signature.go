package signature

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/middleware"
	"github.com/limes-cloud/gateway/utils"
)

const (
	timeHeader  = "x-md-sign-time"
	tokenHeader = "x-md-sign-token"
)

func init() {
	middleware.Register("signature", Middleware)
}

type Signature struct {
	Ak string
	Sk string
}

func Middleware(c *config.Middleware) (middleware.Middleware, error) {
	sign := &Signature{}
	if c.Options != nil {
		if err := utils.Copy(c.Options, sign); err != nil {
			return nil, err
		}
	}

	return func(next http.RoundTripper) http.RoundTripper {
		return middleware.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			var content []byte
			if req.Method == http.MethodGet || req.Method == http.MethodDelete {
				content = []byte(req.URL.Query().Encode())
			} else {
				dataBody, _ := io.ReadAll(req.Body)
				req.Body = io.NopCloser(bytes.NewBuffer(dataBody))
				content = dataBody
			}

			ts := time.Now().Unix()
			timestamp := strconv.FormatInt(ts, 10)
			// 添加时间戳
			content = append(content, []byte(fmt.Sprintf("|%s", timestamp))...)
			// 添加ak
			content = append(content, []byte(fmt.Sprintf("|%s", sign.Ak))...)
			// 加签
			her := hmac.New(sha256.New, []byte(sign.Sk))
			her.Write(content)

			req.Header.Set(timeHeader, timestamp)
			req.Header.Set(tokenHeader, hex.EncodeToString(her.Sum(nil)))
			return next.RoundTrip(req)
		})
	}, nil
}
