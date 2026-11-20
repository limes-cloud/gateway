package config

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// RouteModel gw_route 行模型。
type RouteModel struct {
	ID              uint64    `gorm:"column:id;primaryKey"`
	Name            string    `gorm:"column:name"`
	Description     string    `gorm:"column:description"`
	Enabled         bool      `gorm:"column:enabled"`
	Priority        int       `gorm:"column:priority"`
	MatchExpression string    `gorm:"column:match_expression"`
	Rewrites        string    `gorm:"column:rewrites"`
	Upstream        string    `gorm:"column:upstream"`
	Response        string    `gorm:"column:response"`
	Middlewares     string    `gorm:"column:middlewares"`
	Metadata        string    `gorm:"column:metadata"`
	CreatedAt       time.Time `gorm:"column:created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at"`
}

func (RouteModel) TableName() string { return "gw_route" }

// HistoryModel gw_route_history 行模型。
type HistoryModel struct {
	ID        uint64    `gorm:"column:id;primaryKey"`
	Version   int64     `gorm:"column:version"`
	Snapshot  string    `gorm:"column:snapshot"`
	Operator  string    `gorm:"column:operator"`
	Remark    string    `gorm:"column:remark"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (HistoryModel) TableName() string { return "gw_route_history" }

// Snapshot 发布快照：某版本的全量路由。
type Snapshot struct {
	Version int64   `json:"version"`
	Routes  []Route `json:"routes"`
}

// Store 网关配置的数据库访问层（gateway 自有库）。
type Store struct {
	db *gorm.DB
}

// NewStoreFromDB 使用已有 gorm.DB 构建 Store（便于测试和依赖注入）。
func NewStoreFromDB(db *gorm.DB) *Store {
	return &Store{db: db}
}

// NewStore 建立数据库连接（连接失败返回 error，由调用方降级处理）。
func NewStore(c *DBConf) (*Store, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.Connect.Username, c.Connect.Password, c.Connect.Host, c.Connect.Port, c.Connect.DBName)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}
	return NewStoreFromDB(db), nil
}

// LoadPublished 加载全部启用路由。
func (s *Store) LoadPublished(ctx context.Context) ([]Route, int64, error) {
	var routeRows []*RouteModel
	if err := s.db.WithContext(ctx).Where("enabled = ?", 1).Order("priority DESC, id ASC").Find(&routeRows).Error; err != nil {
		return nil, 0, err
	}
	routes, err := toRoutes(routeRows)
	if err != nil {
		return nil, 0, err
	}
	ver, err := s.LatestVersion(ctx)
	if err != nil {
		return routes, 0, err
	}
	return routes, ver, nil
}

// LatestVersion 获取最新发布版本号（无发布记录返回 0）。
func (s *Store) LatestVersion(ctx context.Context) (int64, error) {
	var version int64
	if err := s.db.WithContext(ctx).Model(&HistoryModel{}).
		Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
		return 0, err
	}
	return version, nil
}

// ListRoutes 路由列表。
func (s *Store) ListRoutes(ctx context.Context, page, size int, keyword string, status *bool) ([]Route, int64, error) {
	var (
		rows  []*RouteModel
		total int64
	)
	query := s.db.WithContext(ctx).Model(&RouteModel{})
	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("name LIKE ? OR match_expression LIKE ?", like, like)
	}
	if status != nil {
		query = query.Where("enabled = ?", *status)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("priority DESC, id ASC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	routes, err := toRoutes(rows)
	if err != nil {
		return nil, 0, err
	}
	return routes, total, nil
}

// CreateRoute 新建路由（草稿态，不生效）。
func (s *Store) CreateRoute(ctx context.Context, route *Route) (uint64, error) {
	row, err := fromRoute(route)
	if err != nil {
		return 0, err
	}
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

// UpdateRoute 修改路由（草稿态，不生效）。
func (s *Store) UpdateRoute(ctx context.Context, route *Route, id uint64) error {
	row, err := fromRoute(route)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&RouteModel{}).Where("id = ?", id).
		Select("name", "description", "enabled", "priority", "match_expression", "rewrites",
			"upstream", "response", "middlewares", "metadata").
		Updates(row).Error
}

// DeleteRoute 删除路由（草稿态，不生效）。
func (s *Store) DeleteRoute(ctx context.Context, id uint64) error {
	return s.db.WithContext(ctx).Delete(&RouteModel{}, "id = ?", id).Error
}

// LoadAll 加载全部路由（含禁用，发布快照用）。
func (s *Store) LoadAll(ctx context.Context) ([]Route, error) {
	var routeRows []*RouteModel
	if err := s.db.WithContext(ctx).Order("priority DESC, id ASC").Find(&routeRows).Error; err != nil {
		return nil, err
	}
	return toRoutes(routeRows)
}

// Publish 发布：读取当前全部配置 → 生成新版本 → 写发布历史（事务）。
func (s *Store) Publish(ctx context.Context, operator, remark string) (*Snapshot, error) {
	var snap *Snapshot
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var version int64
		if err := tx.Raw("SELECT COALESCE(MAX(version), 0) + 1 FROM gw_route_history").Scan(&version).Error; err != nil {
			return err
		}
		var routeRows []*RouteModel
		if err := tx.Order("priority DESC, id ASC").Find(&routeRows).Error; err != nil {
			return err
		}
		routes, err := toRoutes(routeRows)
		if err != nil {
			return err
		}
		snap = &Snapshot{Version: version, Routes: routes}
		body, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		return tx.Create(&HistoryModel{
			Version:  version,
			Snapshot: string(body),
			Operator: operator,
			Remark:   remark,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// Rollback 回滚：将指定版本的快照全量替换当前配置，并产生新版本。
func (s *Store) Rollback(ctx context.Context, version int64, operator, remark string) (*Snapshot, error) {
	var snap *Snapshot
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var h HistoryModel
		if err := tx.First(&h, "version = ?", version).Error; err != nil {
			return err
		}
		old := &Snapshot{}
		if err := json.Unmarshal([]byte(h.Snapshot), old); err != nil {
			return err
		}
		var newVersion int64
		if err := tx.Raw("SELECT COALESCE(MAX(version), 0) + 1 FROM gw_route_history").Scan(&newVersion).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM gw_route").Error; err != nil {
			return err
		}
		for i := range old.Routes {
			old.Routes[i].Normalize()
			row, err := fromRoute(&old.Routes[i])
			if err != nil {
				return err
			}
			row.ID = old.Routes[i].ID
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		snap = &Snapshot{Version: newVersion, Routes: old.Routes}
		body, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		return tx.Create(&HistoryModel{
			Version:  newVersion,
			Snapshot: string(body),
			Operator: operator,
			Remark:   remark,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// ListHistory 发布历史（分页）。
func (s *Store) ListHistory(ctx context.Context, page, size int) ([]*HistoryModel, int64, error) {
	var (
		rows  []*HistoryModel
		total int64
	)
	if err := s.db.WithContext(ctx).Model(&HistoryModel{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := s.db.WithContext(ctx).Order("version DESC").
		Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// GetHistory 获取指定版本快照。
func (s *Store) GetHistory(ctx context.Context, version int64) (*Snapshot, error) {
	var h HistoryModel
	if err := s.db.WithContext(ctx).First(&h, "version = ?", version).Error; err != nil {
		return nil, err
	}
	snap := &Snapshot{}
	if err := json.Unmarshal([]byte(h.Snapshot), snap); err != nil {
		return nil, err
	}
	for i := range snap.Routes {
		snap.Routes[i].Normalize()
	}
	return snap, nil
}

func toRoutes(rows []*RouteModel) ([]Route, error) {
	out := make([]Route, 0, len(rows))
	for _, row := range rows {
		route, err := row.toRoute()
		if err != nil {
			return nil, err
		}
		out = append(out, route)
	}
	return out, nil
}

func (m *RouteModel) toRoute() (Route, error) {
	route := Route{
		ID:          m.ID,
		Name:        m.Name,
		Description: m.Description,
		Enabled:     m.Enabled,
		Priority:    m.Priority,
		Match:       Match{Expression: m.MatchExpression},
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
	if err := decodeJSONColumn(m.Rewrites, &route.Rewrites); err != nil {
		return Route{}, fmt.Errorf("decode rewrites for route %d: %w", m.ID, err)
	}
	if err := decodeJSONColumn(m.Upstream, &route.Upstream); err != nil {
		return Route{}, fmt.Errorf("decode upstream for route %d: %w", m.ID, err)
	}
	if err := decodeJSONColumn(m.Response, &route.Response); err != nil {
		return Route{}, fmt.Errorf("decode response for route %d: %w", m.ID, err)
	}
	if err := decodeJSONColumn(m.Middlewares, &route.Middlewares); err != nil {
		return Route{}, fmt.Errorf("decode middlewares for route %d: %w", m.ID, err)
	}
	if err := decodeJSONColumn(m.Metadata, &route.Metadata); err != nil {
		return Route{}, fmt.Errorf("decode metadata for route %d: %w", m.ID, err)
	}
	route.Normalize()
	return route, nil
}

func fromRoute(route *Route) (*RouteModel, error) {
	route.Normalize()
	rewrites, err := marshalJSON(route.Rewrites)
	if err != nil {
		return nil, err
	}
	upstream, err := marshalJSON(route.Upstream)
	if err != nil {
		return nil, err
	}
	response, err := marshalJSON(route.Response)
	if err != nil {
		return nil, err
	}
	middlewares, err := marshalJSON(route.Middlewares)
	if err != nil {
		return nil, err
	}
	metadata, err := marshalJSON(route.Metadata)
	if err != nil {
		return nil, err
	}
	return &RouteModel{
		Name:            route.Name,
		Description:     route.Description,
		Enabled:         route.Enabled,
		Priority:        route.Priority,
		MatchExpression: route.Match.Expression,
		Rewrites:        rewrites,
		Upstream:        upstream,
		Response:        response,
		Middlewares:     middlewares,
		Metadata:        metadata,
	}, nil
}

func marshalJSON(v any) (string, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func decodeJSONColumn(raw string, dst any) error {
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), dst)
}
