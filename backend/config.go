package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

type WebhookConfig struct {
	URL        string            `json:"url"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	HMACSecret string            `json:"hmac_secret"`
	HMACHeader string            `json:"hmac_header"`
}

// CheckType задаёт способ проверки доступности сервиса.
const (
	CheckTypeTCP  = "tcp"
	CheckTypeHTTP = "http"
)

type ServiceConfig struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Host    string        `json:"host"`
	Port    int           `json:"port"`
	Webhook WebhookConfig `json:"webhook"`

	// CheckType: "tcp" (по умолчанию) — открыть TCP-соединение до host:port;
	// "http" — выполнить HTTP GET и проверить код ответа (2xx = online).
	CheckType string `json:"check_type"`

	// HealthPath — путь для HTTP-проверки, используется вместе с host:port,
	// если не задан HealthURL. По умолчанию "/health".
	HealthPath string `json:"health_path"`

	// HealthURL — полный URL для HTTP-проверки. Если задан, host/port
	// для построения URL не используются (но остаются обязательными
	// полями сервиса, т.к. отображаются в UI и используются в вебхуках).
	HealthURL string `json:"health_url"`

	// HealthScheme — схема, используемая при построении URL из host:port
	// (когда HealthURL не задан). По умолчанию "http".
	HealthScheme string `json:"health_scheme"`

	// SlowThresholdMS — порог (в мс), после которого успешная проверка
	// помечается как "degraded", а не "online". 0 (не задано) — берётся
	// глобальное значение Config.SlowThresholdMS. Отрицательное число
	// отключает degraded-детекцию для этого сервиса, даже если задан
	// глобальный порог.
	SlowThresholdMS int `json:"slow_threshold_ms"`
}

type Config struct {
	CheckIntervalSeconds int             `json:"check_interval_seconds"`
	TCPTimeoutSeconds    int             `json:"tcp_timeout_seconds"`
	HTTPTimeoutSeconds   int             `json:"http_timeout_seconds"`

	// SlowThresholdMS — глобальный порог (в мс) по умолчанию для всех
	// сервисов, у которых не задан собственный slow_threshold_ms.
	// 0 (по умолчанию) — degraded-детекция отключена.
	SlowThresholdMS int `json:"slow_threshold_ms"`

	Services []ServiceConfig `json:"services"`
}

// EffectiveSlowThresholdMS возвращает итоговый порог "медленного ответа"
// для сервиса с учётом override на уровне сервиса и глобального значения.
// 0 означает "degraded-детекция отключена".
func (s *ServiceConfig) EffectiveSlowThresholdMS(globalDefault int) int {
	switch {
	case s.SlowThresholdMS < 0:
		return 0
	case s.SlowThresholdMS > 0:
		return s.SlowThresholdMS
	default:
		return globalDefault
	}
}

// HealthCheckURL возвращает URL, который нужно опросить для HTTP-проверки.
// Если задан HealthURL — используется он как есть, иначе URL строится из
// HealthScheme + host:port + HealthPath.
func (s *ServiceConfig) HealthCheckURL() string {
	if s.HealthURL != "" {
		return s.HealthURL
	}
	scheme := s.HealthScheme
	if scheme == "" {
		scheme = "http"
	}
	path := s.HealthPath
	if path == "" {
		path = "/health"
	}
	return scheme + "://" + net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) + path
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
	if cfg.HTTPTimeoutSeconds <= 0 {
		cfg.HTTPTimeoutSeconds = 3
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

		if s.CheckType == "" {
			s.CheckType = CheckTypeTCP
		}
		switch s.CheckType {
		case CheckTypeTCP:
			// TCP-проверка не использует health_* поля.
		case CheckTypeHTTP:
			if s.HealthScheme == "" {
				s.HealthScheme = "http"
			}
			if s.HealthPath == "" {
				s.HealthPath = "/health"
			}
			if s.HealthURL == "" && !strings.HasPrefix(s.HealthPath, "/") {
				s.HealthPath = "/" + s.HealthPath
			}
		default:
			return nil, fmt.Errorf("service %q has unknown check_type %q (expected %q or %q)", s.ID, s.CheckType, CheckTypeTCP, CheckTypeHTTP)
		}
	}

	return &cfg, nil
}