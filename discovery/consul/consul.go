package consul

import (
	"github.com/hashicorp/consul/api"
	"github.com/limes-cloud/gateway/discovery"
	"github.com/limes-cloud/kratos/contrib/registry/consul"
	"github.com/limes-cloud/kratos/registry"
	"net/url"
)

func init() {
	discovery.Register("consul", New)
}

func New(dsn *url.URL) (registry.Discovery, error) {
	c := api.DefaultConfig()

	c.Address = dsn.Host
	token := dsn.Query().Get("token")
	if token != "" {
		c.Token = token
	}
	datacenter := dsn.Query().Get("datacenter")
	if datacenter != "" {
		c.Datacenter = datacenter
	}
	client, err := api.NewClient(c)
	if err != nil {
		return nil, err
	}
	return consul.New(client), nil
}
