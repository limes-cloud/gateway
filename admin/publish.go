package admin

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/limes-cloud/gateway/config"
)

type publishRequest struct {
	Remark string `json:"remark"`
}

type rollbackRequest struct {
	Version int64  `json:"version"`
	Remark  string `json:"remark"`
}

// publish 发布：校验 → 事务写库 → 快照 → 通知（唯一生效入口）。
func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	routes, err := h.loader.Store().LoadAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := ValidateRoutes(routes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	snap, err := h.loader.Store().Publish(r.Context(), h.operator(r), req.Remark)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.notify(r.Context(), snap)
	writeData(w, map[string]any{"version": snap.Version})
}

// rollback 回滚：按 history 版本恢复并重新发布。
func (h *Handler) rollback(w http.ResponseWriter, r *http.Request) {
	var req rollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Version <= 0 {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	snap, err := h.loader.Store().Rollback(r.Context(), req.Version, h.operator(r), req.Remark)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.notify(r.Context(), snap)
	writeData(w, map[string]any{"version": snap.Version})
}

// history 发布历史（分页）。
func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	page, size := parsePaging(r)
	rows, total, err := h.loader.Store().ListHistory(r.Context(), page, size)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type historyItem struct {
		Version   int64  `json:"version"`
		Operator  string `json:"operator"`
		Remark    string `json:"remark"`
		CreatedAt int64  `json:"createdAt"`
	}
	list := make([]*historyItem, 0, len(rows))
	for _, row := range rows {
		list = append(list, &historyItem{
			Version:   row.Version,
			Operator:  row.Operator,
			Remark:    row.Remark,
			CreatedAt: row.CreatedAt.Unix(),
		})
	}
	writeData(w, map[string]any{"total": total, "list": list})
}

func (h *Handler) notify(ctx context.Context, snap *config.Snapshot) {
	if err := h.loader.Notify(ctx, snap); err != nil {
		return
	}
}
