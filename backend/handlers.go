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
	"strconv"
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

// ActionView — публичное представление action'а, отдаваемое во фронтенд.
// Секретные поля (url, hmac_secret, headers, body) сюда намеренно не входят.
type ActionView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	all := s.store.All()

	data, err := json.Marshal(all)
	if err != nil {
		http.Error(w, "failed to encode status", http.StatusInternalServerError)
		return
	}
	var items []map[string]interface{}
	if err := json.Unmarshal(data, &items); err != nil {
		http.Error(w, "failed to encode status", http.StatusInternalServerError)
		return
	}

	for _, item := range items {
		id, _ := item["id"].(string)
		svc, ok := s.byID[id]
		if !ok {
			continue
		}
		actions := make([]ActionView, 0, len(svc.Actions))
		for _, a := range svc.Actions {
			actions = append(actions, ActionView{ID: a.ID, Label: a.Label})
		}
		item["actions"] = actions
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
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

	path := strings.TrimPrefix(r.URL.Path, "/api/trigger/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "expected /api/trigger/{service_id}/{action_id}", http.StatusBadRequest)
		return
	}
	serviceID, actionID := parts[0], parts[1]

	svc, ok := s.byID[serviceID]
	if !ok {
		http.Error(w, "unknown service id", http.StatusNotFound)
		return
	}
	action, ok := svc.Action(actionID)
	if !ok {
		http.Error(w, "unknown action id", http.StatusNotFound)
		return
	}
	if action.URL == "" {
		writeJSON(w, http.StatusBadRequest, triggerResult{Success: false, Message: "webhook url not configured for this action"})
		return
	}

	rawBody := strings.ReplaceAll(action.Body, "{{ts}}", strconv.FormatInt(time.Now().UnixNano(), 10))
	body := []byte(rawBody)

	req, err := http.NewRequest(action.Method, action.URL, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, triggerResult{Success: false, Message: "failed to build request: " + err.Error()})
		return
	}
	for k, v := range action.Headers {
		req.Header.Set(k, v)
	}

	if action.HMACSecret != "" {
		mac := hmac.New(sha256.New, []byte(action.HMACSecret))
		mac.Write(body)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		req.Header.Set(action.HMACHeader, sig)
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
