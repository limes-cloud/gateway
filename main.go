package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/limes-cloud/kratosx/library/env"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-kratos/kratos/v2/transport"
	_ "go.uber.org/automaxprocs"

	"github.com/limes-cloud/gateway/admin"
	"github.com/limes-cloud/gateway/client"
	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/discovery"
	_ "github.com/limes-cloud/gateway/discovery/consul"
	"github.com/limes-cloud/gateway/internal/initiator/menu"
	"github.com/limes-cloud/gateway/middleware"
	_ "github.com/limes-cloud/gateway/middleware/auth"
	_ "github.com/limes-cloud/gateway/middleware/bbr"
	"github.com/limes-cloud/gateway/middleware/circuitbreaker"
	_ "github.com/limes-cloud/gateway/middleware/cors"
	_ "github.com/limes-cloud/gateway/middleware/logging"
	_ "github.com/limes-cloud/gateway/middleware/rewrite"
	_ "github.com/limes-cloud/gateway/middleware/signature"
	_ "github.com/limes-cloud/gateway/middleware/tracing"
	_ "github.com/limes-cloud/gateway/middleware/transcoder"
	"github.com/limes-cloud/gateway/proxy"
	"github.com/limes-cloud/gateway/proxy/debug"
	"github.com/limes-cloud/gateway/server"
)

func main() {
	env.Load()

	// 启动引导：本地文件（任何环境都必须可加载，网关启动不依赖任何外部组件）
	boot, err := config.LoadBoot("config/config.yaml")
	if err != nil {
		log.Fatal(err.Error())
	}

	// 配置加载器：降级链 file(保底) → redis 快照(次新) → db(最新)
	loader := config.NewLoader(boot)
	conf, err := loader.Load(context.Background())
	if err != nil {
		log.Fatal(err.Error())
	}

	srv, err := NewServer(conf, loader)
	if err != nil {
		log.Fatal(err.Error())
	}

	// 菜单自注册到 manager（失败仅告警，不影响启动与服务）
	menu.Register(boot.ManagerAddr, boot.ManagerSignAk, boot.ManagerSignSk)

	app := kratos.New(
		kratos.Server(srv),
	)

	if err := app.Run(); err != nil {
		log.Errorf("run service fail: %v", err)
	}
}

func NewServer(conf *config.Config, loader config.Loader) (transport.Server, error) {
	clientFactory := client.NewFactory(makeDiscovery(conf.Discovery))

	pxy, err := proxy.New(clientFactory, middleware.Create)
	if err != nil {
		return nil, fmt.Errorf("failed to new proxy: %v", err)
	}

	circuitbreaker.Init(clientFactory)

	if err = pxy.Update(conf); err != nil {
		return nil, fmt.Errorf("failed to update service conf: %v", err)
	}
	// 监听配置变化（PubSub + 对账），原子更新路由
	loader.Watch(context.Background(), func(c *config.Config) {
		if er := pxy.Update(c); er != nil {
			log.Errorf("failed to update service config: %v", er)
		}
	})

	handler := http.Handler(pxy)
	if conf.Debug {
		debug.Register("proxy", pxy)
		handler = debug.MashupWithDebugHandler(pxy)
	}
	// 管理 API：/gateway/api/ 前缀拦截，不经过反向代理路由表
	handler = admin.New(loader, &admin.Options{
		AuthURL: conf.AdminAuthURL,
	}).Mashup(handler)

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
