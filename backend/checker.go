package main

import (
	"net"
	"strconv"
	"sync"
	"time"
)

type ServiceStatus struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Status      string    `json:"status"`
	LastChecked time.Time `json:"last_checked"`
	LastError   string    `json:"last_error,omitempty"`
}

type StatusStore struct {
	mu       sync.RWMutex
	statuses map[string]ServiceStatus
	order    []string
}

func NewStatusStore(cfg *Config) *StatusStore {
	s := &StatusStore{statuses: make(map[string]ServiceStatus)}
	for _, svc := range cfg.Services {
		s.statuses[svc.ID] = ServiceStatus{
			ID:     svc.ID,
			Name:   svc.Name,
			Host:   svc.Host,
			Port:   svc.Port,
			Status: "unknown",
		}
		s.order = append(s.order, svc.ID)
	}
	return s
}

func (s *StatusStore) set(id string, online bool, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statuses[id]
	if online {
		st.Status = "online"
		st.LastError = ""
	} else {
		st.Status = "offline"
		st.LastError = errMsg
	}
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

func StartChecker(cfg *Config, store *StatusStore) {
	interval := time.Duration(cfg.CheckIntervalSeconds) * time.Second
	timeout := time.Duration(cfg.TCPTimeoutSeconds) * time.Second

	check := func() {
		var wg sync.WaitGroup
		for _, svc := range cfg.Services {
			wg.Add(1)
			go func(svc ServiceConfig) {
				defer wg.Done()
				addr := net.JoinHostPort(svc.Host, strconv.Itoa(svc.Port))
				conn, err := net.DialTimeout("tcp", addr, timeout)
				if err != nil {
					store.set(svc.ID, false, err.Error())
					return
				}
				_ = conn.Close()
				store.set(svc.ID, true, "")
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
