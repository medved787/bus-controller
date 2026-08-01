package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	path := os.Getenv("SERVICES_CONFIG")
	if path == "" {
		path = "services.json"
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		log.Fatal(err)
	}

	store := NewStatusStore(cfg)

	StartChecker(cfg, store)

	server := NewServer(cfg, store)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", server.handleStatus)
	mux.HandleFunc("/api/trigger/", server.handleTrigger)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	log.Printf("Loaded %d services", len(cfg.Services))
	log.Printf("Listening on :%s", port)

	log.Fatal(http.ListenAndServe(":"+port, mux))
}
