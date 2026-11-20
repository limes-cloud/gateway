package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/limes-cloud/gateway/config"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testLoader struct {
	store    *config.Store
	notified []*config.Snapshot
}

func (l *testLoader) Load(context.Context) (*config.Config, error) { return &config.Config{}, nil }
func (l *testLoader) Watch(context.Context, func(*config.Config))  {}
func (l *testLoader) Store() *config.Store                         { return l.store }
func (l *testLoader) Notify(_ context.Context, snap *config.Snapshot) error {
	l.notified = append(l.notified, snap)
	return nil
}
func (l *testLoader) Close() error { return nil }

func newAdminTestHandler(t *testing.T) (*Handler, *testLoader) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&config.RouteModel{}, &config.HistoryModel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	loader := &testLoader{store: config.NewStoreFromDB(db)}
	return New(loader, &Options{}), loader
}

type apiResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Reason  string          `json:"reason"`
	Data    json.RawMessage `json:"data"`
}

func doRequest(t *testing.T, h http.Handler, method, target string, body any) (*httptest.ResponseRecorder, apiResponse) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else if raw, ok := body.(string); ok {
		reader = bytes.NewReader([]byte(raw))
	} else {
		content, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(content)
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp apiResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	}
	return rec, resp
}

func validRouteBody(name string) map[string]any {
	return map[string]any{
		"name":     name,
		"priority": 100,
		"match":    map[string]any{"expression": `req_path_prefix_in("/manager/", false)`},
		"rewrites": []any{
			map[string]any{
				"name":     "spider seo",
				"priority": 10,
				"match":    map[string]any{"expression": `req_header_value_contain("User-Agent", "spider", true)`, "terminal": true},
				"actions": []any{
					map[string]any{"type": "PATH_SET", "params": map[string]any{"value": "/manager/seo"}},
				},
			},
		},
		"upstream": map[string]any{
			"protocol": "HTTP",
			"backends": []any{map[string]any{"target": "127.0.0.1:7202"}},
		},
	}
}

func TestAdminHealthAndUnavailableWithoutDatabase(t *testing.T) {
	h := New(&testLoader{}, &Options{})

	rec, _ := doRequest(t, h, http.MethodGet, "/gateway/api/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}

	rec, resp := doRequest(t, h, http.MethodGet, "/gateway/api/routes", nil)
	if rec.Code != http.StatusServiceUnavailable || resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("routes without db status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminCORSPreflightAndMashup(t *testing.T) {
	h := New(&testLoader{}, &Options{})
	called := false
	origin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})
	handler := h.Mashup(origin)

	req := httptest.NewRequest(http.MethodOptions, "http://gateway.local/gateway/api/routes", nil)
	req.Header.Set("Origin", "http://admin.local")
	req.Header.Set("Access-Control-Request-Headers", "Authorization,Content-Type")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://admin.local" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Authorization,Content-Type" {
		t.Fatalf("allow headers = %q", got)
	}
	if called {
		t.Fatal("preflight should not reach origin handler")
	}

	req = httptest.NewRequest(http.MethodGet, "http://gateway.local/other", nil)
	req.Header.Set("Origin", "http://admin.local")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot || !called {
		t.Fatalf("non-admin path status=%d called=%v", rec.Code, called)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("non-admin path should not receive admin CORS headers")
	}
}

func TestAdminRouteCRUD(t *testing.T) {
	h, _ := newAdminTestHandler(t)

	rec, resp := doRequest(t, h, http.MethodPost, "/gateway/api/routes", validRouteBody("manager"))
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID uint64 `json:"id"`
	}
	if err := json.Unmarshal(resp.Data, &created); err != nil || created.ID == 0 {
		t.Fatalf("create data=%s err=%v", resp.Data, err)
	}

	rec, resp = doRequest(t, h, http.MethodGet, "/gateway/api/routes?page=1&size=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d", rec.Code)
	}
	var listed struct {
		Total int64            `json:"total"`
		List  []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(resp.Data, &listed); err != nil {
		t.Fatalf("list data: %v", err)
	}
	if listed.Total != 1 || len(listed.List) != 1 {
		t.Fatalf("list = %#v", listed)
	}
	if listed.List[0]["enabled"] != true {
		t.Fatalf("enabled should default to true: %#v", listed.List[0])
	}
	rewrites := listed.List[0]["rewrites"].([]any)
	if len(rewrites) != 1 {
		t.Fatalf("rewrites = %#v", rewrites)
	}

	update := validRouteBody("manager-v2")
	update["priority"] = 300
	rec, _ = doRequest(t, h, http.MethodPut, "/gateway/api/routes?id="+uintToString(created.ID), update)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec, resp = doRequest(t, h, http.MethodGet, "/gateway/api/routes?keyword=manager-v2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list after update status=%d", rec.Code)
	}
	_ = json.Unmarshal(resp.Data, &listed)
	if listed.Total != 1 || listed.List[0]["name"] != "manager-v2" || listed.List[0]["priority"].(float64) != 300 {
		t.Fatalf("updated list = %#v", listed)
	}

	rec, _ = doRequest(t, h, http.MethodDelete, "/gateway/api/routes?id="+uintToString(created.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status=%d", rec.Code)
	}
	rec, resp = doRequest(t, h, http.MethodGet, "/gateway/api/routes", nil)
	_ = json.Unmarshal(resp.Data, &listed)
	if listed.Total != 0 {
		t.Fatalf("routes after delete = %#v", listed)
	}
}

func TestAdminRejectsInvalidRequests(t *testing.T) {
	h, _ := newAdminTestHandler(t)

	for _, tc := range []struct {
		name   string
		method string
		target string
		body   any
	}{
		{"invalid body", http.MethodPost, "/gateway/api/routes", "{"},
		{"invalid route", http.MethodPost, "/gateway/api/routes", map[string]any{"name": "bad"}},
		{"invalid update id", http.MethodPut, "/gateway/api/routes?id=abc", validRouteBody("x")},
		{"missing update id", http.MethodPut, "/gateway/api/routes", validRouteBody("x")},
		{"invalid delete id", http.MethodDelete, "/gateway/api/routes?id=0", nil},
	} {
		rec, _ := doRequest(t, h, tc.method, tc.target, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

func TestAdminPublishHistoryAndRollback(t *testing.T) {
	h, loader := newAdminTestHandler(t)

	rec, _ := doRequest(t, h, http.MethodPost, "/gateway/api/routes", validRouteBody("manager"))
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d", rec.Code)
	}
	rec, resp := doRequest(t, h, http.MethodPost, "/gateway/api/publish", map[string]any{"remark": "v1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("publish v1 status=%d body=%s", rec.Code, rec.Body.String())
	}
	var published struct {
		Version int64 `json:"version"`
	}
	_ = json.Unmarshal(resp.Data, &published)
	if published.Version != 1 || len(loader.notified) != 1 {
		t.Fatalf("published=%#v notified=%d", published, len(loader.notified))
	}

	rec, resp = doRequest(t, h, http.MethodGet, "/gateway/api/routes", nil)
	var listed struct {
		List []map[string]any `json:"list"`
	}
	_ = json.Unmarshal(resp.Data, &listed)
	id := uint64(listed.List[0]["id"].(float64))

	update := validRouteBody("manager-v2")
	rec, _ = doRequest(t, h, http.MethodPut, "/gateway/api/routes?id="+uintToString(id), update)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d", rec.Code)
	}
	rec, resp = doRequest(t, h, http.MethodPost, "/gateway/api/publish", map[string]any{"remark": "v2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("publish v2 status=%d", rec.Code)
	}
	_ = json.Unmarshal(resp.Data, &published)
	if published.Version != 2 {
		t.Fatalf("v2 version = %d", published.Version)
	}

	rec, resp = doRequest(t, h, http.MethodGet, "/gateway/api/publish/history?page=1&size=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("history status=%d", rec.Code)
	}
	var history struct {
		Total int64 `json:"total"`
		List  []struct {
			Version int64 `json:"version"`
		} `json:"list"`
	}
	_ = json.Unmarshal(resp.Data, &history)
	if history.Total != 2 || len(history.List) != 2 || history.List[0].Version != 2 {
		t.Fatalf("history = %#v", history)
	}

	rec, resp = doRequest(t, h, http.MethodPost, "/gateway/api/publish/rollback", map[string]any{"version": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(resp.Data, &published)
	if published.Version != 3 || len(loader.notified) != 3 {
		t.Fatalf("rollback published=%#v notified=%d", published, len(loader.notified))
	}
	rec, resp = doRequest(t, h, http.MethodGet, "/gateway/api/routes?keyword=manager", nil)
	_ = json.Unmarshal(resp.Data, &listed)
	if len(listed.List) != 1 || listed.List[0]["name"] != "manager" {
		t.Fatalf("routes after rollback = %#v", listed.List)
	}

	for _, body := range []any{
		map[string]any{"version": 0},
		map[string]any{"version": 99},
		"{",
	} {
		rec, _ := doRequest(t, h, http.MethodPost, "/gateway/api/publish/rollback", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("rollback bad body %v status=%d", body, rec.Code)
		}
	}
}

func TestAdminPublishRejectsInvalidRoute(t *testing.T) {
	h, loader := newAdminTestHandler(t)
	// 直接写一条只有名称的坏路由，绕过创建接口校验
	store := loader.store
	if _, err := store.CreateRoute(context.Background(), &config.Route{
		Name:  "broken",
		Match: config.Match{Expression: `req_path_in("/")`},
	}); err != nil {
		t.Fatalf("create broken route: %v", err)
	}
	rec, _ := doRequest(t, h, http.MethodPost, "/gateway/api/publish", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("publish invalid status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(loader.notified) != 0 {
		t.Fatal("invalid publish should not notify")
	}
}

func uintToString(v uint64) string { return strconv.FormatUint(v, 10) }
