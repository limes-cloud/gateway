package proxy

import (
	"encoding/json"
	"github.com/limes-cloud/gateway/consts"
	"io"
	"net/http"
)

type Response struct {
	Code     int32             `json:"code,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	Data     interface{}       `json:"data"`
	Message  string            `json:"message,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	TraceID  string            `json:"trace_id,omitempty"`
}

func ResponseFormat(response *http.Response) []byte {
	b, _ := io.ReadAll(response.Body)

	res := map[string]interface{}{}
	if json.Unmarshal(b, &res) != nil {
		return b
	}

	newRes := Response{
		Code:    consts.HTTP_SUCCESS_CODE,
		Message: consts.HTTP_SUCCESS_MESSAGE,
		Reason:  consts.HTTP_SUCCESS_REASON,
		TraceID: response.Header.Get(consts.TRACE_ID),
	}
	// 上游返回error
	if res["code"] != nil && res["reason"] != nil {
		newRes.Code, _ = res["code"].(int32)
		newRes.Message, _ = res["message"].(string)
		newRes.Metadata, _ = res["metadata"].(map[string]string)
		newRes.Reason, _ = res["reason"].(string)
	} else {
		newRes.Data = res
	}

	b, _ = json.Marshal(newRes)
	return b
}
