package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"
)

type BedLocation struct {
	BedID     uint32  `json:"bed_id"`
	LocationX float64 `json:"location_x"`
	LocationY float64 `json:"location_y"`
}

type CultureRequest struct {
	BedID          uint32 `json:"bed_id"`
	BacteriaName   string `json:"bacteria_name"`
	Result         string `json:"result"`
	CollectedAtUnix int64  `json:"collected_at_unix"`
	ReportedAtUnix  int64  `json:"reported_at_unix"`
}

type PredictSpreadRequest struct {
	SourceBed    uint32        `json:"source_bed"`
	BacteriaName string        `json:"bacteria_name"`
	Beds         []BedLocation `json:"beds"`
	TimeoutMs    int64         `json:"timeout_ms"`
}

type PredictSpreadResponse struct {
	SourceBed    uint32    `json:"source_bed"`
	BacteriaName string    `json:"bacteria_name"`
	SpreadProb   float64   `json:"spread_prob"`
	SpreadPath   []uint32  `json:"spread_path"`
	EdgeWeights  []float64 `json:"edge_weights"`
	IsFallback   bool      `json:"is_fallback"`
	LatencyMs    int64     `json:"latency_ms"`
	Success      bool      `json:"success"`
	Error        string    `json:"error"`
}

type LatencyResponse struct {
	Count        int64 `json:"count"`
	P50Ms        int64 `json:"p50_ms"`
	P95Ms        int64 `json:"p95_ms"`
	P99Ms        int64 `json:"p99_ms"`
	PendingCount int32 `json:"pending_count"`
}

type AckResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type cultureRecord struct {
	BedID          uint32
	BacteriaName   string
	Result         string
	CollectedAtUnix int64
	ReportedAtUnix  int64
}

type pendingReq struct {
	sourceBed    uint32
	bacteriaName string
	startTime    time.Time
	resultChan   chan *PredictSpreadResponse
}

type gnnPythonRequest struct {
	SourceBed    uint32      `json:"source_bed"`
	BacteriaName string      `json:"bacteria"`
	Adjacency    [][]float64 `json:"adjacency"`
	NodeFeatures [][]float64 `json:"node_features"`
}

type gnnPythonResponse struct {
	SpreadProb  float64   `json:"spread_prob"`
	Path        []uint32  `json:"path"`
	EdgeWeights []float64 `json:"edge_weights"`
}

type PredictorAdapter struct {
	pythonServiceURL string
	httpClient       *http.Client
	bedCultures      map[uint32][]cultureRecord
	graphAdjacency   [][]float64
	nodeFeatures     [][]float64
	mu               sync.RWMutex
	asyncTimeoutMs   int64
	latencyHistory   []int64
	latencyMu        sync.RWMutex
	pendingRequests  map[string]*pendingReq
	pendingMu        sync.RWMutex
	numBeds          int
	numFeatures      int
}

func NewPredictorAdapter(pythonServiceURL string, numBeds int, numFeatures int) *PredictorAdapter {
	if numBeds <= 0 {
		numBeds = 50
	}
	if numFeatures <= 0 {
		numFeatures = 5
	}

	p := &PredictorAdapter{
		pythonServiceURL: pythonServiceURL,
		httpClient:     &http.Client{Timeout: 2 * time.Second},
		bedCultures:   make(map[uint32][]cultureRecord),
		graphAdjacency: make([][]float64, numBeds),
		nodeFeatures:   make([][]float64, numBeds),
		asyncTimeoutMs: 2000,
		latencyHistory: make([]int64, 0, 1000),
		pendingRequests: make(map[string]*pendingReq),
		numBeds:      numBeds,
		numFeatures:  numFeatures,
	}

	for i := 0; i < numBeds; i++ {
		p.graphAdjacency[i] = make([]float64, numBeds)
		p.nodeFeatures[i] = make([]float64, numFeatures)
	}

	rand.Seed(time.Now().UnixNano())
	for i := 0; i < numBeds; i++ {
		for j := 0; j < numBeds; j++ {
			if i == j {
				p.graphAdjacency[i][j] = 1.0
			} else {
				dist := math.Abs(float64(i-j)) / float64(numBeds)
				baseVal := 0.1 + (1.0-dist)*0.8
				p.graphAdjacency[i][j] = baseVal
			}
		}
	}

	for i := 0; i < numBeds; i++ {
		for j := 0; j < numFeatures; j++ {
			p.nodeFeatures[i][j] = 0.0
		}
	}

	return p
}

func (p *PredictorAdapter) BuildAdjacencyMatrix(beds []BedLocation) [][]float64 {
	numBeds := p.numBeds
	adj := make([][]float64, numBeds)
	for i := 0; i < numBeds; i++ {
		adj[i] = make([]float64, numBeds)
		adj[i][i] = 1.0
	}

	bedPositions := make(map[uint32]struct{ x, y float64 })
	for _, bed := range beds {
		bedPositions[bed.BedID] = struct{ x, y float64 }{
			x: bed.LocationX,
			y: bed.LocationY,
		}
	}

	for i := 0; i < numBeds; i++ {
		for j := i + 1; j < numBeds; j++ {
			bedIDi := uint32(i + 1)
			bedIDj := uint32(j + 1)
			posI, okI := bedPositions[bedIDi]
			posJ, okJ := bedPositions[bedIDj]
			var dist float64
			if okI && okJ {
				dx := posI.x - posJ.x
				dy := posI.y - posJ.y
				dist = math.Sqrt(dx*dx + dy*dy)
			} else {
				dist = math.Abs(float64(i - j))
			}
			val := math.Exp(-dist / 10.0)
			adj[i][j] = val
			adj[j][i] = val
		}
	}

	p.mu.Lock()
	p.graphAdjacency = adj
	p.mu.Unlock()

	return adj
}

func (p *PredictorAdapter) UpdateCultureResult(req CultureRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()

	rec := cultureRecord{
		BedID:          req.BedID,
		BacteriaName:   req.BacteriaName,
		Result:         req.Result,
		CollectedAtUnix: req.CollectedAtUnix,
		ReportedAtUnix:  req.ReportedAtUnix,
	}

	if _, exists := p.bedCultures[req.BedID]; !exists {
		p.bedCultures[req.BedID] = make([]cultureRecord, 0)
	}
	p.bedCultures[req.BedID] = append(p.bedCultures[req.BedID], rec)
}

func (p *PredictorAdapter) BuildNodeFeatures() [][]float64 {
	numBeds := p.numBeds
	numFeatures := p.numFeatures

	features := make([][]float64, numBeds)
	for i := 0; i < numBeds; i++ {
		features[i] = make([]float64, numFeatures)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	for bedID := uint32(1); bedID <= uint32(numBeds); bedID++ {
		idx := int(bedID) - 1
		if idx < 0 || idx >= numBeds {
			continue
		}

		cultures, hasCulture := p.bedCultures[bedID]
		isolationStatus := 0.0
		culturePositive := 0.0
		abxDays := 0.0
		invasiveCount := 0.0
		baselineRisk := 0.1

		if hasCulture && len(cultures) > 0 {
			isolationStatus = 1.0
			for _, c := range cultures {
				if c.Result == "positive" || c.Result == "Positive" {
					culturePositive = 1.0
					break
				}
			}
			abxDays = float64(len(cultures)) * 2.0
			invasiveCount = float64(len(cultures))
			baselineRisk = 0.3 + float64(len(cultures))*0.05
			if baselineRisk > 0.9 {
				baselineRisk = 0.9
			}
		}

		features[idx][0] = isolationStatus
		features[idx][1] = abxDays
		features[idx][2] = invasiveCount
		features[idx][3] = culturePositive
		features[idx][4] = baselineRisk
	}

	p.nodeFeatures = features
	return features
}

func (p *PredictorAdapter) PredictSpread(sourceBed uint32, bacteriaName string) (*PredictSpreadResponse, error) {
	startTime := time.Now()

	p.mu.RLock()
	adj := make([][]float64, len(p.graphAdjacency))
	for i := range p.graphAdjacency {
		adj[i] = make([]float64, len(p.graphAdjacency[i]))
		copy(adj[i], p.graphAdjacency[i])
	}
	nf := make([][]float64, len(p.nodeFeatures))
	for i := range p.nodeFeatures {
		nf[i] = make([]float64, len(p.nodeFeatures[i]))
		copy(nf[i], p.nodeFeatures[i])
	}
	p.mu.RUnlock()

	reqBody := gnnPythonRequest{
		SourceBed:    sourceBed,
		BacteriaName: bacteriaName,
		Adjacency:    adj,
		NodeFeatures: nf,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		fallback := p.FallbackPrediction(sourceBed, bacteriaName)
		fallback.LatencyMs = time.Since(startTime).Milliseconds()
		return fallback, fmt.Errorf("marshal error: %w", err)
	}

	url := p.pythonServiceURL + "/predict/gnn_spread"
	resp, err := p.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		fallback := p.FallbackPrediction(sourceBed, bacteriaName)
		fallback.LatencyMs = time.Since(startTime).Milliseconds()
		return fallback, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fallback := p.FallbackPrediction(sourceBed, bacteriaName)
		fallback.LatencyMs = time.Since(startTime).Milliseconds()
		return fallback, fmt.Errorf("http status: %d", resp.StatusCode)
	}

	var gnnResp gnnPythonResponse
	if err := json.NewDecoder(resp.Body).Decode(&gnnResp); err != nil {
		fallback := p.FallbackPrediction(sourceBed, bacteriaName)
		fallback.LatencyMs = time.Since(startTime).Milliseconds()
		return fallback, fmt.Errorf("decode error: %w", err)
	}

	result := &PredictSpreadResponse{
		SourceBed:    sourceBed,
		BacteriaName: bacteriaName,
		SpreadProb:   gnnResp.SpreadProb,
		SpreadPath:   gnnResp.Path,
		EdgeWeights:  gnnResp.EdgeWeights,
		IsFallback:   false,
		LatencyMs:    time.Since(startTime).Milliseconds(),
		Success:      true,
	}

	if result.SpreadPath == nil {
		result.SpreadPath = make([]uint32, 0)
	}
	if result.EdgeWeights == nil {
		result.EdgeWeights = make([]float64, 0)
	}
	if result.SpreadProb < 0 {
		result.SpreadProb = 0
	}
	if result.SpreadProb > 1 {
		result.SpreadProb = 1
	}

	return result, nil
}

func (p *PredictorAdapter) PredictSpreadAsync(sourceBed uint32, bacteriaName string) <-chan *PredictSpreadResponse {
	resultChan := make(chan *PredictSpreadResponse, 1)
	reqKey := fmt.Sprintf("%d-%s-%d", sourceBed, bacteriaName, time.Now().UnixNano())

	req := &pendingReq{
		sourceBed:    sourceBed,
		bacteriaName: bacteriaName,
		startTime:    time.Now(),
		resultChan:   resultChan,
	}

	p.pendingMu.Lock()
	p.pendingRequests[reqKey] = req
	p.pendingMu.Unlock()

	go func() {
		defer func() {
			p.pendingMu.Lock()
			delete(p.pendingRequests, reqKey)
			p.pendingMu.Unlock()
			close(resultChan)
		}()

		pred, err := p.PredictSpread(sourceBed, bacteriaName)
		latency := time.Since(req.startTime).Milliseconds()
		p.recordLatency(latency)

		if err != nil || pred == nil {
			fallback := p.FallbackPrediction(sourceBed, bacteriaName)
			fallback.IsFallback = true
			fallback.LatencyMs = latency
			select {
			case resultChan <- fallback:
			default:
			}
			return
		}

		pred.LatencyMs = latency
		select {
		case resultChan <- pred:
		default:
		}
	}()

	return resultChan
}

func (p *PredictorAdapter) WaitForPrediction(
	resultChan <-chan *PredictSpreadResponse,
	sourceBed uint32,
	bacteriaName string,
	timeoutMs int64,
) *PredictSpreadResponse {
	if timeoutMs <= 0 {
		timeoutMs = p.asyncTimeoutMs
	}

	select {
	case pred, ok := <-resultChan:
		if ok && pred != nil {
			return pred
		}
		fallback := p.FallbackPrediction(sourceBed, bacteriaName)
		fallback.IsFallback = true
		return fallback
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		fallback := p.FallbackPrediction(sourceBed, bacteriaName)
		fallback.IsFallback = true
		fallback.LatencyMs = timeoutMs
		p.recordLatency(timeoutMs)
		return fallback
	}
}

func (p *PredictorAdapter) PredictSpreadAsyncWithFallback(
	sourceBed uint32,
	bacteriaName string,
) *PredictSpreadResponse {
	resultChan := p.PredictSpreadAsync(sourceBed, bacteriaName)
	return p.WaitForPrediction(resultChan, sourceBed, bacteriaName, p.asyncTimeoutMs)
}

func (p *PredictorAdapter) recordLatency(latencyMs int64) {
	p.latencyMu.Lock()
	defer p.latencyMu.Unlock()
	p.latencyHistory = append(p.latencyHistory, latencyMs)
	if len(p.latencyHistory) > 1000 {
		p.latencyHistory = p.latencyHistory[len(p.latencyHistory)-1000:]
	}
}

func (p *PredictorAdapter) GetP99Latency() int64 {
	p.latencyMu.RLock()
	defer p.latencyMu.RUnlock()

	n := len(p.latencyHistory)
	if n == 0 {
		return 3000
	}

	sorted := make([]int64, n)
	copy(sorted, p.latencyHistory)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	p99Idx := int(math.Ceil(0.99*float64(n))) - 1
	if p99Idx < 0 {
		p99Idx = 0
	}
	return sorted[p99Idx]
}

func (p *PredictorAdapter) GetLatencyStats() LatencyResponse {
	p.latencyMu.RLock()
	defer p.latencyMu.RUnlock()

	n := len(p.latencyHistory)
	stats := LatencyResponse{
		Count:        int64(n),
		P50Ms:        0,
		P95Ms:        0,
		P99Ms:        0,
		PendingCount: int32(p.GetPendingCount()),
	}
	if n == 0 {
		return stats
	}

	sorted := make([]int64, n)
	copy(sorted, p.latencyHistory)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	p50Idx := int(math.Ceil(0.50*float64(n))) - 1
	p95Idx := int(math.Ceil(0.95*float64(n))) - 1
	p99Idx := int(math.Ceil(0.99*float64(n))) - 1
	if p50Idx < 0 {
		p50Idx = 0
	}
	if p95Idx < 0 {
		p95Idx = 0
	}
	if p99Idx < 0 {
		p99Idx = 0
	}

	stats.P50Ms = sorted[p50Idx]
	stats.P95Ms = sorted[p95Idx]
	stats.P99Ms = sorted[p99Idx]
	return stats
}

func (p *PredictorAdapter) GetPendingCount() int {
	p.pendingMu.RLock()
	defer p.pendingMu.RUnlock()
	return len(p.pendingRequests)
}

func (p *PredictorAdapter) FallbackPrediction(sourceBed uint32, bacteriaName string) *PredictSpreadResponse {
	p.mu.RLock()
	adj := p.graphAdjacency
	p.mu.RUnlock()

	numBeds := len(adj)
	sourceIdx := int(sourceBed) - 1
	if sourceIdx < 0 {
		sourceIdx = 0
	}
	if sourceIdx >= numBeds {
		sourceIdx = numBeds - 1
	}

	type neighbor struct {
		idx    int
		bedID  uint32
		weight float64
	}
	neighbors := make([]neighbor, 0, numBeds)

	for i := 0; i < numBeds; i++ {
		if i == sourceIdx {
			continue
		}
		w := 0.0
		if sourceIdx < numBeds && i < numBeds {
			w = adj[sourceIdx][i]
		}
		neighbors = append(neighbors, neighbor{
			idx:    i,
			bedID:  uint32(i + 1),
			weight: w,
		})
	}

	sort.Slice(neighbors, func(a, b int) bool {
		return neighbors[a].weight > neighbors[b].weight
	})

	topK := 5
	if len(neighbors) < topK {
		topK = len(neighbors)
	}

	path := make([]uint32, 0, topK+1)
	path = append(path, sourceBed)
	edgeWeights := make([]float64, 0, topK)

	for i := 0; i < topK; i++ {
		path = append(path, neighbors[i].bedID)
		edgeWeights = append(edgeWeights, neighbors[i].weight)
	}

	spreadProb := 0.3 + 0.01*rand.Float64()*70.0
	if spreadProb > 1.0 {
		spreadProb = 1.0
	}

	for i := range edgeWeights {
		edgeWeights[i] = rand.Float64()
	}

	return &PredictSpreadResponse{
		SourceBed:    sourceBed,
		BacteriaName: bacteriaName,
		SpreadProb:   spreadProb,
		SpreadPath:   path,
		EdgeWeights:  edgeWeights,
		IsFallback:   true,
		LatencyMs:    0,
		Success:      true,
		Error:        "降级预测：GNN服务不可用或超时",
	}
}
