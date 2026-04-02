package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    Server    `yaml:"server"`
	Mode      string    `yaml:"mode"`
	Specs     Specs     `yaml:"specs"`
	Fixtures  Fixtures  `yaml:"fixtures"`
	Routes    []Route   `yaml:"routes"`
}

type Server struct {
	Addr string `yaml:"addr"`
	TLS  *TLS   `yaml:"tls,omitempty"`
}

type TLS struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

type Specs struct {
	CacheDir        string `yaml:"cache_dir"`
	RefreshInterval string `yaml:"refresh_interval"`
}

type Fixtures struct {
	Dir   string `yaml:"dir"`
	Watch bool   `yaml:"watch"`
}

type Route struct {
	Host     string            `yaml:"host"`
	Provider string            `yaml:"provider"`
	Labels   map[string]string `yaml:"labels"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
