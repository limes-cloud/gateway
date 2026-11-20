package router

import (
	"context"
	"io"
	"net/http"

	"github.com/limes-cloud/gateway/config"
)

// Router is a gateway router.
type Router interface {
	http.Handler
	Handle(match config.Match, handler http.Handler, closer io.Closer) error
	SyncClose(ctx context.Context) error
}
