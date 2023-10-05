package client

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/limes-cloud/gateway/consts"
	"github.com/limes-cloud/kratos/middleware/tracing"
	"io"
)

type Response struct {
	Code     int32             `json:"code,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	Data     interface{}       `json:"data"`
	Message  string            `json:"message,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	TraceID  string            `json:"trace_id,omitempty"`
}

type response struct {
	data *bytes.Reader
	body io.ReadCloser
}

func (r *response) Read(b []byte) (n int, err error) {
	return r.data.Read(b)
}

func (r *response) Close() error {
	return r.body.Close()
}

func ResponseFormat(ctx context.Context, reader io.ReadCloser) (io.ReadCloser, int) {
	b, _ := io.ReadAll(reader)
	res := map[string]interface{}{}

	if json.Unmarshal(b, &res) != nil {
		return reader, 0
	}

	newRes := Response{
		Code:    consts.HTTP_SUCCESS_CODE,
		Message: consts.HTTP_SUCCESS_MESSAGE,
		TraceID: tracing.TraceID()(ctx).(string),
	}
	// 上游返回error
	if res["code"] != nil && res["reason"] != nil {
		if err := json.Unmarshal(b, &newRes); err != nil {
			return reader, 0
		}
	} else {
		newRes.Data = res
	}

	b, _ = json.Marshal(newRes)

	return &response{
		data: bytes.NewReader(b),
		body: reader,
	}, len(b)
}
