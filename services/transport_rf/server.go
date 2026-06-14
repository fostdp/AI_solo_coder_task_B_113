package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type TransportServer struct {
	scorer *ScorerAdapter
	addr   string
}

func NewTransportServer(scorer *ScorerAdapter, addr string) *TransportServer {
	return &TransportServer{
		scorer: scorer,
		addr:   addr,
	}
}

func (s *TransportServer) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

func (s *TransportServer) writeError(w http.ResponseWriter, status int, errMsg string) {
	resp := &ScoreResponse{
		Success: false,
		Error:   errMsg,
	}
	s.writeJSON(w, status, resp)
}

func (s *TransportServer) handleScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req ScoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	defer r.Body.Close()

	resp := s.scorer.Score(req)
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *TransportServer) handleScoreBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var requests []ScoreRequest
	if err := json.NewDecoder(r.Body).Decode(&requests); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	defer r.Body.Close()

	responses := s.scorer.ScoreBatch(requests)
	s.writeJSON(w, http.StatusOK, responses)
}

func (s *TransportServer) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	stats := s.scorer.GetStats()
	s.writeJSON(w, http.StatusOK, stats)
}

func (s *TransportServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "healthy",
		"service": "transport-risk-scorer",
	})
}

func (s *TransportServer) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/score", s.handleScore)
	mux.HandleFunc("/v1/score/batch", s.handleScoreBatch)
	mux.HandleFunc("/v1/stats", s.handleStats)
	mux.HandleFunc("/health", s.handleHealth)

	log.Printf("Transport risk scorer server starting on %s", s.addr)
	log.Printf("Endpoints:")
	log.Printf("  POST   /v1/score         - Single request scoring")
	log.Printf("  POST   /v1/score/batch   - Batch scoring")
	log.Printf("  GET    /v1/stats         - Service statistics")
	log.Printf("  GET    /health           - Health check")

	return http.ListenAndServe(s.addr, mux)
}
