package infection_rf

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

type InfectionPrediction struct {
	BedID    int
	CRERisk  float64
	MRSARisk float64
	MaxRisk  float64
	Time     time.Time
}

type Predictor struct {
	InChan         chan models.VitalSign
	OutChan        chan InfectionPrediction
	cfg            config.MLConfig
	vitalCache     map[int]map[string]float64
	cacheMux       sync.RWMutex
	antibioticDays map[int]int
	invasiveCount  map[int]int
	statsMux       sync.RWMutex
	rfTreeWeights  []float64
	rfFeatureSets  [][]float64
	stopChan       chan struct{}
	wg             sync.WaitGroup
}

var Instance *Predictor

func NewPredictor(cfg config.MLConfig, inChan chan models.VitalSign, outChan chan InfectionPrediction) *Predictor {
	p := &Predictor{
		InChan:         inChan,
		OutChan:        outChan,
		cfg:            cfg,
		vitalCache:     make(map[int]map[string]float64),
		antibioticDays: make(map[int]int),
		invasiveCount:  make(map[int]int),
		stopChan:       make(chan struct{}),
	}

	for i := 1; i <= cfg.NumOfBeds; i++ {
		p.vitalCache[i] = map[string]float64{
			"ecg":         0,
			"ventilator":  0,
			"spo2":        0,
			"temperature": 0,
		}
	}

	p.initRandomForest()

	return p
}

func (p *Predictor) initRandomForest() {
	nTrees := p.cfg.RandomForest.NumTrees
	p.rfTreeWeights = make([]float64, nTrees)
	for i := range p.rfTreeWeights {
		p.rfTreeWeights[i] = p.cfg.RandomForest.TreeWeightMin +
			rand.Float64()*(p.cfg.RandomForest.TreeWeightMax-p.cfg.RandomForest.TreeWeightMin)
	}

	if len(p.cfg.RandomForest.FeatureWeightSets) > 0 {
		p.rfFeatureSets = p.cfg.RandomForest.FeatureWeightSets
	} else {
		p.rfFeatureSets = [][]float64{
			{0.25, 0.30, 0.15, 0.20, 0.10},
			{0.20, 0.35, 0.10, 0.25, 0.10},
			{0.30, 0.25, 0.20, 0.15, 0.10},
		}
	}
}

func (p *Predictor) Start() {
	p.wg.Add(2)

	go p.vitalSignLoop()
	go p.predictionLoop()
}

func (p *Predictor) vitalSignLoop() {
	defer p.wg.Done()

	for {
		select {
		case <-p.stopChan:
			return
		case vs, ok := <-p.InChan:
			if !ok {
				return
			}
			p.cacheMux.Lock()
			if _, bedExists := p.vitalCache[vs.BedID]; !bedExists {
				p.vitalCache[vs.BedID] = make(map[string]float64)
			}
			p.vitalCache[vs.BedID][vs.SensorType] = vs.Value
			p.cacheMux.Unlock()
		}
	}
}

func (p *Predictor) predictionLoop() {
	defer p.wg.Done()

	interval := time.Duration(p.cfg.PredictionInterval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			results := p.PredictAll()
			for _, pred := range results {
				select {
				case p.OutChan <- pred:
				default:
				}
			}
		}
	}
}

func (p *Predictor) Stop() {
	close(p.stopChan)
	p.wg.Wait()
}

func (p *Predictor) SetAntibioticDays(bedID, days int) {
	p.statsMux.Lock()
	defer p.statsMux.Unlock()
	p.antibioticDays[bedID] = days
}

func (p *Predictor) SetInvasiveCount(bedID, count int) {
	p.statsMux.Lock()
	defer p.statsMux.Unlock()
	p.invasiveCount[bedID] = count
}

func (p *Predictor) getAntibioticDays(bedID int) int {
	p.statsMux.RLock()
	defer p.statsMux.RUnlock()
	if days, ok := p.antibioticDays[bedID]; ok {
		return days
	}
	return 0
}

func (p *Predictor) getInvasiveCount(bedID int) int {
	p.statsMux.RLock()
	defer p.statsMux.RUnlock()
	if count, ok := p.invasiveCount[bedID]; ok {
		return count
	}
	return 0
}

func (p *Predictor) PredictAll() map[int]InfectionPrediction {
	results := make(map[int]InfectionPrediction)
	now := time.Now()

	for bedID := 1; bedID <= p.cfg.NumOfBeds; bedID++ {
		creRisk := p.PredictCRE(bedID)
		mrsaRisk := p.PredictMRSA(bedID)
		maxRisk := creRisk
		if mrsaRisk > maxRisk {
			maxRisk = mrsaRisk
		}

		results[bedID] = InfectionPrediction{
			BedID:    bedID,
			CRERisk:  creRisk,
			MRSARisk: mrsaRisk,
			MaxRisk:  maxRisk,
			Time:     now,
		}
	}

	return results
}

func (p *Predictor) PredictCRE(bedID int) float64 {
	features := p.getInfectionFeatures(bedID)
	prob := p.randomForestPredict(features, 0)

	antibiotics := p.getAntibioticDays(bedID)
	invasive := p.getInvasiveCount(bedID)

	prob += float64(antibiotics) * p.cfg.RandomForest.CRERiskAntibioticCoef
	prob += float64(invasive) * p.cfg.RandomForest.CRERiskInvasiveCoef
	prob += rand.Float64() * p.cfg.RandomForest.CRERiskNoise

	return math.Max(0, math.Min(1, prob))
}

func (p *Predictor) PredictMRSA(bedID int) float64 {
	features := p.getInfectionFeatures(bedID)
	prob := p.randomForestPredict(features, 1)

	antibiotics := p.getAntibioticDays(bedID)
	invasive := p.getInvasiveCount(bedID)

	prob += float64(antibiotics) * p.cfg.RandomForest.MRSARiskAntibioticCoef
	prob += float64(invasive) * p.cfg.RandomForest.MRSARiskInvasiveCoef
	prob += rand.Float64() * p.cfg.RandomForest.MRSARiskNoise

	return math.Max(0, math.Min(1, prob))
}

func (p *Predictor) getInfectionFeatures(bedID int) []float64 {
	antibiotics := float64(p.getAntibioticDays(bedID))
	invasive := float64(p.getInvasiveCount(bedID))
	temp := 0.0
	vent := 0.0

	p.cacheMux.RLock()
	if bedMap, bedOk := p.vitalCache[bedID]; bedOk {
		if val, ok := bedMap["temperature"]; ok {
			temp = val
		}
		if val, ok := bedMap["ventilator"]; ok {
			vent = val
		}
	}
	p.cacheMux.RUnlock()

	admissionDays := rand.Float64() * 14

	return []float64{antibiotics, invasive, temp, vent, admissionDays}
}

func (p *Predictor) randomForestPredict(features []float64, treeSet int) float64 {
	nTrees := len(p.rfTreeWeights)
	predictions := make([]float64, nTrees)

	featureIdx := treeSet % len(p.rfFeatureSets)
	weights := p.rfFeatureSets[featureIdx]

	for t := 0; t < nTrees; t++ {
		votes := 0.0
		for i, f := range features {
			w := weights[i%len(weights)]
			threshold := 0.3 + rand.Float64()*0.4
			if f > threshold {
				votes += w
			} else {
				votes -= w * 0.5
			}
		}
		predictions[t] = sigmoid(votes + p.rfTreeWeights[t]*0.1)
	}

	sort.Float64s(predictions)
	median := predictions[nTrees/2]

	return median
}

func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}
