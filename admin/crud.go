package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/limes-cloud/gateway/config"
)

// listEndpoints 路由列表（分页 + 过滤）
func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request) {
	page, size := parsePaging(r)
	keyword := r.URL.Query().Get("keyword")
	eps, total, err := h.loader.Store().ListEndpoints(r.Context(), page, size, keyword, parseStatus(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeData(w, map[string]any{"total": total, "list": toDTOs(eps)})
}

// createEndpoint 新建路由（草稿态，不生效）
func (h *Handler) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var dto endpointDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	ep := dto.toEndpoint()
	if err := ValidateEndpoint(ep); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.loader.Store().CreateEndpoint(r.Context(), ep)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeData(w, map[string]any{"id": id})
}

// updateEndpoint 修改路由（草稿态，不生效；id 经 query 传递）
func (h *Handler) updateEndpoint(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(w, r)
	if err != nil {
		return
	}
	var dto endpointDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	ep := dto.toEndpoint()
	if err := ValidateEndpoint(ep); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.loader.Store().UpdateEndpoint(r.Context(), ep, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeData(w, map[string]any{"id": id})
}

// deleteEndpoint 删除路由（草稿态，不生效；id 经 query 传递）
func (h *Handler) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(w, r)
	if err != nil {
		return
	}
	if err := h.loader.Store().DeleteEndpoint(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeData(w, map[string]any{"id": id})
}

// parseID 解析 query 中的 id 参数
func parseID(w http.ResponseWriter, r *http.Request) (uint64, error) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, errors.New("invalid id")
	}
	return id, nil
}

var _ = config.Endpoint{}
