package config

import (
	kc "github.com/limes-cloud/kratos/config"
)

type Config struct {
	conf        kc.Config
	Debug       bool
	Addr        string
	Discovery   string
	Endpoints   []Endpoint
	Middlewares []Middleware
}

type Watch func(*Config)

// New 新建并初始化配置
func New(source kc.Source) (*Config, error) {
	kcIns := kc.New(kc.WithSource(source))
	if err := kcIns.Load(); err != nil {
		return nil, err
	}

	conf := &Config{
		conf: kcIns,
	}
	return conf, kcIns.Scan(conf)
}

// Watch 监听配置
func (c *Config) Watch(key string, fn Watch) {
	_ = c.conf.Watch(key, func(s string, value kc.Value) {
		fn(c)
	})
}
