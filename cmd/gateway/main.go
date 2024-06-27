package main

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-kratos/kratos/v2/transport"
	configure "github.com/limes-cloud/configure/api/configure/client"
	_ "go.uber.org/automaxprocs"

	"github.com/limes-cloud/gateway/client"
	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/discovery"
	_ "github.com/limes-cloud/gateway/discovery/consul"
	"github.com/limes-cloud/gateway/middleware"
	_ "github.com/limes-cloud/gateway/middleware/auth"
	_ "github.com/limes-cloud/gateway/middleware/bbr"
	"github.com/limes-cloud/gateway/middleware/circuitbreaker"
	_ "github.com/limes-cloud/gateway/middleware/cors"
	_ "github.com/limes-cloud/gateway/middleware/logging"
	_ "github.com/limes-cloud/gateway/middleware/rewrite"
	_ "github.com/limes-cloud/gateway/middleware/tracing"
	_ "github.com/limes-cloud/gateway/middleware/transcoder"
	"github.com/limes-cloud/gateway/proxy"
	"github.com/limes-cloud/gateway/proxy/debug"
	"github.com/limes-cloud/gateway/server"
)

func main() {
	conf, err := config.New(configure.NewFromEnv())
	if err != nil {
		log.Fatal(err.Error())
	}

	srv, err := NewServer(conf)
	if err != nil {
		log.Fatal(err.Error())
	}

	app := kratos.New(
		kratos.Server(srv),
	)

	if err := app.Run(); err != nil {
		log.Errorf("run service fail: %v", err)
	}
}

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
	conf.WatchEndpoints(func(c *config.Config) {
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
