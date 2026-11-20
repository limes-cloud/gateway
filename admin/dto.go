package admin

import "github.com/limes-cloud/gateway/config"

// routeDTO 流量路由请求/响应结构。
type routeDTO struct {
	ID          uint64               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Enabled     *bool                `json:"enabled"`
	Priority    int                  `json:"priority"`
	Match       config.Match         `json:"match"`
	Rewrites    []config.RewriteRule `json:"rewrites"`
	Upstream    config.Upstream      `json:"upstream"`
	Response    config.Response      `json:"response"`
	Middlewares []config.Middleware  `json:"middlewares"`
	Metadata    map[string]string    `json:"metadata"`
	CreatedAt   int64                `json:"createdAt"`
	UpdatedAt   int64                `json:"updatedAt"`
}

func (d *routeDTO) toRoute() *config.Route {
	enabled := true
	if d.Enabled != nil {
		enabled = *d.Enabled
	}
	route := &config.Route{
		Name:        d.Name,
		Description: d.Description,
		Enabled:     enabled,
		Priority:    d.Priority,
		Match:       d.Match,
		Rewrites:    d.Rewrites,
		Upstream:    d.Upstream,
		Response:    d.Response,
		Middlewares: d.Middlewares,
		Metadata:    d.Metadata,
	}
	route.Normalize()
	return route
}

func toRouteDTO(route *config.Route) *routeDTO {
	enabled := route.Enabled
	return &routeDTO{
		ID:          route.ID,
		Name:        route.Name,
		Description: route.Description,
		Enabled:     &enabled,
		Priority:    route.Priority,
		Match:       route.Match,
		Rewrites:    route.Rewrites,
		Upstream:    route.Upstream,
		Response:    route.Response,
		Middlewares: route.Middlewares,
		Metadata:    route.Metadata,
		CreatedAt:   route.CreatedAt.Unix(),
		UpdatedAt:   route.UpdatedAt.Unix(),
	}
}

func toRouteDTOs(list []config.Route) []*routeDTO {
	out := make([]*routeDTO, 0, len(list))
	for i := range list {
		out = append(out, toRouteDTO(&list[i]))
	}
	return out
}
