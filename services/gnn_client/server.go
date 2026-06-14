package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type GnnServer struct {
	predictor *PredictorAdapter
	server    *http.Server
	mu        sync.RWMutex
}

type timeoutWriter struct {
	http.ResponseWriter
	header     http.Header
	statusCode int
	body       *bytes.Buffer
	mu         sync.Mutex
	written    bool
}

func newTimeoutWriter(w http.ResponseWriter) *timeoutWriter {
	return &timeoutWriter{
		ResponseWriter: w,
		header:         make(http.Header),
		body:           &bytes.Buffer{},
	}
}

func (tw *timeoutWriter) Header() http.Header {
	return tw.header
}

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if !tw.written {
		tw.statusCode = code
		tw.written = true
	}
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if !tw.written {
		tw.statusCode = http.StatusOK
		tw.written = true
	}
	return tw.body.Write(b)
}

func (tw *timeoutWriter) flush() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	dst := tw.ResponseWriter.Header()
	for k, v := range tw.header {
		dst[k] = v
	}
	if tw.statusCode == 0 {
		tw.statusCode = http.StatusOK
	}
	tw.ResponseWriter.WriteHeader(tw.statusCode)
	if tw.body.Len() > 0 {
		io.Copy(tw.ResponseWriter, tw.body)
	}
}

func NewGnnServer(predictor *PredictorAdapter) *GnnServer {
	return &GnnServer{
		predictor: predictor,
	}
}

func (s *GnnServer) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", s.withTimeout(s.handleHealth, 2*time.Second))
	mux.HandleFunc("/v1/predict", s.withTimeout(s.handlePredict, 2*time.Second))
	mux.HandleFunc("/v1/culture", s.withTimeout(s.handleCulture, 2*time.Second))
	mux.HandleFunc("/v1/stats/latency", s.withTimeout(s.handleLatencyStats, 2*time.Second))

	return mux
}

func (s *GnnServer) withTimeout(handler func(http.ResponseWriter, *http.Request), timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		tw := newTimeoutWriter(w)
		done := make(chan struct{})

		var cachedBody []byte
		if r.Body != nil {
			cachedBody, _ = io.ReadAll(r.Body)
			r.Body.Close()
		}

		go func() {
			defer close(done)
			newReq := r.Clone(ctx)
			if cachedBody != nil {
				newReq.Body = io.NopCloser(bytes.NewReader(cachedBody))
				newReq.ContentLength = int64(len(cachedBody))
			}
			handler(tw, newReq)
		}()

		select {
		case <-done:
			tw.flush()
		case <-ctx.Done():
			s.writeFallbackTimeout(w, r, cachedBody)
		}
	}
}

func (s *GnnServer) writeFallbackTimeout(w http.ResponseWriter, r *http.Request, cachedBody []byte) {
	if r.URL.Path == "/v1/predict" && r.Method == http.MethodPost {
		var req PredictSpreadRequest
		if len(cachedBody) > 0 {
			json.Unmarshal(cachedBody, &req)
		}

		fallback := s.predictor.FallbackPrediction(req.SourceBed, req.BacteriaName)
		fallback.IsFallback = true
		fallback.Error = "服务端超时降级"
		fallback.LatencyMs = 2000
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(fallback)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusGatewayTimeout)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"message": "请求超时",
	})
}

func (s *GnnServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "healthy",
		"service": "gnn_client",
		"time":    time.Now().Unix(),
	})
}

func (s *GnnServer) handlePredict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req PredictSpreadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(&PredictSpreadResponse{
			SourceBed:    req.SourceBed,
			BacteriaName: req.BacteriaName,
			IsFallback:   true,
			Success:      false,
			Error:        "请求解析失败: " + err.Error(),
		})
		return
	}

	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 2000
	}

	if len(req.Beds) > 0 {
		s.predictor.BuildAdjacencyMatrix(req.Beds)
	}
	s.predictor.BuildNodeFeatures()

	resultChan := s.predictor.PredictSpreadAsync(req.SourceBed, req.BacteriaName)
	result := s.predictor.WaitForPrediction(resultChan, req.SourceBed, req.BacteriaName, timeoutMs)

	if result == nil {
		fallback := s.predictor.FallbackPrediction(req.SourceBed, req.BacteriaName)
		fallback.IsFallback = true
		fallback.Error = "预测结果为空，降级返回"
		result = fallback
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

func (s *GnnServer) handleCulture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req CultureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(&AckResponse{
			Success: false,
			Message: "请求解析失败: " + err.Error(),
		})
		return
	}

	s.predictor.UpdateCultureResult(req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(&AckResponse{
		Success: true,
		Message: "培养结果已更新",
	})
}

func (s *GnnServer) handleLatencyStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	stats := s.predictor.GetLatencyStats()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(stats)
}

func (s *GnnServer) Start(addr string) error {
	mux := s.setupRoutes()

	s.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("GNN Client 服务启动，监听地址: %s", addr)
	log.Printf("  - POST /v1/predict       - 同步预测")
	log.Printf("  - POST /v1/culture       - 更新培养结果")
	log.Printf("  - GET  /v1/stats/latency - 延迟统计")
	log.Printf("  - GET  /health           - 健康检查")

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *GnnServer) Shutdown(ctx context.Context) error {
	log.Println("正在关闭 GNN Client 服务...")
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
