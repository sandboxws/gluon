// Package config reads the shop's settings: the settings file, then the SHOP_
// environment, over the defaults.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config is the settings the services read, as a document.
type Config struct {
	HTTP     Listen   `yaml:"http" toml:"http" json:"http"`
	GRPC     Listen   `yaml:"grpc" toml:"grpc" json:"grpc"`
	Database Database `yaml:"database" toml:"database" json:"database"`
	Features []string `yaml:"features,omitempty" toml:"features,omitempty" json:"features,omitempty"`
	LogLevel string   `yaml:"log_level" toml:"log_level" json:"log_level"`
}

// Listen is where a server listens.
type Listen struct {
	Addr string `yaml:"addr" toml:"addr" json:"addr"`
}

// Database is where the data is.
type Database struct {
	Path     string `yaml:"path" toml:"path" json:"path"`
	ReadOnly bool   `yaml:"read_only,omitempty" toml:"read_only,omitempty" json:"read_only,omitempty"`
}

// File is where the settings are read from: $SHOP_CONFIG, or config.yaml in
// the working directory.
func File() string {
	if p := os.Getenv("SHOP_CONFIG"); p != "" {
		return p
	}
	return "config.yaml"
}

// Load reads the settings file, then the environment. A setting neither has
// comes from the defaults below.
func Load() (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigFile(File())
	v.SetDefault("http.addr", "127.0.0.1:8765")
	v.SetDefault("grpc.addr", "127.0.0.1:50051")
	v.SetDefault("log_level", "info")
	v.SetEnvPrefix("SHOP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	return v, v.ReadInConfig()
}

// Resolve is a path setting, relative to the settings file that named it.
func Resolve(v *viper.Viper, key string) string {
	p := v.GetString(key)
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(filepath.Dir(File()), p)
}

// Defaults is the settings with nothing read.
func Defaults() Config {
	return Config{
		HTTP:     Listen{Addr: "127.0.0.1:8765"},
		GRPC:     Listen{Addr: "127.0.0.1:50051"},
		Database: Database{Path: "data/shop.db"},
		Features: []string{"gift-wrap"},
		LogLevel: "info",
	}
}
