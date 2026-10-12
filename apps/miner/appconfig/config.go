package appconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

type Config struct {
	Network      string `json:"network"`
	MinerAddress string `json:"minerAddress"`
	Threads      int    `json:"threads"`
	LightMode    bool   `json:"lightMode"`
	DataDir      string `json:"dataDir"`
}

func DefaultDataDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Chakram"), nil
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appdata, "Chakram"), nil
	default:
		xdg := os.Getenv("XDG_DATA_HOME")
		if xdg == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			xdg = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(xdg, "chakram"), nil
	}
}

func (c *Config) EffectiveDataDir() (string, error) {
	if c.DataDir != "" {
		return c.DataDir, nil
	}
	return DefaultDataDir()
}

func (c *Config) IsConfigured() bool {
	return c.MinerAddress != "" && c.Network != ""
}

func configPath() (string, error) {
	dir, err := DefaultDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return defaultConfig(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultConfig(), nil
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig(), nil
	}
	if cfg.Network == "" {
		cfg.Network = "testnet"
	}
	if cfg.Threads <= 0 {
		cfg.Threads = 1
	}
	return &cfg, nil
}

func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func defaultConfig() *Config {
	return &Config{
		Network: "testnet",
		Threads: 1,
	}
}
