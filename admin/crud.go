package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// listRoutes 流量路由列表。
func (h *Handler) listRoutes(w http.ResponseWriter, r *http.Request) {
	page, size := parsePaging(r)
	keyword := r.URL.Query().Get("keyword")
	routes, total, err := h.loader.Store().ListRoutes(r.Context(), page, size, keyword, parseStatus(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeData(w, map[string]any{"total": total, "list": toRouteDTOs(routes)})
}

// createRoute 新建路由（草稿态，不生效）。
func (h *Handler) createRoute(w http.ResponseWriter, r *http.Request) {
	var dto routeDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	route := dto.toRoute()
	if err := ValidateRoute(route); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.loader.Store().CreateRoute(r.Context(), route)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeData(w, map[string]any{"id": id})
}

// updateRoute 修改路由（草稿态，不生效；id 经 query 传递）。
func (h *Handler) updateRoute(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(w, r)
	if err != nil {
		return
	}
	var dto routeDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	route := dto.toRoute()
	if err := ValidateRoute(route); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.loader.Store().UpdateRoute(r.Context(), route, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeData(w, map[string]any{"id": id})
}

// deleteRoute 删除路由（草稿态，不生效）。
func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(w, r)
	if err != nil {
		return
	}
	if err := h.loader.Store().DeleteRoute(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeData(w, map[string]any{"id": id})
}

func parseID(w http.ResponseWriter, r *http.Request) (uint64, error) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, errors.New("invalid id")
	}
	return id, nil
}
