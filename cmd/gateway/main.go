package main

import (
	"fmt"
	"github.com/limes-cloud/gateway/client"
	"github.com/limes-cloud/gateway/discovery"
	"github.com/limes-cloud/gateway/middleware"
	"github.com/limes-cloud/gateway/middleware/circuitbreaker"
	"github.com/limes-cloud/gateway/proxy"
	"github.com/limes-cloud/gateway/proxy/debug"
	"github.com/limes-cloud/gateway/server"
	"github.com/limes-cloud/kratos/contrib/config/configure"
	"github.com/limes-cloud/kratos/registry"
	"github.com/limes-cloud/kratos/transport"
	"net/http"
	"os"

	_ "github.com/limes-cloud/gateway/discovery/consul"
	_ "github.com/limes-cloud/gateway/middleware/auth"
	_ "github.com/limes-cloud/gateway/middleware/bbr"
	_ "github.com/limes-cloud/gateway/middleware/cors"
	_ "github.com/limes-cloud/gateway/middleware/logging"
	_ "github.com/limes-cloud/gateway/middleware/rewrite"
	_ "github.com/limes-cloud/gateway/middleware/tracing"
	_ "github.com/limes-cloud/gateway/middleware/transcoder"
	_ "go.uber.org/automaxprocs"
	_ "net/http/pprof"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/kratos"
	"github.com/limes-cloud/kratos/log"
	"github.com/limes-cloud/kratos/middleware/tracing"
)

// go build -ldflags "-X main.Version=x.y.z"
var (
	ConfigHost  string
	ConfigToken string
	// Name is the name of the compiled software.
	Name string
	// Version is the version of the compiled software.
	Version string

	id, _ = os.Hostname()
)

func main() {
	conf, err := config.New(configure.NewFromEnv())
	if err != nil {
		log.Fatal(err.Error())
	}

	server, err := NewServer(conf)
	if err != nil {
		log.Fatal(err.Error())
	}

	app := kratos.New(
		kratos.ID(id),
		kratos.Name(Name),
		kratos.Version(Version),
		kratos.Metadata(map[string]string{}),
		kratos.Server(server),
		kratos.LoggerWith(kratos.LogField{
			"id":      id,
			"name":    Name,
			"version": Version,
			"trace":   tracing.TraceID(),
			"span":    tracing.SpanID(),
		}),
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
