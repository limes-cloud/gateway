package client

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/limes-cloud/gateway/middleware"
	"github.com/limes-cloud/kratos/selector"
)

type client struct {
	applier  *nodeApplier
	selector selector.Selector
}

type Client interface {
	http.RoundTripper
	io.Closer
}

func newClient(applier *nodeApplier, selector selector.Selector) *client {
	return &client{
		applier:  applier,
		selector: selector,
	}
}

func (c *client) Close() error {
	c.applier.Cancel()
	return nil
}

func (c *client) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	reqOpt, _ := middleware.FromRequestContext(ctx)
	filter, _ := middleware.SelectorFiltersFromContext(ctx)
	n, done, err := c.selector.Select(ctx, selector.WithNodeFilter(filter...))
	if err != nil {
		return nil, err
	}
	reqOpt.CurrentNode = n

	addr := n.Address()
	reqOpt.Backends = append(reqOpt.Backends, addr)
	req.URL.Host = addr
	req.URL.Scheme = "http"
	req.RequestURI = ""
	startAt := time.Now()
	resp, err := n.(*node).client.Do(req)
	reqOpt.UpstreamResponseTime = append(reqOpt.UpstreamResponseTime, time.Since(startAt).Seconds())
	if err != nil {
		done(ctx, selector.DoneInfo{Err: err})
		reqOpt.UpstreamStatusCode = append(reqOpt.UpstreamStatusCode, 0)
		return nil, err
	}
	if reqOpt.Endpoint.ResponseFormat {
		body, length := ResponseFormat(req.Context(), resp.Body)
		if length != 0 {
			resp.Body = body
			resp.Header.Set("Content-Length", fmt.Sprint(length))
		}
	}

	reqOpt.UpstreamStatusCode = append(reqOpt.UpstreamStatusCode, resp.StatusCode)
	reqOpt.DoneFunc = done
	return resp, nil
}
