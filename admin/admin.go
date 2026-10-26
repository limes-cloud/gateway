package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/consts"
	"github.com/limes-cloud/gateway/middleware"
	"github.com/limes-cloud/gateway/middleware/auth"
)

const (
	// apiPrefix 管理 API 前缀（不经过反向代理路由表）
	apiPrefix = "/gateway/api/"
	// healthPath 探活端点（免鉴权）
	healthPath = "/gateway/api/health"
)

// Options 管理 API 配置
type Options struct {
	// AuthURL manager 鉴权服务地址（如 http://localhost:7080/manager/api/v1/auth），空 = 不鉴权
	AuthURL string
}

// Handler 网关管理 API（路由配置的增删改查 + 发布）
type Handler struct {
	loader config.Loader
	opts   *Options
	auth   middleware.Middleware // 鉴权包装器（nil = 不启用鉴权）
	mux    *http.ServeMux
}

// New 构建管理 API；底层未配置数据库时管理 API 不可用（503）
func New(loader config.Loader, opts *Options) *Handler {
	h := &Handler{loader: loader, opts: opts, mux: http.NewServeMux()}
	h.register()
	if opts != nil && opts.AuthURL != "" {
		if mw, err := auth.Middleware(&config.Middleware{
			Name: "auth",
			Options: map[string]interface{}{
				"url":    opts.AuthURL,
				"method": "POST",
			},
		}); err == nil {
			h.auth = mw
		}
	}
	return h
}

func (h *Handler) register() {
	h.mux.HandleFunc("GET /gateway/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h.mux.HandleFunc("GET /gateway/api/endpoints", h.listEndpoints)
	h.mux.HandleFunc("POST /gateway/api/endpoints", h.createEndpoint)
	h.mux.HandleFunc("PUT /gateway/api/endpoints", h.updateEndpoint)
	h.mux.HandleFunc("DELETE /gateway/api/endpoints", h.deleteEndpoint)
	h.mux.HandleFunc("POST /gateway/api/publish", h.publish)
	h.mux.HandleFunc("POST /gateway/api/publish/rollback", h.rollback)
	h.mux.HandleFunc("GET /gateway/api/publish/history", h.history)
}

// Mashup 将管理 API 挂载到主 handler：/gateway/api/ 前缀拦截，其余走反向代理。
// 管理 API 不走全局 cors 中间件，此处统一处理 CORS：预检直接放行（带 CORS 头，不鉴权）。
func (h *Handler) Mashup(origin http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, apiPrefix) {
			origin.ServeHTTP(w, r)
			return
		}
		// 回显 Origin + 允许凭据（与前端 VITE_WITH_CREDENTIALS 兼容的安全写法）
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
				w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ServeHTTP 管理 API 入口（探活 → 鉴权 → 分发）
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 探活端点始终可用（不依赖数据库）
	if r.URL.Path == healthPath {
		h.mux.ServeHTTP(w, r)
		return
	}
	if h.loader.Store() == nil {
		writeError(w, http.StatusServiceUnavailable, "database not configured")
		return
	}
	if h.auth != nil && !h.authenticate(w, r) {
		return
	}
	h.mux.ServeHTTP(w, r)
}

// authenticate 复用 auth 中间件校验 token（调用 manager 鉴权服务），通过后在请求头注入用户信息
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	// 鉴权通过后由哨兵终止链路（真正请求已由 mux 接管）
	pass := &passTripper{resp: &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
	}}
	resp, err := h.auth(pass).RoundTrip(r)
	if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
		code := http.StatusUnauthorized
		message := "unauthorized"
		if resp != nil {
			code = resp.StatusCode
			if b, _ := io.ReadAll(resp.Body); len(b) > 0 {
				message = string(b)
			}
		}
		writeError(w, code, message)
		return false
	}
	return true
}

// operator 从鉴权注入的用户信息中提取操作人（审计用）
func (h *Handler) operator(r *http.Request) string {
	info := map[string]any{}
	if raw := r.Header.Get("x-md-global-info"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &info); err == nil {
			for _, key := range []string{"username", "name", "nickname"} {
				if v, ok := info[key].(string); ok && v != "" {
					return v
				}
			}
		}
	}
	return "console"
}

// passTripper 鉴权通过的哨兵 RoundTripper（终止调用链）
type passTripper struct{ resp *http.Response }

func (t *passTripper) RoundTrip(_ *http.Request) (*http.Response, error) { return t.resp, nil }

// writeData 成功响应（项目协议：{code, message, data, reason}，code=200 前端判定为成功）
func writeData(w http.ResponseWriter, data any) {
	writeJSON(w, consts.HTTP_SUCCESS_CODE, consts.HTTP_SUCCESS_REASON, consts.HTTP_SUCCESS_MESSAGE, data)
}

// writeError 错误响应（reason 用于前端判定，如 401 触发 token 刷新）
func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, reasonOf(code), message, nil)
}

// writeJSON 统一 JSON 响应格式（与网关透传的 responseFormat 一致）
func writeJSON(w http.ResponseWriter, code int, reason, message string, data any) {
	w.Header().Set("Content-Type", "application/json;charset=utf8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"reason":  reason,
		"message": message,
		"data":    data,
	})
}

// reasonOf 状态码 → 错误原因（与前端 ApiStatus/刷新逻辑对齐）
func reasonOf(code int) string {
	if code == http.StatusUnauthorized {
		return "UNAUTHORIZED"
	}
	return http.StatusText(code)
}

// parsePaging 解析分页参数，默认 1/20
func parsePaging(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 50 {
		size = 20
	}
	return page, size
}

// parseStatus 解析 enabled 过滤参数（空 = 全部）
func parseStatus(r *http.Request) *bool {
	raw := r.URL.Query().Get("enabled")
	if raw == "" {
		return nil
	}
	v := raw == "1" || raw == "true"
	return &v
}
