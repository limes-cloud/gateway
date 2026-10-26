package config

import (
	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/limes-cloud/kratosx/config"
)

// DBConf 数据库连接配置（gateway 自有库，kratosx 标准结构）
type DBConf struct {
	Enable  bool   `json:"enable"`
	Name    string `json:"name"`
	Drive   string `json:"drive"`
	Connect struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		DBName   string `json:"dbName"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"connect"`
}

// RedisConf Redis 连接配置
type RedisConf struct {
	Enable bool   `json:"enable"`
	Name   string `json:"name"`
	Host   string `json:"host"`
	DB     int    `json:"db"`
}

// Config 网关配置（启动引导 + 运行时路由）
type Config struct {
	Debug       bool
	Addr        string
	Name        string
	Version     string
	Discovery   string
	ManagerAddr string
	// ManagerSignAk/Sk manager 接口签名密钥（自注册菜单时使用，与 kratosx 算法一致）
	ManagerSignAk string
	ManagerSignSk string
	AdminAuthURL  string
	// Database/Redis 为 kratosx 标准配置结构（数组），取第一个启用实例
	Database []*DBConf    `json:"database"`
	Redis    []*RedisConf `json:"redis"`

	// Endpoints 路由表（文件保底 / 快照 / 数据库，由 Loader 降级链决定）
	Endpoints   []Endpoint
	Middlewares []Middleware

	// VersionID 当前配置版本（来自发布快照；0 = 仅文件引导，未经过发布链路）
	VersionID int64
}

// LoadBoot 从本地文件加载启动引导配置（保底，任何环境都必须可加载）
func LoadBoot(path string) (*Config, error) {
	ins := config.New(file.NewSource(path))
	if err := ins.Load(); err != nil {
		return nil, err
	}
	conf := &Config{}
	return conf, ins.Scan(conf)
}
