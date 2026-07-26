package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type WebhookConfig struct {
	URL        string            `json:"url"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	HMACSecret string            `json:"hmac_secret"`
	HMACHeader string            `json:"hmac_header"`
}

type ServiceConfig struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Host    string        `json:"host"`
	Port    int           `json:"port"`
	Webhook WebhookConfig `json:"webhook"`
}

type Config struct {
	CheckIntervalSeconds int             `json:"check_interval_seconds"`
	TCPTimeoutSeconds    int             `json:"tcp_timeout_seconds"`
	Services             []ServiceConfig `json:"services"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.CheckIntervalSeconds <= 0 {
		cfg.CheckIntervalSeconds = 10
	}
	if cfg.TCPTimeoutSeconds <= 0 {
		cfg.TCPTimeoutSeconds = 3
	}
	if len(cfg.Services) == 0 {
		return nil, fmt.Errorf("config has no services defined")
	}
	seen := make(map[string]bool, len(cfg.Services))
	for i := range cfg.Services {
		s := &cfg.Services[i]
		if s.ID == "" {
			return nil, fmt.Errorf("service at index %d is missing id", i)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("duplicate service id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Host == "" || s.Port == 0 {
			return nil, fmt.Errorf("service %q is missing host/port", s.ID)
		}
	}

	return &cfg, nil
}
