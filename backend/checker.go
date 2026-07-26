package main

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type ServiceStatus struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Host           string    `json:"host"`
	Port           int       `json:"port"`
	CheckType      string    `json:"check_type"`
	Status         string    `json:"status"`
	ResponseTimeMs int64     `json:"response_time_ms,omitempty"`
	LastChecked    time.Time `json:"last_checked"`
	LastError      string    `json:"last_error,omitempty"`
}

// Возможные значения ServiceStatus.Status.
const (
	StatusOnline   = "online"
	StatusDegraded = "degraded"
	StatusOffline  = "offline"
	StatusUnknown  = "unknown"
)

type StatusStore struct {
	mu       sync.RWMutex
	statuses map[string]ServiceStatus
	order    []string
}

func NewStatusStore(cfg *Config) *StatusStore {
	s := &StatusStore{statuses: make(map[string]ServiceStatus)}
	for _, svc := range cfg.Services {
		checkType := svc.CheckType
		if checkType == "" {
			checkType = CheckTypeTCP
		}
		s.statuses[svc.ID] = ServiceStatus{
			ID:        svc.ID,
			Name:      svc.Name,
			Host:      svc.Host,
			Port:      svc.Port,
			CheckType: checkType,
			Status:    StatusUnknown,
		}
		s.order = append(s.order, svc.ID)
	}
	return s
}

// set обновляет статус сервиса по результату проверки.
// elapsed — время выполнения проверки; slowThresholdMs — порог (в мс),
// после которого успешная проверка помечается как "degraded" вместо
// "online" (0 — детекция отключена).
func (s *StatusStore) set(id string, online bool, errMsg string, elapsed time.Duration, slowThresholdMs int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statuses[id]

	switch {
	case !online:
		st.Status = StatusOffline
		st.LastError = errMsg
	case slowThresholdMs > 0 && elapsed.Milliseconds() > int64(slowThresholdMs):
		st.Status = StatusDegraded
		st.LastError = fmt.Sprintf("slow response: %dms (threshold %dms)", elapsed.Milliseconds(), slowThresholdMs)
	default:
		st.Status = StatusOnline
		st.LastError = ""
	}

	st.ResponseTimeMs = elapsed.Milliseconds()
	st.LastChecked = time.Now()
	s.statuses[id] = st
}

func (s *StatusStore) All() []ServiceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ServiceStatus, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.statuses[id])
	}
	return out
}

// checkTCP пытается открыть TCP-соединение до host:port.
func checkTCP(svc ServiceConfig, timeout time.Duration) (bool, string) {
	addr := net.JoinHostPort(svc.Host, strconv.Itoa(svc.Port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false, err.Error()
	}
	_ = conn.Close()
	return true, ""
}

// checkHTTP выполняет GET-запрос к health-эндпоинту сервиса (по умолчанию
// /health) и считает сервис online при получении кода ответа 2xx.
func checkHTTP(svc ServiceConfig, client *http.Client) (bool, string) {
	url := svc.HealthCheckURL()
	resp, err := client.Get(url)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Sprintf("unexpected status code %d from %s", resp.StatusCode, url)
	}
	return true, ""
}

func StartChecker(cfg *Config, store *StatusStore) {
	interval := time.Duration(cfg.CheckIntervalSeconds) * time.Second
	tcpTimeout := time.Duration(cfg.TCPTimeoutSeconds) * time.Second
	httpTimeout := time.Duration(cfg.HTTPTimeoutSeconds) * time.Second
	httpClient := &http.Client{Timeout: httpTimeout}

	check := func() {
		var wg sync.WaitGroup
		for _, svc := range cfg.Services {
			wg.Add(1)
			go func(svc ServiceConfig) {
				defer wg.Done()

				start := time.Now()
				var ok bool
				var errMsg string
				switch svc.CheckType {
				case CheckTypeHTTP:
					ok, errMsg = checkHTTP(svc, httpClient)
				default: // CheckTypeTCP и пустое значение (на случай config без LoadConfig)
					ok, errMsg = checkTCP(svc, tcpTimeout)
				}
				elapsed := time.Since(start)

				threshold := svc.EffectiveSlowThresholdMS(cfg.SlowThresholdMS)
				store.set(svc.ID, ok, errMsg, elapsed, threshold)
			}(svc)
		}
		wg.Wait()
	}

	check()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			check()
		}
	}()
}
