package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

type SuggestRequest struct {
	PreviousSolutionID string `json:"previous_solution_id"`
	CurrentSolutionID  string `json:"current_solution_id"`
}

type SuggestResponse struct {
	Suggestions []Suggestion `json:"suggestions"`
}

type LambdaRequest struct {
	Lambda float64 `json:"lambda"`
}

type AckResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type ChangeRateResponse struct {
	LatestChangeRate  float64 `json:"latest_change_rate"`
	AverageChangeRate float64 `json:"average_change_rate"`
	TotalSolves       int64   `json:"total_solves"`
}

type Server struct {
	solver *SolverAdapter
	port   string
}

func NewServer(solver *SolverAdapter, port string) *Server {
	return &Server{
		solver: solver,
		port:   port,
	}
}

func (s *Server) solveWithDeadline(req SolveRequest) *SolveResponse {
	start := time.Now()
	deadline := time.Duration(req.DeadlineMs) * time.Millisecond
	if deadline == 0 {
		deadline = 5 * time.Second
	}

	resultCh := make(chan *SolveResponse, 1)
	go func() {
		resultCh <- s.solver.FullSolve(req)
	}()

	select {
	case result := <-resultCh:
		result.SolveTimeMs = time.Since(start).Milliseconds()
		return result
	case <-time.After(deadline):
		greedy := s.solver.GreedySolve(req)
		greedy.IsGreedyFallback = true
		greedy.Status = "greedy_fallback"
		greedy.SolveTimeMs = time.Since(start).Milliseconds()
		return greedy
	}
}

func (s *Server) handleSolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result := s.solveWithDeadline(req)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("[SERVER] Error encoding response: %v", err)
	}

	log.Printf("[SERVER] Solve request: beds=%d, np=%d, nurses=%d, status=%s, time=%dms, greedy=%v, change_rate=%.4f",
		len(req.BedRisks), req.NegativePressureBeds, req.NursesPerShift,
		result.Status, result.SolveTimeMs, result.IsGreedyFallback, result.ChangeRate)
}

func (s *Server) handleSuggestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SuggestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	suggestions := s.solver.GetCachedSuggestions(req.PreviousSolutionID, req.CurrentSolutionID)
	resp := SuggestResponse{Suggestions: suggestions}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleChangeRate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	latest, avg, total := s.solver.GetChangeRateStats()
	resp := ChangeRateResponse{
		LatestChangeRate:  latest,
		AverageChangeRate: avg,
		TotalSolves:       total,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleLambda(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LambdaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.solver.SetStabilityLambda(req.Lambda)
	actualLambda := s.solver.GetStabilityLambda()

	resp := AckResponse{
		Success: true,
		Message: "Stability lambda set to " + formatFloat(actualLambda),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)

	log.Printf("[SERVER] Stability lambda updated: %.4f", actualLambda)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"service": "scheduler_optimizer",
		"time":    time.Now().Unix(),
		"lambda":  s.solver.GetStabilityLambda(),
	})
}

func formatFloat(f float64) string {
	return string(appendFloat([]byte{}, f))
}

func appendFloat(buf []byte, f float64) []byte {
	if f == 0 {
		return append(buf, '0')
	}
	if f < 0 {
		buf = append(buf, '-')
		f = -f
	}
	mag := int64(f * 10000)
	buf = appendInt(buf, mag/10000)
	buf = append(buf, '.')
	frac := mag % 10000
	if frac < 0 {
		frac = -frac
	}
	fracStr := appendInt([]byte{}, frac)
	for len(fracStr) < 4 {
		fracStr = append([]byte{'0'}, fracStr...)
	}
	buf = append(buf, fracStr...)
	return buf
}

func appendInt(buf []byte, n int64) []byte {
	if n == 0 {
		return append(buf, '0')
	}
	if n < 0 {
		buf = append(buf, '-')
		n = -n
	}
	var tmp [20]byte
	pos := len(tmp)
	for n > 0 {
		pos--
		tmp[pos] = byte('0' + n%10)
		n /= 10
	}
	return append(buf, tmp[pos:]...)
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/solve", s.handleSolve)
	mux.HandleFunc("/v1/suggestions", s.handleSuggestions)
	mux.HandleFunc("/v1/stats/change_rate", s.handleChangeRate)
	mux.HandleFunc("/v1/lambda", s.handleLambda)
	mux.HandleFunc("/health", s.handleHealth)

	addr := ":" + s.port
	log.Printf("[SERVER] Scheduler Optimizer Service starting on port %s", s.port)
	log.Printf("[SERVER] Default stability lambda: %.4f", s.solver.GetStabilityLambda())
	log.Printf("[SERVER] Endpoints:")
	log.Printf("[SERVER]   POST   /v1/solve")
	log.Printf("[SERVER]   POST   /v1/suggestions")
	log.Printf("[SERVER]   GET    /v1/stats/change_rate")
	log.Printf("[SERVER]   POST   /v1/lambda")
	log.Printf("[SERVER]   GET    /health")

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return server.ListenAndServe()
}
