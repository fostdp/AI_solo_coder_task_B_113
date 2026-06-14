package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultRPCPort    = "50051"
	defaultHealthPort = "50052"
	defaultTimeoutSec = 2
)

type RPCRequest struct {
	Method string          `json:"method"`
	ID     int64           `json:"id"`
	Params json.RawMessage `json:"params"`
}

type RPCResponse struct {
	ID     int64       `json:"id"`
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

type EvaluateBedRequest struct {
	BedID   uint32            `json:"bed_id"`
	History []VentilatorParam `json:"history"`
}

type EvaluateAllRequest struct {
	Beds []EvaluateBedRequest `json:"beds"`
}

type CIndexRequest struct {
	Predictions []float64 `json:"predictions"`
	Events      []int32   `json:"events"`
	Times       []float64 `json:"times"`
}

type CIndexResponse struct {
	CIndex         float64 `json:"c_index"`
	BaselineCIndex float64 `json:"baseline_c_index"`
	Note           string  `json:"note"`
}

type StatsRequest struct{}

type StatsResponse struct {
	PoolSize         int32 `json:"pool_size"`
	ActiveTasks      int32 `json:"active_tasks"`
	TotalEvaluations int64 `json:"total_evaluations"`
	TotalP99LatencyUs int64 `json:"total_p99_latency_us"`
}

type VapCoxServer struct {
	rpcAddr      string
	healthAddr   string
	timeout      time.Duration
	predictor    *CoxPredictorAdapter
	pool         *GoroutinePool
	listener     net.Listener
	healthServer *http.Server

	totalEvaluations int64
	latencySamples   []int64
	latencyMu        sync.Mutex
}

func NewVapCoxServer(rpcAddr, healthAddr string, timeoutSec int, poolSize int) *VapCoxServer {
	if timeoutSec <= 0 {
		timeoutSec = defaultTimeoutSec
	}
	s := &VapCoxServer{
		rpcAddr:      rpcAddr,
		healthAddr:   healthAddr,
		timeout:      time.Duration(timeoutSec) * time.Second,
		predictor:    NewCoxPredictorAdapter(),
		pool:         NewGoroutinePool(poolSize),
		latencySamples: make([]int64, 0, 10000),
	}
	return s
}

func (s *VapCoxServer) recordLatency(start time.Time) {
	elapsedUs := time.Since(start).Microseconds()
	s.latencyMu.Lock()
	s.latencySamples = append(s.latencySamples, elapsedUs)
	if len(s.latencySamples) > 10000 {
		s.latencySamples = s.latencySamples[len(s.latencySamples)-10000:]
	}
	s.latencyMu.Unlock()
}

func (s *VapCoxServer) getP99LatencyUs() int64 {
	s.latencyMu.Lock()
	defer s.latencyMu.Unlock()
	if len(s.latencySamples) == 0 {
		return 0
	}
	sorted := make([]int64, len(s.latencySamples))
	copy(sorted, s.latencySamples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * 0.99)
	return sorted[idx]
}

func (s *VapCoxServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"time":   time.Now().Unix(),
		})
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(StatsResponse{
			PoolSize:          int32(s.pool.Size()),
			ActiveTasks:       int32(s.pool.Active()),
			TotalEvaluations:  atomic.LoadInt64(&s.totalEvaluations),
			TotalP99LatencyUs: s.getP99LatencyUs(),
		})
	})

	s.healthServer = &http.Server{
		Addr:         s.healthAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		if err := s.healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("health server error: %v\n", err)
		}
	}()

	var err error
	s.listener, err = net.Listen("tcp", s.rpcAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.rpcAddr, err)
	}

	fmt.Printf("VAP Cox RPC server listening on %s, health on %s\n", s.rpcAddr, s.healthAddr)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return err
		}
		go s.handleConnection(conn)
	}
}

func (s *VapCoxServer) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
	if s.healthServer != nil {
		s.healthServer.Close()
	}
	s.pool.Stop()
}

func (s *VapCoxServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(s.timeout))

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	enc := json.NewEncoder(writer)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				resp := RPCResponse{Error: fmt.Sprintf("read error: %v", err)}
				enc.Encode(resp)
				writer.Flush()
			}
			return
		}

		conn.SetDeadline(time.Now().Add(s.timeout))

		var req RPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := RPCResponse{Error: fmt.Sprintf("invalid json: %v", err)}
			enc.Encode(resp)
			writer.Flush()
			continue
		}

		result, errStr := s.dispatchRequest(&req)
		resp := RPCResponse{
			ID:     req.ID,
			Result: result,
			Error:  errStr,
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
		writer.Flush()
	}
}

func (s *VapCoxServer) dispatchRequest(req *RPCRequest) (interface{}, string) {
	switch req.Method {
	case "EvaluateBed":
		return s.handleEvaluateBed(req.Params)
	case "EvaluateAllBeds":
		return s.handleEvaluateAllBeds(req.Params)
	case "ComputeConcordanceIndex":
		return s.handleComputeConcordanceIndex(req.Params)
	case "GetStats":
		return s.handleGetStats(req.Params)
	default:
		return nil, fmt.Sprintf("unknown method: %s", req.Method)
	}
}

func (s *VapCoxServer) handleEvaluateBed(params json.RawMessage) (interface{}, string) {
	var req EvaluateBedRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Sprintf("invalid params: %v", err)
	}

	start := time.Now()
	result := s.predictor.EvaluateBed(req.BedID, req.History)
	atomic.AddInt64(&s.totalEvaluations, 1)
	s.recordLatency(start)

	return result, ""
}

func (s *VapCoxServer) handleEvaluateAllBeds(params json.RawMessage) (interface{}, string) {
	var req EvaluateAllRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Sprintf("invalid params: %v", err)
	}

	results := make([]*BedEvaluationResult, len(req.Beds))
	var wg sync.WaitGroup
	errCh := make(chan string, len(req.Beds))

	for i, bedReq := range req.Beds {
		wg.Add(1)
		idx := i
		bed := bedReq
		task := func() {
			defer wg.Done()
			start := time.Now()
			result := s.predictor.EvaluateBed(bed.BedID, bed.History)
			atomic.AddInt64(&s.totalEvaluations, 1)
			s.recordLatency(start)
			results[idx] = result
		}
		if err := s.pool.Submit(task); err != nil {
			wg.Done()
			errCh <- fmt.Sprintf("bed %d: %v", bed.BedID, err)
			results[idx] = &BedEvaluationResult{
				BedID:   bed.BedID,
				Success: false,
				Error:   err.Error(),
			}
		}
	}

	wg.Wait()
	close(errCh)

	var firstErr string
	for e := range errCh {
		if firstErr == "" {
			firstErr = e
		}
		break
	}

	return results, firstErr
}

func (s *VapCoxServer) handleComputeConcordanceIndex(params json.RawMessage) (interface{}, string) {
	var req CIndexRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Sprintf("invalid params: %v", err)
	}

	cIndex, baseline := s.predictor.ComputeConcordanceIndex(req.Predictions, req.Events, req.Times)

	return CIndexResponse{
		CIndex:         cIndex,
		BaselineCIndex: baseline,
		Note:           "Concordance index computed; values closer to 1.0 indicate better discriminative power",
	}, ""
}

func (s *VapCoxServer) handleGetStats(params json.RawMessage) (interface{}, string) {
	return StatsResponse{
		PoolSize:          int32(s.pool.Size()),
		ActiveTasks:       int32(s.pool.Active()),
		TotalEvaluations:  atomic.LoadInt64(&s.totalEvaluations),
		TotalP99LatencyUs: s.getP99LatencyUs(),
	}, ""
}
