package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
)

const (
	// snapshotKey Redis 共享快照键（发布时写入，多实例一致）
	snapshotKey = "gw:config:snapshot"
	// changedKey Redis 发布通知通道（仅版本号，不含内容；通知只是触发器）
	changedKey = "gw:config:changed"
	// reconcileInterval 周期对账间隔（防丢通知/组件短暂故障）
	reconcileInterval = 60 * time.Second
)

// Loader 配置加载器：启动引导 + 运行时更新
type Loader interface {
	// Load 启动时加载：按降级链返回一份配置（任何情况都不应失败，除非连文件都没有）
	Load(ctx context.Context) (*Config, error)
	// Watch 注册变更回调（内部：PubSub 订阅 + 周期对账），回调在独立 goroutine 中触发
	Watch(ctx context.Context, fn func(*Config))
	// Store 返回底层数据库访问（未配置数据库时返回 nil，管理 API 据此降级 503）
	Store() *Store
	// Notify 发布/回滚后调用：写共享快照 + 通知所有实例回源拉取
	Notify(ctx context.Context, snap *Snapshot) error
	// Close 释放资源
	Close() error
}

type loader struct {
	boot  *Config
	store *Store
	rdb   *redis.Client

	mu             sync.Mutex
	currentVersion int64
}

// NewLoader 组装降级链：file(保底) → redisSnapshot(次新) → db(最新)
// 任何后端初始化失败仅降级，不阻断启动
func NewLoader(boot *Config) Loader {
	l := &loader{boot: boot}
	for _, c := range boot.Database {
		if c.Enable {
			if s, err := NewStore(c); err != nil {
				log.Warnw("msg", "init db store error, degrade to file", "err", err.Error())
			} else {
				l.store = s
			}
			break
		}
	}
	for _, c := range boot.Redis {
		if c.Enable {
			l.rdb = redis.NewClient(&redis.Options{Addr: c.Host, DB: c.DB})
			if err := l.rdb.Ping(context.Background()).Err(); err != nil {
				log.Warnw("msg", "redis ping error, degrade to file", "err", err.Error())
			}
			break
		}
	}
	return l
}

// Load 启动加载降级链：① 文件保底 → ② Redis 快照（次新）→ ③ DB（最新，成功则回写快照）
func (l *loader) Load(ctx context.Context) (*Config, error) {
	cfg := l.build(l.boot.Endpoints, 0)

	// ② Redis 共享快照（多实例一致，比文件新）
	if l.rdb != nil {
		if snap, err := l.loadSnapshot(ctx); err == nil && len(snap.Endpoints) > 0 {
			cfg = l.build(snap.Endpoints, snap.Version)
		}
	}

	// ③ DB（事实源）
	if l.store != nil {
		if eps, ver, err := l.store.LoadPublished(ctx); err == nil && len(eps) > 0 {
			cfg = l.build(eps, ver)
			_ = l.saveSnapshot(ctx, &Snapshot{Version: ver, Endpoints: eps})
		} else if err != nil {
			log.Warnw("msg", "load endpoints from db error, keep previous", "err", err.Error())
		}
	}
	return cfg, nil
}

// Watch 注册变更回调：PubSub 订阅（触发器）+ 周期对账（兜底）
func (l *loader) Watch(ctx context.Context, fn func(*Config)) {
	if l.rdb != nil {
		go l.watchChannel(ctx, fn)
	}
	go l.reconcile(ctx, fn)
}

// watchChannel 订阅发布通知：收到后回源拉取（DB → 快照），拉取成功才回调
func (l *loader) watchChannel(ctx context.Context, fn func(*Config)) {
	sub := l.rdb.Subscribe(ctx, changedKey)
	defer sub.Close()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if cfg, err := l.fetch(ctx); err == nil && cfg != nil {
				l.applyAndNotify(ctx, cfg, fn)
				log.Infow("msg", "gateway config updated", "version", cfg.VersionID, "notify", msg.Payload)
			}
		}
	}
}

// reconcile 周期对账：版本与最新发布不一致时拉取（防丢通知、防组件短暂故障）
func (l *loader) reconcile(ctx context.Context, fn func(*Config)) {
	if l.store == nil {
		return
	}
	t := time.NewTicker(reconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ver, err := l.store.LatestVersion(ctx)
			if err != nil || ver == l.version() {
				continue
			}
			if cfg, err := l.fetch(ctx); err == nil && cfg != nil {
				l.applyAndNotify(ctx, cfg, fn)
				log.Infow("msg", "gateway config reconciled", "version", cfg.VersionID)
			}
		}
	}
}

// fetch 回源拉取：DB（最新）→ Redis 快照（次新），都不可用返回错误（保持旧配置）
func (l *loader) fetch(ctx context.Context) (*Config, error) {
	if l.store != nil {
		if eps, ver, err := l.store.LoadPublished(ctx); err == nil && len(eps) > 0 {
			return l.build(eps, ver), nil
		}
	}
	if l.rdb != nil {
		if snap, err := l.loadSnapshot(ctx); err == nil && len(snap.Endpoints) > 0 {
			return l.build(snap.Endpoints, snap.Version), nil
		}
	}
	return nil, ErrSourceUnavailable
}

// build 组装配置：实例级字段沿用启动引导，路由级由调用方提供
func (l *loader) build(eps []Endpoint, version int64) *Config {
	return &Config{
		Debug:         l.boot.Debug,
		Addr:          l.boot.Addr,
		Name:          l.boot.Name,
		Version:       l.boot.Version,
		Discovery:     l.boot.Discovery,
		ManagerAddr:   l.boot.ManagerAddr,
		ManagerSignAk: l.boot.ManagerSignAk,
		ManagerSignSk: l.boot.ManagerSignSk,
		AdminAuthURL:  l.boot.AdminAuthURL,
		Database:      l.boot.Database,
		Redis:         l.boot.Redis,
		Endpoints:     eps,
		Middlewares:   l.boot.Middlewares,
		VersionID:     version,
	}
}

func (l *loader) applyAndNotify(ctx context.Context, cfg *Config, fn func(*Config)) {
	l.setVersion(cfg.VersionID)
	// 拉取到最新后回写快照，保证其他实例/重启后能拿到
	if l.rdb != nil && l.store != nil {
		_ = l.saveSnapshot(ctx, &Snapshot{Version: cfg.VersionID, Endpoints: cfg.Endpoints})
	}
	fn(cfg)
}

func (l *loader) loadSnapshot(ctx context.Context) (*Snapshot, error) {
	body, err := l.rdb.Get(ctx, snapshotKey).Bytes()
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{}
	if err := json.Unmarshal(body, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

func (l *loader) saveSnapshot(ctx context.Context, snap *Snapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return l.rdb.Set(ctx, snapshotKey, body, 0).Err()
}

func (l *loader) version() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.currentVersion
}

func (l *loader) setVersion(v int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.currentVersion = v
}

// Store 返回底层数据库访问（未配置时 nil）
func (l *loader) Store() *Store {
	return l.store
}

// Notify 发布/回滚后调用：写共享快照 + 通知所有实例（通知只含版本号，实例收到后回源拉取）
func (l *loader) Notify(ctx context.Context, snap *Snapshot) error {
	if l.rdb == nil {
		return nil
	}
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if err := l.rdb.Set(ctx, snapshotKey, body, 0).Err(); err != nil {
		return err
	}
	return l.rdb.Publish(ctx, changedKey, fmt.Sprint(snap.Version)).Err()
}

// Close 释放连接
func (l *loader) Close() error {
	if l.rdb != nil {
		return l.rdb.Close()
	}
	return nil
}

// ErrSourceUnavailable 所有配置源均不可用
var ErrSourceUnavailable = errors.New("all config sources unavailable")
