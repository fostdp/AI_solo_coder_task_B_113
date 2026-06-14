package main

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"
)

type ScoreRequest struct {
	RequestID       uint32  `json:"request_id"`
	BedID           uint32  `json:"bed_id"`
	FromBedID       uint32  `json:"from_bed_id"`
	ToBedID         uint32  `json:"to_bed_id"`
	Distance        float64 `json:"distance"`
	Urgent          bool    `json:"urgent"`
	Priority        int32   `json:"priority"`
	HourOfDay       int32   `json:"hour_of_day"`
	PatientAge      int32   `json:"patient_age"`
	VitalStability  float64 `json:"vital_stability"`
	InfectionRisk   float64 `json:"infection_risk"`
}

type ScoreResponse struct {
	RequestID        uint32            `json:"request_id"`
	RiskScore        int32             `json:"risk_score"`
	RiskLevel        string            `json:"risk_level"`
	AdverseEventProb float64           `json:"adverse_event_prob"`
	FeatureContrib   map[string]float64 `json:"feature_contrib"`
	Recommendations  []string          `json:"recommendations"`
	UsedDefaultDist  bool              `json:"used_default_distance"`
	UsedDefaultVitals bool             `json:"used_default_vitals"`
	ScoredAtUnix     int64             `json:"scored_at_unix"`
	Success          bool              `json:"success"`
	Error            string            `json:"error,omitempty"`
}

type StatsRequest struct{}

type StatsResponse struct {
	TotalScores int64            `json:"total_scores"`
	AvgScore    float64          `json:"avg_score"`
	LevelCounts map[string]int64 `json:"level_counts"`
}

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

const (
	DefaultDistance      = 1000.0
	DefaultVitalStability = 75.0
	DefaultInfectionRisk  = 0.3
)

type ScorerStats struct {
	mu          sync.RWMutex
	totalScores int64
	sumScores   int64
	levelCounts map[string]int64
}

func NewScorerStats() *ScorerStats {
	return &ScorerStats{
		levelCounts: map[string]int64{
			"low":      0,
			"medium":   0,
			"high":     0,
			"critical": 0,
		},
	}
}

func (s *ScorerStats) Record(score int32, level string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalScores++
	s.sumScores += int64(score)
	s.levelCounts[level]++
}

func (s *ScorerStats) GetStats() *StatsResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	avg := 0.0
	if s.totalScores > 0 {
		avg = float64(s.sumScores) / float64(s.totalScores)
	}
	levelCounts := make(map[string]int64, len(s.levelCounts))
	for k, v := range s.levelCounts {
		levelCounts[k] = v
	}
	return &StatsResponse{
		TotalScores: s.totalScores,
		AvgScore:    avg,
		LevelCounts: levelCounts,
	}
}

type ScorerAdapter struct {
	trees []*DecisionTree
	stats *ScorerStats
}

func NewScorerAdapter(nTrees, maxDepth int) *ScorerAdapter {
	if nTrees <= 0 {
		nTrees = 50
	}
	if maxDepth <= 0 {
		maxDepth = 3
	}
	trees := make([]*DecisionTree, nTrees)
	for i := 0; i < nTrees; i++ {
		trees[i] = buildDecisionTree(maxDepth)
	}
	return &ScorerAdapter{
		trees: trees,
		stats: NewScorerStats(),
	}
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

func (a *ScorerAdapter) resolveDistance(req ScoreRequest) (dist float64, usedDefault bool) {
	if req.Distance <= 0 || math.IsNaN(req.Distance) {
		return DefaultDistance, true
	}
	return req.Distance, false
}

func (a *ScorerAdapter) resolveVitals(req ScoreRequest) (vital float64, infection float64, usedDefault bool) {
	usedDefault = false
	vital = req.VitalStability
	if vital < 0 || math.IsNaN(vital) {
		vital = DefaultVitalStability
		usedDefault = true
	}
	infection = req.InfectionRisk
	if infection < 0 || math.IsNaN(infection) {
		infection = DefaultInfectionRisk
		usedDefault = true
	}
	return vital, infection, usedDefault
}

func (a *ScorerAdapter) buildFeatures(req ScoreRequest) ([]float64, float64, bool, bool) {
	features := make([]float64, 6)

	distance, usedDefaultDist := a.resolveDistance(req)
	vitalStability, infectionRisk, usedDefaultVitals := a.resolveVitals(req)

	vitalStability = math.Max(0, math.Min(100, vitalStability))
	features[0] = vitalStability

	infectionRisk = math.Max(0, math.Min(1, infectionRisk))
	features[1] = infectionRisk

	distanceNorm := distance / 5000.0
	distanceNorm = math.Max(0, math.Min(1, distanceNorm))
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

	return features, distance, usedDefaultDist, usedDefaultVitals
}

func (a *ScorerAdapter) buildRecommendations(req ScoreRequest, distance float64, features []float64) []string {
	var recs []string

	if features[0] < 60 {
		recs = append(recs, "先稳定生命体征")
	}
	if features[1] > 0.7 {
		recs = append(recs, "加强防护装备")
	}
	if distance > 1000 {
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

func (a *ScorerAdapter) traverseTree(node *TreeNode, features []float64, contrib map[string]float64) (int, map[string]float64) {
	if node.IsLeaf {
		for k, v := range node.LeafContrib {
			contrib[k] = v
		}
		return node.LeafValue, contrib
	}

	featVal := features[node.FeatureIndex]
	if featVal <= node.Threshold {
		return a.traverseTree(node.Left, features, contrib)
	}
	return a.traverseTree(node.Right, features, contrib)
}

func (a *ScorerAdapter) Score(req ScoreRequest) *ScoreResponse {
	features, distance, usedDefaultDist, usedDefaultVitals := a.buildFeatures(req)

	scores := make([]int, len(a.trees))
	contribSum := make(map[string]float64)
	for _, name := range featureNames {
		contribSum[name] = 0.0
	}

	for i, tree := range a.trees {
		contrib := make(map[string]float64)
		score, treeContrib := a.traverseTree(tree.Root, features, contrib)
		scores[i] = score
		for k, v := range treeContrib {
			contribSum[k] += v
		}
	}

	sort.Ints(scores)
	medianScore := scores[len(scores)/2]
	medianScore = int(math.Max(0, math.Min(100, float64(medianScore))))

	featureContrib := make(map[string]float64)
	nTrees := float64(len(a.trees))
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
	recommendations := a.buildRecommendations(req, distance, features)

	a.stats.Record(int32(medianScore), riskLevel)

	return &ScoreResponse{
		RequestID:        req.RequestID,
		RiskScore:        int32(medianScore),
		RiskLevel:        riskLevel,
		AdverseEventProb: adverseProb,
		FeatureContrib:   featureContrib,
		Recommendations:  recommendations,
		UsedDefaultDist:  usedDefaultDist,
		UsedDefaultVitals: usedDefaultVitals,
		ScoredAtUnix:     time.Now().Unix(),
		Success:          true,
	}
}

func (a *ScorerAdapter) ScoreBatch(requests []ScoreRequest) []*ScoreResponse {
	responses := make([]*ScoreResponse, len(requests))
	for i, req := range requests {
		responses[i] = a.Score(req)
	}
	return responses
}

func (a *ScorerAdapter) GetStats() *StatsResponse {
	return a.stats.GetStats()
}
