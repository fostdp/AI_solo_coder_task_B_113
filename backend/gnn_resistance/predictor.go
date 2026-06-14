package gnn_resistance

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

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

type GNNSpreadPredictor struct {
	config        config.GNNConfig
	httpClient    *http.Client
	BedCulture    map[uint32][]models.CultureResult
	BedPrediction map[uint32]*models.ResistancePrediction
	GraphAdjacency [][]float64
	NodeFeatures   [][]float64
	mu            sync.RWMutex
	stopChan      chan struct{}
	wg            sync.WaitGroup
	Instance      *GNNSpreadPredictor
}

var Instance *GNNSpreadPredictor
var instanceOnce sync.Once

type gnnRequest struct {
	SourceBed    uint32      `json:"source_bed"`
	BacteriaName string      `json:"bacteria"`
	Adjacency    [][]float64 `json:"adjacency"`
	NodeFeatures [][]float64 `json:"node_features"`
}

type gnnResponse struct {
	SpreadProb  float64   `json:"spread_prob"`
	Path        []uint32  `json:"path"`
	EdgeWeights []float64 `json:"edge_weights"`
}

func NewGNNSpreadPredictor(cfg config.GNNConfig) *GNNSpreadPredictor {
	numBeds := 50
	numFeatures := 5
	if cfg.NumOfBeds > 0 {
		numBeds = cfg.NumOfBeds
	}
	if cfg.NumFeatures > 0 {
		numFeatures = cfg.NumFeatures
	}

	p := &GNNSpreadPredictor{
		config:        cfg,
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		BedCulture:    make(map[uint32][]models.CultureResult),
		BedPrediction: make(map[uint32]*models.ResistancePrediction),
		GraphAdjacency: make([][]float64, numBeds),
		NodeFeatures:   make([][]float64, numBeds),
		stopChan:      make(chan struct{}),
	}

	for i := 0; i < numBeds; i++ {
		p.GraphAdjacency[i] = make([]float64, numBeds)
		p.NodeFeatures[i] = make([]float64, numFeatures)
	}

	rand.Seed(time.Now().UnixNano())
	for i := 0; i < numBeds; i++ {
		for j := 0; j < numBeds; j++ {
			if i == j {
				p.GraphAdjacency[i][j] = 1.0
			} else {
				dist := math.Abs(float64(i-j)) / float64(numBeds)
				baseVal := 0.1 + (1.0-dist)*0.8
				p.GraphAdjacency[i][j] = baseVal
			}
		}
	}

	for i := 0; i < numBeds; i++ {
		for j := 0; j < numFeatures; j++ {
			p.NodeFeatures[i][j] = 0.0
		}
	}

	instanceOnce.Do(func() {
		Instance = p
	})

	return p
}

func (p *GNNSpreadPredictor) BuildAdjacencyMatrix(beds []models.Bed) [][]float64 {
	numBeds := 50
	if len(p.GraphAdjacency) > 0 {
		numBeds = len(p.GraphAdjacency)
	}

	adj := make([][]float64, numBeds)
	for i := 0; i < numBeds; i++ {
		adj[i] = make([]float64, numBeds)
		adj[i][i] = 1.0
	}

	bedPositions := make(map[int]struct{ x, y float64 })
	for _, bed := range beds {
		bedPositions[bed.ID] = struct{ x, y float64 }{
			x: bed.LocationX,
			y: bed.LocationY,
		}
	}

	var maxDist float64 = 1.0
	for i := 0; i < numBeds; i++ {
		for j := i + 1; j < numBeds; j++ {
			posI, okI := bedPositions[i+1]
			posJ, okJ := bedPositions[j+1]
			var dist float64
			if okI && okJ {
				dx := posI.x - posJ.x
				dy := posI.y - posJ.y
				dist = math.Sqrt(dx*dx + dy*dy)
			} else {
				dist = math.Abs(float64(i - j))
			}
			if dist > maxDist {
				maxDist = dist
			}
			val := math.Exp(-dist / 10.0)
			adj[i][j] = val
			adj[j][i] = val
		}
	}

	p.mu.Lock()
	p.GraphAdjacency = adj
	p.mu.Unlock()

	return adj
}

func (p *GNNSpreadPredictor) Start() {
	p.wg.Add(1)
	go p.Run()
}

func (p *GNNSpreadPredictor) Stop() {
	close(p.stopChan)
	p.wg.Wait()
}

func (p *GNNSpreadPredictor) Run() {
	defer p.wg.Done()

	interval := time.Duration(p.config.UpdateIntervalSec) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			p.PredictAllSpread()
		}
	}
}

func (p *GNNSpreadPredictor) UpdateCultureResult(cr models.CultureResult) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.BedCulture[cr.BedID]; !exists {
		p.BedCulture[cr.BedID] = make([]models.CultureResult, 0)
	}
	p.BedCulture[cr.BedID] = append(p.BedCulture[cr.BedID], cr)
}

func (p *GNNSpreadPredictor) BuildNodeFeatures() [][]float64 {
	numBeds := 50
	if len(p.NodeFeatures) > 0 {
		numBeds = len(p.NodeFeatures)
	}
	numFeatures := 5
	if numBeds > 0 && len(p.NodeFeatures[0]) > 0 {
		numFeatures = len(p.NodeFeatures[0])
	}

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

		cultures, hasCulture := p.BedCulture[bedID]
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

	p.NodeFeatures = features
	return features
}

func (p *GNNSpreadPredictor) PredictAllSpread() {
	p.BuildNodeFeatures()

	p.mu.RLock()
	bedsWithCulture := make([]uint32, 0, len(p.BedCulture))
	bacteriaMap := make(map[uint32]string)
	for bedID, cultures := range p.BedCulture {
		if len(cultures) > 0 {
			bedsWithCulture = append(bedsWithCulture, bedID)
			bacteriaMap[bedID] = cultures[len(cultures)-1].BacteriaName
		}
	}
	p.mu.RUnlock()

	for _, bedID := range bedsWithCulture {
		bacteriaName := bacteriaMap[bedID]
		if bacteriaName == "" {
			bacteriaName = "Unknown"
		}
		_, _ = p.PredictSpread(bedID, bacteriaName)
	}
}

func (p *GNNSpreadPredictor) PredictSpread(sourceBed uint32, bacteriaName string) (*models.ResistancePrediction, error) {
	p.mu.RLock()
	adj := make([][]float64, len(p.GraphAdjacency))
	for i := range p.GraphAdjacency {
		adj[i] = make([]float64, len(p.GraphAdjacency[i]))
		copy(adj[i], p.GraphAdjacency[i])
	}
	nf := make([][]float64, len(p.NodeFeatures))
	for i := range p.NodeFeatures {
		nf[i] = make([]float64, len(p.NodeFeatures[i]))
		copy(nf[i], p.NodeFeatures[i])
	}
	p.mu.RUnlock()

	reqBody := gnnRequest{
		SourceBed:    sourceBed,
		BacteriaName: bacteriaName,
		Adjacency:    adj,
		NodeFeatures: nf,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		fallback := p.FallbackPrediction(sourceBed, bacteriaName)
		return fallback, fmt.Errorf("marshal error: %w", err)
	}

	url := p.config.PythonServiceURL + "/predict/gnn_spread"
	resp, err := p.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		fallback := p.FallbackPrediction(sourceBed, bacteriaName)
		p.cachePrediction(fallback)
		return fallback, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fallback := p.FallbackPrediction(sourceBed, bacteriaName)
		p.cachePrediction(fallback)
		return fallback, fmt.Errorf("http status: %d", resp.StatusCode)
	}

	var gnnResp gnnResponse
	if err := json.NewDecoder(resp.Body).Decode(&gnnResp); err != nil {
		fallback := p.FallbackPrediction(sourceBed, bacteriaName)
		p.cachePrediction(fallback)
		return fallback, fmt.Errorf("decode error: %w", err)
	}

	now := time.Now()
	prediction := &models.ResistancePrediction{
		ID:             0,
		BedID:          sourceBed,
		SourceBed:      sourceBed,
		BacteriaName:   bacteriaName,
		GeneSpreadProb: gnnResp.SpreadProb,
		SpreadProb:     gnnResp.SpreadProb,
		SpreadPath:     gnnResp.Path,
		Path:           gnnResp.Path,
		EdgeWeights:    gnnResp.EdgeWeights,
		Time:           now,
		PredictedAt:    now,
		IsFallback:     false,
	}

	if prediction.Path == nil {
		prediction.Path = make([]uint32, 0)
	}
	if prediction.EdgeWeights == nil {
		prediction.EdgeWeights = make([]float64, 0)
	}
	if prediction.SpreadProb < 0 {
		prediction.SpreadProb = 0
	}
	if prediction.SpreadProb > 1 {
		prediction.SpreadProb = 1
	}

	p.cachePrediction(prediction)
	return prediction, nil
}

func (p *GNNSpreadPredictor) cachePrediction(pred *models.ResistancePrediction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.BedPrediction[pred.SourceBed] = pred
}

func (p *GNNSpreadPredictor) FallbackPrediction(sourceBed uint32, bacteriaName string) *models.ResistancePrediction {
	p.mu.RLock()
	adj := p.GraphAdjacency
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
		idx   int
		bedID uint32
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

	now := time.Now()
	return &models.ResistancePrediction{
		ID:             0,
		BedID:          sourceBed,
		SourceBed:      sourceBed,
		BacteriaName:   bacteriaName,
		GeneSpreadProb: spreadProb,
		SpreadProb:     spreadProb,
		SpreadPath:     path,
		Path:           path,
		EdgeWeights:    edgeWeights,
		Time:           now,
		PredictedAt:    now,
		IsFallback:     true,
	}
}

func (p *GNNSpreadPredictor) GetPrediction(bedID uint32) *models.ResistancePrediction {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.BedPrediction[bedID]
}

func (p *GNNSpreadPredictor) GetAllPredictions() map[uint32]*models.ResistancePrediction {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make(map[uint32]*models.ResistancePrediction, len(p.BedPrediction))
	for k, v := range p.BedPrediction {
		result[k] = v
	}
	return result
}

func GetInstance() *GNNSpreadPredictor {
	return Instance
}
