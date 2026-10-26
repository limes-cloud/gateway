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

// EndpointModel gw_endpoint 行模型
type EndpointModel struct {
	ID             uint64    `gorm:"column:id;primaryKey"`
	Path           string    `gorm:"column:path"`
	Title          string    `gorm:"column:title"`
	Method         string    `gorm:"column:method"`
	Host           string    `gorm:"column:host"`
	Protocol       string    `gorm:"column:protocol"`
	ResponseFormat bool      `gorm:"column:response_format"`
	TimeoutMs      int64     `gorm:"column:timeout_ms"`
	Description    string    `gorm:"column:description"`
	Metadata       string    `gorm:"column:metadata"` // JSON 字符串，NULL 转空
	Backends       string    `gorm:"column:backends"`
	Middlewares    string    `gorm:"column:middlewares"`
	Retry          string    `gorm:"column:retry"`
	Enabled        bool      `gorm:"column:enabled"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (EndpointModel) TableName() string { return "gw_endpoint" }

// HistoryModel gw_endpoint_history 行模型
type HistoryModel struct {
	ID        uint64    `gorm:"column:id;primaryKey"`
	Version   int64     `gorm:"column:version"`
	Snapshot  string    `gorm:"column:snapshot"`
	Operator  string    `gorm:"column:operator"`
	Remark    string    `gorm:"column:remark"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (HistoryModel) TableName() string { return "gw_endpoint_history" }

// Snapshot 发布快照：某版本全量下发的路由配置（全局 middlewares 为实例级，不进快照）
type Snapshot struct {
	Version   int64      `json:"version"`
	Endpoints []Endpoint `json:"endpoints"`
}

// Store 网关路由配置的数据库访问层（gateway 自有库）
type Store struct {
	db *gorm.DB
}

// NewStore 建立数据库连接（连接失败返回 error，由调用方降级处理）
func NewStore(c *DBConf) (*Store, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.Connect.Username, c.Connect.Password, c.Connect.Host, c.Connect.Port, c.Connect.DBName)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// LoadPublished 加载全部启用路由（启动降级链 / 发布后拉取用）
func (s *Store) LoadPublished(ctx context.Context) ([]Endpoint, int64, error) {
	var rows []*EndpointModel
	if err := s.db.WithContext(ctx).Where("enabled = ?", 1).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	eps := make([]Endpoint, 0, len(rows))
	for _, row := range rows {
		eps = append(eps, row.toEndpoint())
	}
	ver, err := s.LatestVersion(ctx)
	if err != nil {
		return eps, 0, err
	}
	return eps, ver, nil
}

// LatestVersion 获取最新发布版本号（无发布记录返回 0）
func (s *Store) LatestVersion(ctx context.Context) (int64, error) {
	var version int64
	if err := s.db.WithContext(ctx).Model(&HistoryModel{}).
		Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
		return 0, err
	}
	return version, nil
}

// ListEndpoints 路由列表（分页；keyword 模糊匹配 path，status 为 enabled 状态过滤）
func (s *Store) ListEndpoints(ctx context.Context, page, size int, keyword string, status *bool) ([]Endpoint, int64, error) {
	var (
		rows  []*EndpointModel
		total int64
	)
	query := s.db.WithContext(ctx).Model(&EndpointModel{})
	if keyword != "" {
		query = query.Where("path LIKE ?", "%"+keyword+"%")
	}
	if status != nil {
		query = query.Where("enabled = ?", *status)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id ASC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	eps := make([]Endpoint, 0, len(rows))
	for _, row := range rows {
		eps = append(eps, row.toEndpoint())
	}
	return eps, total, nil
}

// CreateEndpoint 新建路由（草稿态，不生效）
func (s *Store) CreateEndpoint(ctx context.Context, e *Endpoint) (uint64, error) {
	row, err := fromEndpoint(e)
	if err != nil {
		return 0, err
	}
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

// UpdateEndpoint 修改路由（草稿态，不生效）
func (s *Store) UpdateEndpoint(ctx context.Context, e *Endpoint, id uint64) error {
	row, err := fromEndpoint(e)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&EndpointModel{}).Where("id = ?", id).
		Select("path", "title", "method", "host", "protocol", "response_format", "timeout_ms",
			"description", "metadata", "backends", "middlewares", "retry", "enabled").
		Updates(row).Error
}

// DeleteEndpoint 删除路由（草稿态，不生效）
func (s *Store) DeleteEndpoint(ctx context.Context, id uint64) error {
	return s.db.WithContext(ctx).Delete(&EndpointModel{}, "id = ?", id).Error
}

// LoadAll 加载全部路由（含禁用，发布快照用）
func (s *Store) LoadAll(ctx context.Context) ([]Endpoint, error) {
	var rows []*EndpointModel
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	eps := make([]Endpoint, 0, len(rows))
	for _, row := range rows {
		eps = append(eps, row.toEndpoint())
	}
	return eps, nil
}

// Publish 发布：读取当前全部路由 → 生成新版本 → 写发布历史（事务）
// 返回生成的快照；唯一键冲突（并发发布）时返回 error，由调用方重试
func (s *Store) Publish(ctx context.Context, operator, remark string) (*Snapshot, error) {
	var snap *Snapshot
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var version int64
		if err := tx.Raw("SELECT COALESCE(MAX(version), 0) + 1 FROM gw_endpoint_history").Scan(&version).Error; err != nil {
			return err
		}
		var rows []*EndpointModel
		if err := tx.Order("id ASC").Find(&rows).Error; err != nil {
			return err
		}
		eps := make([]Endpoint, 0, len(rows))
		for _, row := range rows {
			eps = append(eps, row.toEndpoint())
		}
		snap = &Snapshot{Version: version, Endpoints: eps}
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

// Rollback 回滚：将指定版本的快照全量替换当前路由，并产生新版本（事务）
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
		// 生成新版本（回滚也产生新记录，保留完整审计链）
		var newVersion int64
		if err := tx.Raw("SELECT COALESCE(MAX(version), 0) + 1 FROM gw_endpoint_history").Scan(&newVersion).Error; err != nil {
			return err
		}
		// 全量替换：清空后按快照重建
		if err := tx.Exec("DELETE FROM gw_endpoint").Error; err != nil {
			return err
		}
		for _, ep := range old.Endpoints {
			row, err := fromEndpoint(&ep)
			if err != nil {
				return err
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		snap = &Snapshot{Version: newVersion, Endpoints: old.Endpoints}
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

// ListHistory 发布历史（分页）
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

// GetHistory 获取指定版本快照
func (s *Store) GetHistory(ctx context.Context, version int64) (*Snapshot, error) {
	var h HistoryModel
	if err := s.db.WithContext(ctx).First(&h, "version = ?", version).Error; err != nil {
		return nil, err
	}
	snap := &Snapshot{}
	if err := json.Unmarshal([]byte(h.Snapshot), snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// toEndpoint 行模型 → 领域模型（JSON 列空值兜底）
func (m *EndpointModel) toEndpoint() Endpoint {
	e := Endpoint{
		ID:             m.ID,
		Path:           m.Path,
		Title:          m.Title,
		Method:         m.Method,
		Host:           m.Host,
		Protocol:       m.Protocol,
		ResponseFormat: m.ResponseFormat,
		Timeout:        time.Duration(m.TimeoutMs) * time.Millisecond,
		TimeoutMs:      m.TimeoutMs,
		Description:    m.Description,
		Enabled:        m.Enabled,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
	_ = json.Unmarshal([]byte(empty2obj(m.Metadata)), &e.Metadata)
	_ = json.Unmarshal([]byte(m.Backends), &e.Backends)
	_ = json.Unmarshal([]byte(empty2arr(m.Middlewares)), &e.Middlewares)
	if m.Retry != "" {
		_ = json.Unmarshal([]byte(m.Retry), &e.Retry)
	}
	return e
}

// fromEndpoint 领域模型 → 行模型（JSON 字段序列化）
func fromEndpoint(e *Endpoint) (*EndpointModel, error) {
	backends, err := json.Marshal(e.Backends)
	if err != nil {
		return nil, err
	}
	// JSON 列不接受空字符串，未配置的字段显式写入 null（合法 JSON 文本）
	metadata := []byte("null")
	middlewares := []byte("null")
	retry := []byte("null")
	if metadata, err = json.Marshal(e.Metadata); err != nil {
		return nil, err
	}
	if middlewares, err = json.Marshal(e.Middlewares); err != nil {
		return nil, err
	}
	if e.Retry != nil {
		if retry, err = json.Marshal(e.Retry); err != nil {
			return nil, err
		}
	}
	return &EndpointModel{
		Path:           e.Path,
		Title:          e.Title,
		Method:         e.Method,
		Host:           e.Host,
		Protocol:       e.Protocol,
		ResponseFormat: e.ResponseFormat,
		TimeoutMs:      e.TimeoutMs,
		Description:    e.Description,
		Metadata:       string(metadata),
		Backends:       string(backends),
		Middlewares:    string(middlewares),
		Retry:          string(retry),
		Enabled:        e.Enabled,
	}, nil
}

func empty2obj(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

func empty2arr(s string) string {
	if s == "" {
		return "[]"
	}
	return s
}
