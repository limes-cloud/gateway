package main

import (
	"os"

	_ "github.com/limes-cloud/gateway/discovery/consul"
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
	"github.com/limes-cloud/kratos/config/file"
	"github.com/limes-cloud/kratos/log"
	"github.com/limes-cloud/kratos/middleware/tracing"
)

// go build -ldflags "-X main.Version=x.y.z"
var (
	// Name is the name of the compiled software.
	Name string
	// Version is the version of the compiled software.
	Version string

	id, _ = os.Hostname()
)

func main() {
	conf, err := config.New(file.NewSource("config/config.yaml"))
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
