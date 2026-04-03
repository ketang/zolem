package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Mode     string         `yaml:"mode"`
	Specs    SpecsConfig    `yaml:"specs"`
	Fixtures FixturesConfig `yaml:"fixtures"`
	Routes   []RouteConfig  `yaml:"routes"`
}

type ServerConfig struct {
	Addr string    `yaml:"addr"`
	TLS  TLSConfig `yaml:"tls"`
}

type TLSConfig struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

type SpecsConfig struct {
	CacheDir        string        `yaml:"cache_dir"`
	RefreshInterval time.Duration `yaml:"refresh_interval"`
}

type FixturesConfig struct {
	Dir   string `yaml:"dir"`
	Watch bool   `yaml:"watch"`
}

type RouteConfig struct {
	Host     string            `yaml:"host"`
	Provider string            `yaml:"provider"`
	Labels   map[string]string `yaml:"labels"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	cfg := &Config{
		Mode:   "lorem",
		Server: ServerConfig{Addr: ":8080"},
		Specs:  SpecsConfig{RefreshInterval: 6 * time.Hour},
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Mode != "lorem" && cfg.Mode != "fixture" {
		return nil, fmt.Errorf("invalid mode %q: must be lorem or fixture", cfg.Mode)
	}
	return cfg, nil
}
