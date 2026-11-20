package proxy

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/spf13/cast"

	"github.com/limes-cloud/gateway/consts"
)

type Response struct {
	Code     int32             `json:"code,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	Data     any               `json:"data"`
	Message  string            `json:"message,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	TraceID  string            `json:"traceId,omitempty"`
}

func parseResponse(response *http.Response) ([]byte, Response, bool) {
	b, _ := io.ReadAll(response.Body)

	var res any
	if err := json.Unmarshal(b, &res); err != nil {
		return b, Response{}, false
	}

	newRes := Response{
		Code:    consts.HTTP_SUCCESS_CODE,
		Message: consts.HTTP_SUCCESS_MESSAGE,
		Reason:  consts.HTTP_SUCCESS_REASON,
		TraceID: response.Header.Get(consts.TRACE_ID),
	}

	m, ok := res.(map[string]any)
	if ok && m["code"] != nil {
		newRes.Code = cast.ToInt32(m["code"])
		newRes.Message = cast.ToString(m["message"])
		newRes.Metadata = cast.ToStringMapString(m["metadata"])
		newRes.Reason = cast.ToString(m["reason"])
		newRes.Data = m["data"]
	} else {
		newRes.Data = res
	}

	return nil, newRes, true
}

func ResponseFormat(response *http.Response) []byte {
	raw, newRes, ok := parseResponse(response)
	if !ok {
		return raw
	}
	b, _ := json.Marshal(newRes)
	return b
}

func GetData(response *http.Response) []byte {
	raw, newRes, ok := parseResponse(response)
	if !ok {
		return raw
	}
	b, _ := json.Marshal(newRes.Data)
	return b
}
