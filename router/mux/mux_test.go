package mux

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/limes-cloud/gateway/config"
)

func TestHandleExpressionRoutes(t *testing.T) {
	r := NewRouter(http.NotFoundHandler(), http.NotFoundHandler())
	if err := r.Handle(config.Match{Expression: `req_path_in("/health") && req_method_in("GET")`}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		code   int
	}{
		{http.MethodGet, http.StatusNoContent},
		{http.MethodHead, http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, "http://example.com/health", nil)
		r.ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Fatalf("%s status = %d, want %d", tc.method, rec.Code, tc.code)
		}
	}
}

type recordingCloser struct {
	closed bool
	err    error
}

func (c *recordingCloser) Close() error {
	c.closed = true
	return c.err
}

func TestSyncCloseWaitsAndClosesRoutes(t *testing.T) {
	r := NewRouter(http.NotFoundHandler(), http.NotFoundHandler())
	closer := &recordingCloser{}
	if err := r.Handle(config.Match{Expression: `req_path_in("/")`}, http.NotFoundHandler(), closer); err != nil {
		t.Fatal(err)
	}
	if err := r.SyncClose(context.Background()); err != nil {
		t.Fatalf("sync close: %v", err)
	}
	if !closer.closed {
		t.Fatal("route closer should be closed")
	}
}

func TestSyncCloseForcesCloseAfterTimeout(t *testing.T) {
	r := NewRouter(http.NotFoundHandler(), http.NotFoundHandler())
	closer := &recordingCloser{err: io.ErrClosedPipe}
	if err := r.Handle(config.Match{Expression: `req_path_in("/")`}, http.NotFoundHandler(), closer); err != nil {
		t.Fatal(err)
	}
	rr := r.(*muxRouter)
	rr.wg.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.SyncClose(ctx); err != nil {
		t.Fatalf("sync close: %v", err)
	}
	if !closer.closed {
		t.Fatal("route closer should be closed after timeout")
	}
	rr.wg.Done()
}

func TestInspectMuxRouter(t *testing.T) {
	r := NewRouter(http.NotFoundHandler(), http.NotFoundHandler())
	if err := r.Handle(config.Match{Expression: `req_path_in("/health")`}, http.NotFoundHandler(), nil); err != nil {
		t.Fatal(err)
	}
	list := InspectMuxRouter(r)
	if len(list) < 2 {
		t.Fatalf("inspect routes = %#v", list)
	}
	if InspectMuxRouter(http.NewServeMux()) != nil {
		t.Fatal("unexpected inspect result for non-mux router")
	}
}

func TestParseBool(t *testing.T) {
	if !parseBool("true", false) || parseBool("false", true) {
		t.Fatal("valid bool parse failed")
	}
	if !parseBool("nope", true) || parseBool("", true) == false {
		t.Fatal("invalid bool should fall back to default")
	}
}

func TestHandleFallsThroughWhenExpressionDoesNotMatch(t *testing.T) {
	r := NewRouter(http.NotFoundHandler(), http.NotFoundHandler())
	if err := r.Handle(config.Match{Expression: `req_path_in("/a")`}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Handle(config.Match{Expression: `req_path_in("/b")`}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}), nil); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/b", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
}
