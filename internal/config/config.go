package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	HomeVisible   []string `toml:"home_visible"`
	HomeWriteable []string `toml:"home_writeable"`
	ProcReadable  []string `toml:"proc_readable"`
	Hide          []string `toml:"hide"`
}

func Parse(configStr string) (*Config, error) {
	var config Config
	if _, err := toml.Decode(configStr, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}
	return &config, nil
}

func Read(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %v", err)
	}
	return Parse(string(content))
}
