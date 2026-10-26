package admin

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/limes-cloud/gateway/config"
)

// publishRequest 发布请求
type publishRequest struct {
	Remark string `json:"remark"`
}

// rollbackRequest 回滚请求
type rollbackRequest struct {
	Version int64  `json:"version"`
	Remark  string `json:"remark"`
}

// publish 发布：校验 → 事务写库 → 快照 → 通知（唯一生效入口，改表不自动生效）
func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	// ① 读取当前全部路由（含草稿变更）
	eps, err := h.loader.Store().LoadAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// ② 全量校验：任意一条不合法即拒绝发布，不产生任何变更
	if err := ValidateEndpoints(eps); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// ③ 发布事务（生成版本 + 写历史）
	snap, err := h.loader.Store().Publish(r.Context(), h.operator(r), req.Remark)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// ④ 快照 + 通知（网关实例收到后回源拉取）
	h.notify(r.Context(), snap)
	writeData(w, map[string]any{"version": snap.Version})
}

// rollback 回滚：按 history 版本恢复并重新发布（产生新版本，保留完整审计链）
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

// history 发布历史（分页）
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
		CreatedAt int64  `json:"createdAt"` // Unix 秒
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

// notify 写共享快照 + 发布通知（通知只含版本号，实例收到后回源拉取，容忍丢消息）
func (h *Handler) notify(ctx context.Context, snap *config.Snapshot) {
	if err := h.loader.Notify(ctx, snap); err != nil {
		// 通知失败不阻塞发布结果（对账兜底会补拉）
		return
	}
}
