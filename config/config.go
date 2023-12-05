package config

import (
	kc "github.com/go-kratos/kratos/v2/config"
	"github.com/limes-cloud/kratosx/config"
)

type Config struct {
	conf        config.Config
	Debug       bool
	Addr        string
	Discovery   string
	Endpoints   []Endpoint
	Middlewares []Middleware
}

type Watch func(*Config)

// New 新建并初始化配置
func New(source kc.Source) (*Config, error) {
	ins := config.New(source)
	if err := ins.Load(); err != nil {
		return nil, err
	}

	conf := &Config{
		conf: ins,
	}
	return conf, ins.Scan(conf)
}

// Watch 监听配置
func (c *Config) Watch(key string, fn Watch) {
	c.conf.Watch(key, func(value config.Value) {
		fn(c)
	})
}
