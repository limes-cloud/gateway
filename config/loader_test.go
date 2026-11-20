package config

import (
	"context"
	"errors"
	"testing"
)

func TestSaveSnapshotWithoutRedis(t *testing.T) {
	l := &loader{}
	if err := l.saveSnapshot(context.Background(), &Snapshot{}); err != nil {
		t.Fatalf("saveSnapshot without redis returned error: %v", err)
	}
}

func TestHasConfig(t *testing.T) {
	if hasConfig(nil, 0) {
		t.Fatal("empty routes without version should be invalid")
	}
	if !hasConfig(nil, 1) {
		t.Fatal("published empty snapshot should be valid")
	}
	if !hasConfig([]Route{{Name: "file"}}, 0) {
		t.Fatal("file routes should be valid")
	}
}

func TestLoaderLoadUsesPublishedDatabaseRoutes(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	boot := &Config{
		Name:          "gateway",
		Addr:          ":9000",
		ManagerAddr:   "manager:7080",
		ManagerSignAk: "ak",
		Version:       "boot-version",
		Routes:        []Route{{Name: "file-route"}},
	}
	l := &loader{boot: boot, store: store}

	route := testRoute("db-route", 100, "127.0.0.1:7202")
	if _, err := store.CreateRoute(ctx, &route); err != nil {
		t.Fatalf("create route: %v", err)
	}
	cfg, err := l.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Name != "db-route" || cfg.VersionID != 0 {
		t.Fatalf("db routes not loaded: %#v", cfg)
	}
	if cfg.Name != "gateway" || cfg.ManagerSignAk != "ak" || cfg.Version != "boot-version" {
		t.Fatalf("instance fields not preserved: %#v", cfg)
	}

	if _, err := store.Publish(ctx, "tester", "empty check"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	cfg, err = l.Load(ctx)
	if err != nil {
		t.Fatalf("load published: %v", err)
	}
	if cfg.VersionID != 1 || len(cfg.Routes) != 1 || cfg.Routes[0].Name != "db-route" {
		t.Fatalf("published version not loaded: %#v", cfg)
	}
}

func TestLoaderLoadFallsBackToBootFile(t *testing.T) {
	l := &loader{boot: &Config{
		Name:   "gateway",
		Routes: []Route{{Name: "file-route"}},
	}}
	cfg, err := l.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Name != "file-route" || cfg.VersionID != 0 {
		t.Fatalf("file fallback = %#v", cfg)
	}
}

func TestLoaderPublishedEmptySnapshotOverridesBootFile(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if _, err := store.Publish(ctx, "tester", "empty"); err != nil {
		t.Fatalf("publish empty: %v", err)
	}
	l := &loader{
		boot:  &Config{Routes: []Route{{Name: "stale-file-route"}}},
		store: store,
	}
	cfg, err := l.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.VersionID != 1 || len(cfg.Routes) != 0 {
		t.Fatalf("published empty snapshot should override file: %#v", cfg)
	}
}

func TestLoaderFetchWithoutSources(t *testing.T) {
	l := &loader{boot: &Config{}}
	if _, err := l.fetch(context.Background()); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("fetch err = %v", err)
	}
}

func TestLoaderVersionRoundTrip(t *testing.T) {
	l := &loader{}
	if l.version() != 0 {
		t.Fatalf("initial version = %d", l.version())
	}
	l.setVersion(7)
	if l.version() != 7 {
		t.Fatalf("version = %d", l.version())
	}
}
