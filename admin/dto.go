package admin

import (
	"time"

	"github.com/limes-cloud/gateway/config"
)

// endpointDTO 管理 API 的端点请求/响应结构（timeoutMs 毫秒整数，与库表列一致）
type endpointDTO struct {
	ID             uint64              `json:"id"`
	Path           string              `json:"path"`
	Title          string              `json:"title"`
	Method         string              `json:"method"`
	Host           string              `json:"host"`
	Protocol       string              `json:"protocol"`
	ResponseFormat bool                `json:"responseFormat"`
	TimeoutMs      int64               `json:"timeoutMs"`
	Description    string              `json:"description"`
	Metadata       map[string]string   `json:"metadata"`
	Backends       []config.Backend    `json:"backends"`
	Middlewares    []config.Middleware `json:"middlewares"`
	Retry          *config.Retry       `json:"retry"`
	Enabled        *bool               `json:"enabled"`
	CreatedAt      int64               `json:"createdAt"` // Unix 秒
	UpdatedAt      int64               `json:"updatedAt"` // Unix 秒
}

// toEndpoint 转为领域模型（DTO → Entity；enabled 未传默认启用）
func (d *endpointDTO) toEndpoint() *config.Endpoint {
	enabled := true
	if d.Enabled != nil {
		enabled = *d.Enabled
	}
	return &config.Endpoint{
		Enabled:        enabled,
		Path:           d.Path,
		Title:          d.Title,
		Method:         d.Method,
		Host:           d.Host,
		Protocol:       d.Protocol,
		ResponseFormat: d.ResponseFormat,
		TimeoutMs:      d.TimeoutMs,
		Timeout:        time.Duration(d.TimeoutMs) * time.Millisecond,
		Description:    d.Description,
		Metadata:       d.Metadata,
		Backends:       d.Backends,
		Middlewares:    d.Middlewares,
		Retry:          d.Retry,
	}
}

// toDTO 转为响应结构（Entity → DTO）
func toDTO(e *config.Endpoint) *endpointDTO {
	return &endpointDTO{
		ID:             e.ID,
		Path:           e.Path,
		Title:          e.Title,
		Method:         e.Method,
		Host:           e.Host,
		Protocol:       e.Protocol,
		ResponseFormat: e.ResponseFormat,
		TimeoutMs:      e.Timeout.Milliseconds(),
		Description:    e.Description,
		Metadata:       e.Metadata,
		Backends:       e.Backends,
		Middlewares:    e.Middlewares,
		Retry:          e.Retry,
		Enabled:        &e.Enabled,
		CreatedAt:      e.CreatedAt.Unix(),
		UpdatedAt:      e.UpdatedAt.Unix(),
	}
}

// toDTOs 批量转换（Edit 响应）
func toDTOs(list []config.Endpoint) []*endpointDTO {
	out := make([]*endpointDTO, 0, len(list))
	for i := range list {
		out = append(out, toDTO(&list[i]))
	}
	return out
}
