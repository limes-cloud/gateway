package config

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&RouteModel{}, &HistoryModel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewStoreFromDB(db)
}

func testRoute(name string, priority int, upstream string) Route {
	return Route{
		Name:     name,
		Enabled:  true,
		Priority: priority,
		Match:    Match{Expression: `req_path_prefix_in("/` + name + `/", false)`},
		Upstream: Upstream{Protocol: "HTTP", Backends: []Backend{{Target: upstream}}},
	}
}

func TestRouteWithConditionalRewritesRoundTrip(t *testing.T) {
	route := Route{
		ID:       7,
		Name:     "manager",
		Enabled:  true,
		Priority: 100,
		Match:    Match{Expression: `req_path_prefix_in("/manager/", false)`},
		Rewrites: []RewriteRule{
			{
				Name:     "spider seo",
				Priority: 100,
				Match: Match{
					Expression: `req_header_value_contain("User-Agent", "spider", true)`,
					Terminal:   true,
				},
				Actions: []Action{
					{Type: ActionPathSet, Params: map[string]any{"value": "/manager/seo"}},
					{
						Type: ActionRedirect,
						Params: map[string]any{
							"statusCode":    float64(302),
							"location":      "/login",
							"preserveQuery": false,
						},
					},
				},
			},
		},
		Upstream: Upstream{
			Protocol: "HTTP",
			Backends: []Backend{{Target: "127.0.0.1:7202"}},
		},
	}
	row, err := fromRoute(&route)
	if err != nil {
		t.Fatal(err)
	}
	got, err := row.toRoute()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rewrites) != 1 || !got.Rewrites[0].Match.Terminal {
		t.Fatalf("rewrites not round-tripped: %#v", got.Rewrites)
	}
	if len(got.Rewrites[0].Actions) != 2 {
		t.Fatalf("actions not round-tripped: %#v", got.Rewrites[0].Actions)
	}
}

func TestStoreCRUDAndRewriteUpdate(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	route := testRoute("manager", 100, "127.0.0.1:7202")
	route.Rewrites = []RewriteRule{{
		Name:     "spider seo",
		Priority: 10,
		Match:    Match{Expression: `req_header_value_contain("User-Agent", "spider", true)`, Terminal: true},
		Actions: []Action{
			{Type: ActionPathSet, Params: map[string]any{"value": "/manager/seo"}},
		},
	}}
	id, err := store.CreateRoute(ctx, &route)
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	if id == 0 {
		t.Fatal("create route returned zero id")
	}

	list, total, err := store.ListRoutes(ctx, 1, 20, "manager", nil)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list routes = %#v, total=%d, err=%v", list, total, err)
	}
	if len(list[0].Rewrites) != 1 || list[0].Rewrites[0].Actions[0].Type != ActionPathSet {
		t.Fatalf("created rewrites = %#v", list[0].Rewrites)
	}

	// 更新时 rewrites 必须整体覆盖，而不是保留旧规则
	route.Name = "manager-v2"
	route.Priority = 200
	route.Rewrites = []RewriteRule{{
		Name:  "legacy redirect",
		Match: Match{Expression: `req_path_in("/legacy")`, Terminal: true},
		Actions: []Action{
			{Type: ActionRedirect, Params: map[string]any{"statusCode": 302, "location": "/new"}},
		},
	}}
	if err := store.UpdateRoute(ctx, &route, id); err != nil {
		t.Fatalf("update route: %v", err)
	}
	list, total, err = store.ListRoutes(ctx, 1, 20, "", nil)
	if err != nil || total != 1 {
		t.Fatalf("list after update: total=%d err=%v", total, err)
	}
	got := list[0]
	if got.Name != "manager-v2" || got.Priority != 200 {
		t.Fatalf("route fields not updated: %#v", got)
	}
	if len(got.Rewrites) != 1 || got.Rewrites[0].Name != "legacy redirect" {
		t.Fatalf("rewrites not replaced: %#v", got.Rewrites)
	}

	if err := store.DeleteRoute(ctx, id); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	_, total, err = store.ListRoutes(ctx, 1, 20, "", nil)
	if err != nil || total != 0 {
		t.Fatalf("routes after delete: total=%d err=%v", total, err)
	}
}

func TestStoreListRoutesFilteringAndPaging(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	high := testRoute("api", 300, "127.0.0.1:8081")
	low := testRoute("web", 100, "127.0.0.1:8082")
	disabled := testRoute("legacy", 200, "127.0.0.1:8083")
	disabled.Enabled = false
	for _, route := range []*Route{&high, &low, &disabled} {
		if _, err := store.CreateRoute(ctx, route); err != nil {
			t.Fatalf("create %s: %v", route.Name, err)
		}
	}

	list, total, err := store.ListRoutes(ctx, 1, 20, "", nil)
	if err != nil || total != 3 || len(list) != 3 {
		t.Fatalf("all routes total=%d len=%d err=%v", total, len(list), err)
	}
	if list[0].Name != "api" || list[1].Name != "legacy" || list[2].Name != "web" {
		t.Fatalf("priority order = %v", []string{list[0].Name, list[1].Name, list[2].Name})
	}

	enabled := true
	list, total, err = store.ListRoutes(ctx, 1, 20, "", &enabled)
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("enabled routes total=%d len=%d err=%v", total, len(list), err)
	}

	list, total, err = store.ListRoutes(ctx, 1, 20, "leg", nil)
	if err != nil || total != 1 || list[0].Name != "legacy" {
		t.Fatalf("keyword routes = %#v total=%d err=%v", list, total, err)
	}

	list, total, err = store.ListRoutes(ctx, 2, 1, "", nil)
	if err != nil || total != 3 || len(list) != 1 || list[0].Name != "legacy" {
		t.Fatalf("paged routes = %#v total=%d err=%v", list, total, err)
	}

	published, version, err := store.LoadPublished(ctx)
	if err != nil || version != 0 || len(published) != 2 {
		t.Fatalf("published routes len=%d version=%d err=%v", len(published), version, err)
	}
	if published[0].Name != "api" || published[1].Name != "web" {
		t.Fatalf("published order = %v", []string{published[0].Name, published[1].Name})
	}

	all, err := store.LoadAll(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("load all len=%d err=%v", len(all), err)
	}
}

func TestStorePublishHistoryAndRollback(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	route := testRoute("manager", 100, "127.0.0.1:7202")
	id, err := store.CreateRoute(ctx, &route)
	if err != nil {
		t.Fatalf("create route: %v", err)
	}

	snap, err := store.Publish(ctx, "tester", "first")
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	if snap.Version != 1 || len(snap.Routes) != 1 || snap.Routes[0].ID != id {
		t.Fatalf("snapshot v1 = %#v", snap)
	}

	// 修改后发布 v2
	route.Name = "manager-v2"
	route.Rewrites = []RewriteRule{{
		Name:    "seo",
		Match:   Match{Expression: `req_path_in("/")`, Terminal: true},
		Actions: []Action{{Type: ActionPathSet, Params: map[string]any{"value": "/manager/seo"}}},
	}}
	if err := store.UpdateRoute(ctx, &route, id); err != nil {
		t.Fatalf("update route: %v", err)
	}
	snap, err = store.Publish(ctx, "tester", "second")
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	if snap.Version != 2 || snap.Routes[0].Name != "manager-v2" {
		t.Fatalf("snapshot v2 = %#v", snap)
	}

	history, total, err := store.ListHistory(ctx, 1, 20)
	if err != nil || total != 2 || len(history) != 2 {
		t.Fatalf("history total=%d len=%d err=%v", total, len(history), err)
	}
	if history[0].Version != 2 || history[0].Operator != "tester" {
		t.Fatalf("latest history = %#v", history[0])
	}

	old, err := store.GetHistory(ctx, 1)
	if err != nil {
		t.Fatalf("get history v1: %v", err)
	}
	if old.Version != 1 || old.Routes[0].Name != "manager" || len(old.Routes[0].Rewrites) != 0 {
		t.Fatalf("history v1 = %#v", old)
	}

	// 回滚 v1 应生成 v3，并全量替换当前路由
	rolled, err := store.Rollback(ctx, 1, "tester", "rollback")
	if err != nil {
		t.Fatalf("rollback v1: %v", err)
	}
	if rolled.Version != 3 || rolled.Routes[0].Name != "manager" {
		t.Fatalf("rollback snapshot = %#v", rolled)
	}
	published, version, err := store.LoadPublished(ctx)
	if err != nil || version != 3 || len(published) != 1 {
		t.Fatalf("published after rollback len=%d version=%d err=%v", len(published), version, err)
	}
	if published[0].Name != "manager" || len(published[0].Rewrites) != 0 {
		t.Fatalf("published route after rollback = %#v", published[0])
	}
}

func TestStorePublishEmptyRoutesKeepsVersion(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	snap, err := store.Publish(ctx, "tester", "empty")
	if err != nil {
		t.Fatalf("publish empty: %v", err)
	}
	if snap.Version != 1 || len(snap.Routes) != 0 {
		t.Fatalf("empty snapshot = %#v", snap)
	}
	routes, version, err := store.LoadPublished(ctx)
	if err != nil || version != 1 || len(routes) != 0 {
		t.Fatalf("empty published len=%d version=%d err=%v", len(routes), version, err)
	}
	if !hasConfig(routes, version) {
		t.Fatal("published empty snapshot should still be treated as valid config version")
	}
}

func TestStoreRejectsInvalidJSONColumn(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	row := &RouteModel{
		Name:            "broken",
		Enabled:         true,
		MatchExpression: `req_path_in("/")`,
		Rewrites:        "{",
		Upstream:        "{}",
		Response:        "{}",
		Middlewares:     "[]",
		Metadata:        "{}",
	}
	if err := store.db.WithContext(ctx).Create(row).Error; err != nil {
		t.Fatalf("create broken row: %v", err)
	}
	if _, _, err := store.LoadPublished(ctx); err == nil {
		t.Fatal("expected invalid rewrites json to be rejected")
	}
	if _, _, err := store.ListRoutes(ctx, 1, 20, "", nil); err == nil {
		t.Fatal("expected invalid rewrites json to be rejected in list")
	}
	if _, err := store.Publish(ctx, "tester", ""); err == nil {
		t.Fatal("expected publish to reject invalid rewrites json")
	}
}
