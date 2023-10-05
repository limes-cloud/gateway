package main

import (
	"fmt"
	"github.com/limes-cloud/gateway/client"
	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/discovery"
	"github.com/limes-cloud/gateway/middleware"
	"github.com/limes-cloud/gateway/middleware/circuitbreaker"
	"github.com/limes-cloud/gateway/proxy"
	"github.com/limes-cloud/gateway/proxy/debug"
	"github.com/limes-cloud/gateway/server"
	"github.com/limes-cloud/kratos/log"
	"github.com/limes-cloud/kratos/registry"
	"github.com/limes-cloud/kratos/transport"
	"net/http"
)

func NewServer(conf *config.Config) (transport.Server, error) {
	clientFactory := client.NewFactory(makeDiscovery(conf.Discovery))

	pxy, err := proxy.New(clientFactory, middleware.Create)
	if err != nil {
		return nil, fmt.Errorf("failed to new proxy: %v", err)
	}

	circuitbreaker.Init(clientFactory)

	if err = pxy.Update(conf); err != nil {
		return nil, fmt.Errorf("failed to update service conf: %v", err)
	}
	// 监听配置变化
	conf.Watch("endpoints", func(c *config.Config) {
		if er := pxy.Update(c); er != nil {
			log.Errorf("failed to update service config: %v", err)
		}
	})

	handler := http.Handler(pxy)
	if conf.Debug {
		debug.Register("proxy", pxy)
		handler = debug.MashupWithDebugHandler(pxy)
	}

	return server.NewProxy(handler, conf.Addr), nil
}

func makeDiscovery(dsn string) registry.Discovery {
	if dsn == "" {
		return nil
	}
	d, err := discovery.Create(dsn)
	if err != nil {
		log.Fatalf("failed to create discovery: %v", err)
	}
	return d
}
