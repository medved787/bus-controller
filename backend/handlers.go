package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type Server struct {
	cfg   *Config
	store *StatusStore
	byID  map[string]ServiceConfig
	http  *http.Client
}

func NewServer(cfg *Config, store *StatusStore) *Server {
	byID := make(map[string]ServiceConfig, len(cfg.Services))
	for _, s := range cfg.Services {
		byID[s.ID] = s
	}
	return &Server{
		cfg:   cfg,
		store: store,
		byID:  byID,
		http:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.store.All())
}

type triggerResult struct {
	Success    bool   `json:"success"`
	StatusCode int    `json:"status_code,omitempty"`
	Message    string `json:"message"`
}

func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/trigger/")
	svc, ok := s.byID[id]
	if !ok {
		http.Error(w, "unknown service id", http.StatusNotFound)
		return
	}
	if svc.Webhook.URL == "" {
		writeJSON(w, http.StatusBadRequest, triggerResult{Success: false, Message: "webhook url not configured for this service"})
		return
	}

	body := []byte(svc.Webhook.Body)
	req, err := http.NewRequest(svc.Webhook.Method, svc.Webhook.URL, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, triggerResult{Success: false, Message: "failed to build request: " + err.Error()})
		return
	}
	for k, v := range svc.Webhook.Headers {
		req.Header.Set(k, v)
	}

	if svc.Webhook.HMACSecret != "" {
		mac := hmac.New(sha256.New, []byte(svc.Webhook.HMACSecret))
		mac.Write(body)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		req.Header.Set(svc.Webhook.HMACHeader, sig)
	}

	resp, err := s.http.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, triggerResult{Success: false, Message: "request failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		writeJSON(
			w,
			http.StatusInternalServerError,
			triggerResult{
				Success: false,
				Message: err.Error(),
			},
		)
		return
	}

	ok = resp.StatusCode >= 200 && resp.StatusCode < 300
	msg := strings.TrimSpace(string(respBody))
	if msg == "" {
		msg = resp.Status
	}
	writeJSON(w, http.StatusOK, triggerResult{Success: ok, StatusCode: resp.StatusCode, Message: msg})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json response: %v", err)
	}
}
