package transport_rf

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

type TreeVote struct {
	Score int
}

type TransportScorer struct {
	cfg                   config.TransportConfig
	trees                 []*DecisionTree
	InChan                <-chan models.TransportRequest
	OutChan               chan<- models.TransportRiskResult
	LatestResults         map[uint32]*models.TransportRiskResult
	vitalStabilityProvider interface { GetVitalStabilityScore(bedID uint32) float64 }
	infectionProvider     interface { GetInfectionRisk(bedID uint32) float64 }
	mu                    sync.RWMutex
	wg                    sync.WaitGroup
	stopChan              chan struct{}
}

var Instance *TransportScorer

type DecisionTree struct {
	Root *TreeNode
}

type TreeNode struct {
	IsLeaf       bool
	FeatureIndex int
	Threshold    float64
	Left         *TreeNode
	Right        *TreeNode
	LeafValue    int
	LeafContrib  map[string]float64
}

var featureNames = []string{"vital_stability", "infection_risk", "distance", "urgent", "priority", "day_hours"}

func NewTransportScorer(cfg config.TransportConfig, in <-chan models.TransportRequest, out chan<- models.TransportRiskResult) *TransportScorer {
	ts := &TransportScorer{
		cfg:           cfg,
		InChan:        in,
		OutChan:       out,
		LatestResults: make(map[uint32]*models.TransportRiskResult),
		stopChan:      make(chan struct{}),
	}

	nTrees := cfg.ForestTrees
	if nTrees <= 0 {
		nTrees = 50
	}
	ts.trees = make([]*DecisionTree, nTrees)
	for i := 0; i < nTrees; i++ {
		ts.trees[i] = buildDecisionTree(3)
	}

	Instance = ts
	return ts
}

func buildDecisionTree(maxDepth int) *DecisionTree {
	root := buildNode(0, maxDepth)
	return &DecisionTree{Root: root}
}

func buildNode(depth, maxDepth int) *TreeNode {
	if depth >= maxDepth {
		return buildLeafNode()
	}

	featureIdx := rand.Intn(6)
	var threshold float64
	switch featureIdx {
	case 0:
		threshold = 30 + rand.Float64()*50
	case 1:
		threshold = 0.2 + rand.Float64()*0.5
	case 2:
		threshold = 0.1 + rand.Float64()*0.6
	case 3:
		threshold = 0.5
	case 4:
		threshold = 0.2 + rand.Float64()*0.6
	case 5:
		threshold = 0.2 + rand.Float64()*0.5
	}

	return &TreeNode{
		IsLeaf:       false,
		FeatureIndex: featureIdx,
		Threshold:    threshold,
		Left:         buildNode(depth+1, maxDepth),
		Right:        buildNode(depth+1, maxDepth),
	}
}

func buildLeafNode() *TreeNode {
	score := rand.Intn(101)
	contrib := make(map[string]float64)
	for _, name := range featureNames {
		contrib[name] = rand.Float64()
	}
	sum := 0.0
	for _, v := range contrib {
		sum += v
	}
	if sum > 0 {
		for k := range contrib {
			contrib[k] = contrib[k] / sum
		}
	}
	return &TreeNode{
		IsLeaf:      true,
		LeafValue:   score,
		LeafContrib: contrib,
	}
}

func (ts *TransportScorer) Start() {
	ts.wg.Add(1)
	go ts.Run()
}

func (ts *TransportScorer) Stop() {
	close(ts.stopChan)
	ts.wg.Wait()
}

func (ts *TransportScorer) Run() {
	defer ts.wg.Done()
	ts.requestLoop()
}

func (ts *TransportScorer) requestLoop() {
	for {
		select {
		case <-ts.stopChan:
			return
		case req, ok := <-ts.InChan:
			if !ok {
				return
			}
			result, err := ts.ScoreRequest(req)
			if err != nil {
				continue
			}
			select {
			case ts.OutChan <- *result:
			default:
			}
		}
	}
}

func (ts *TransportScorer) ScoreRequest(req models.TransportRequest) (*models.TransportRiskResult, error) {
	features := ts.buildFeatures(req)

	scores := make([]int, len(ts.trees))
	contribSum := make(map[string]float64)
	for _, name := range featureNames {
		contribSum[name] = 0.0
	}

	for i, tree := range ts.trees {
		contrib := make(map[string]float64)
		score, treeContrib := ts.traverseTree(tree.Root, features, contrib)
		scores[i] = score
		for k, v := range treeContrib {
			contribSum[k] += v
		}
	}

	sort.Ints(scores)
	medianScore := scores[len(scores)/2]
	medianScore = int(math.Max(0, math.Min(100, float64(medianScore))))

	featureContrib := make(map[string]float64)
	nTrees := float64(len(ts.trees))
	for k, v := range contribSum {
		featureContrib[k] = v / nTrees
	}

	riskLevel := "low"
	switch {
	case medianScore >= 80:
		riskLevel = "critical"
	case medianScore >= 60:
		riskLevel = "high"
	case medianScore >= 30:
		riskLevel = "medium"
	}

	adverseProb := float64(medianScore) / 100.0 * 0.4
	recommendations := ts.buildRecommendations(req, features)

	now := time.Now()
	result := &models.TransportRiskResult{
		ID:               0,
		RequestID:        req.RequestID,
		BedID:            req.BedID,
		RiskScore:        medianScore,
		RiskLevel:        riskLevel,
		AdverseEventProb: adverseProb,
		FeatureContrib:   featureContrib,
		Recommendations:  recommendations,
		Time:             now,
		Timestamp:        now,
	}

	ts.mu.Lock()
	ts.LatestResults[req.RequestID] = result
	ts.mu.Unlock()

	return result, nil
}

func (ts *TransportScorer) buildFeatures(req models.TransportRequest) []float64 {
	features := make([]float64, 6)

	vitalStability := 75.0
	if ts.vitalStabilityProvider != nil {
		vitalStability = ts.vitalStabilityProvider.GetVitalStabilityScore(req.BedID)
	}
	if vitalStability < 0 {
		vitalStability = 0
	}
	if vitalStability > 100 {
		vitalStability = 100
	}
	features[0] = vitalStability

	infectionRisk := 0.3
	if ts.infectionProvider != nil {
		infectionRisk = ts.infectionProvider.GetInfectionRisk(req.BedID)
	}
	if infectionRisk < 0 {
		infectionRisk = 0
	}
	if infectionRisk > 1 {
		infectionRisk = 1
	}
	features[1] = infectionRisk

	distanceNorm := req.Distance / 5000.0
	if distanceNorm > 1 {
		distanceNorm = 1
	}
	if distanceNorm < 0 {
		distanceNorm = 0
	}
	features[2] = distanceNorm

	if req.Urgent {
		features[3] = 1.0
	} else {
		features[3] = 0.0
	}

	switch req.Priority {
	case 1:
		features[4] = 0.33
	case 2:
		features[4] = 0.66
	case 3:
		features[4] = 1.0
	default:
		features[4] = 0.33
	}

	hour := req.HourOfDay
	if hour < 0 {
		hour = 0
	}
	if hour > 23 {
		hour = 23
	}
	features[5] = float64(hour) / 24.0

	return features
}

func (ts *TransportScorer) buildRecommendations(req models.TransportRequest, features []float64) []string {
	var recs []string

	if features[0] < 60 {
		recs = append(recs, "先稳定生命体征")
	}
	if features[1] > 0.7 {
		recs = append(recs, "加强防护装备")
	}
	if req.Distance > 1000 {
		recs = append(recs, "选择最短路径")
	}
	if !req.Urgent {
		recs = append(recs, "建议错峰转运")
	}

	if len(recs) == 0 {
		recs = append(recs, "按标准流程转运")
	}

	return recs
}

func (ts *TransportScorer) traverseTree(node *TreeNode, features []float64, contrib map[string]float64) (int, map[string]float64) {
	if node.IsLeaf {
		for k, v := range node.LeafContrib {
			contrib[k] = v
		}
		return node.LeafValue, contrib
	}

	featVal := features[node.FeatureIndex]
	if featVal <= node.Threshold {
		return ts.traverseTree(node.Left, features, contrib)
	}
	return ts.traverseTree(node.Right, features, contrib)
}

func (ts *TransportScorer) GetResult(requestID uint32) *models.TransportRiskResult {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if res, ok := ts.LatestResults[requestID]; ok {
		return res
	}
	return nil
}

func (ts *TransportScorer) GetAllResults() map[uint32]*models.TransportRiskResult {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	results := make(map[uint32]*models.TransportRiskResult, len(ts.LatestResults))
	for k, v := range ts.LatestResults {
		results[k] = v
	}
	return results
}

func (ts *TransportScorer) SetProviders(
	vsp interface{ GetVitalStabilityScore(bedID uint32) float64 },
	ip interface{ GetInfectionRisk(bedID uint32) float64 },
) {
	ts.vitalStabilityProvider = vsp
	ts.infectionProvider = ip
}

func GetInstance() *TransportScorer {
	return Instance
}
