package cox_vap

import (
	"math"
	"sync"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

const (
	maxHistoryPoints = 288
	hoursPerPoint    = 5.0 / 60.0
)

type CoxPredictor struct {
	config          config.CoxVapConfig
	InChan          <-chan models.VentilatorParam
	OutChan         chan<- models.VapRiskRecord
	BedBuffers      map[uint32]*models.VapRiskRecord
	bedParamHistory map[uint32][]models.VentilatorParam
	coxCoefficients map[string]float64
	baselineHazard  float64
	mu              sync.RWMutex
	stopChan        chan struct{}
	wg              sync.WaitGroup
}

var Instance *CoxPredictor

func NewCoxPredictor(cfg config.CoxVapConfig, in <-chan models.VentilatorParam, out chan<- models.VapRiskRecord) *CoxPredictor {
	p := &CoxPredictor{
		config:          cfg,
		InChan:          in,
		OutChan:         out,
		BedBuffers:      make(map[uint32]*models.VapRiskRecord),
		bedParamHistory: make(map[uint32][]models.VentilatorParam),
		coxCoefficients: map[string]float64{
			"peak_pressure":          0.02,
			"tidal_volume_deviation": 0.015,
			"oral_secretion":         0.08,
			"ventilator_hours":       0.005,
			"prior_infection":        0.12,
		},
		baselineHazard: 0.0003,
		stopChan:       make(chan struct{}),
	}
	Instance = p
	return p
}

func (p *CoxPredictor) Start() {
	go p.Run()
}

func (p *CoxPredictor) Stop() {
	close(p.stopChan)
	p.wg.Wait()
}

func (p *CoxPredictor) Run() {
	p.wg.Add(2)

	go p.paramLoop()
	go p.updateLoop()
}

func (p *CoxPredictor) paramLoop() {
	defer p.wg.Done()
	for {
		select {
		case <-p.stopChan:
			return
		case param, ok := <-p.InChan:
			if !ok {
				return
			}
			p.cacheParam(param)
		}
	}
}

func (p *CoxPredictor) updateLoop() {
	defer p.wg.Done()
	interval := time.Duration(p.config.UpdateIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			p.EvaluateAllBeds()
		}
	}
}

func (p *CoxPredictor) cacheParam(param models.VentilatorParam) {
	p.mu.Lock()
	defer p.mu.Unlock()

	bedID := param.BedID
	if _, ok := p.bedParamHistory[bedID]; !ok {
		p.bedParamHistory[bedID] = make([]models.VentilatorParam, 0, maxHistoryPoints)
	}
	p.bedParamHistory[bedID] = append(p.bedParamHistory[bedID], param)
	if len(p.bedParamHistory[bedID]) > maxHistoryPoints {
		p.bedParamHistory[bedID] = p.bedParamHistory[bedID][len(p.bedParamHistory[bedID])-maxHistoryPoints:]
	}
}

func (p *CoxPredictor) EvaluateAllBeds() {
	p.mu.RLock()
	beds := make([]uint32, 0, len(p.bedParamHistory))
	for bedID := range p.bedParamHistory {
		beds = append(beds, bedID)
	}
	p.mu.RUnlock()

	for _, bedID := range beds {
		record, err := p.EvaluateBed(bedID)
		if err != nil {
			continue
		}
		select {
		case p.OutChan <- *record:
		default:
		}
	}
}

func (p *CoxPredictor) EvaluateBed(bedID uint32) (*models.VapRiskRecord, error) {
	p.mu.RLock()
	history, ok := p.bedParamHistory[bedID]
	p.mu.RUnlock()

	if !ok || len(history) == 0 {
		return nil, nil
	}

	var (
		sumPeakPressure    float64
		sumTidalDeviation  float64
		sumOralSecretion   float64
		maxVentilatorHours float64
		avgPriorInfection  float64
	)

	idealTidalVolume := 8.0
	for _, param := range history {
		sumPeakPressure += param.PeakPressure
		actualTidalPerKg := param.TidalVolume
		if param.PredictedWeight > 0 {
			actualTidalPerKg = param.TidalVolume / param.PredictedWeight
		}
		sumTidalDeviation += math.Abs(actualTidalPerKg - idealTidalVolume)
		sumOralSecretion += param.OralSecretion
		if param.VentilatorHours > maxVentilatorHours {
			maxVentilatorHours = param.VentilatorHours
		}
		avgPriorInfection += param.PriorInfection
	}

	n := float64(len(history))
	features := map[string]float64{
		"peak_pressure":          sumPeakPressure / n,
		"tidal_volume_deviation": sumTidalDeviation / n,
		"oral_secretion":         sumOralSecretion / n,
		"ventilator_hours":       maxVentilatorHours,
		"prior_infection":        avgPriorInfection / n,
	}

	linearPredictor := 0.0
	featureContribs := make(map[string]float64)
	for name, coeff := range p.coxCoefficients {
		contrib := coeff * features[name]
		linearPredictor += contrib
		featureContribs[name] = contrib
	}

	hazardsRatio := math.Exp(linearPredictor)
	hoursInWindow := float64(len(history)) * hoursPerPoint
	risk := 1.0 - math.Exp(-p.baselineHazard*hazardsRatio*hoursInWindow)
	if risk < 0 {
		risk = 0
	}
	if risk > 1 {
		risk = 1
	}

	var predictedOnset float64
	denominator := -p.baselineHazard * hazardsRatio
	if denominator != 0 {
		predictedOnset = math.Log(0.5) / denominator
	}
	if predictedOnset < 0 || math.IsInf(predictedOnset, 0) || math.IsNaN(predictedOnset) {
		predictedOnset = -1
	}

	totalAbsContrib := 0.0
	for _, v := range featureContribs {
		totalAbsContrib += math.Abs(v)
	}
	featureWeights := make(map[string]float64)
	if totalAbsContrib > 0 {
		for name, v := range featureContribs {
			featureWeights[name] = (math.Abs(v) / totalAbsContrib) * 100
		}
	} else {
		for name := range featureContribs {
			featureWeights[name] = 0
		}
	}

	now := time.Now()
	record := &models.VapRiskRecord{
		ID:                  0,
		BedID:               bedID,
		Time:                now,
		Timestamp:           now,
		RiskProbability:     risk,
		RiskProb:            risk,
		HazardsRatio:        hazardsRatio,
		PredictedOnsetHours: predictedOnset,
		PredictedOnset:      predictedOnset,
		FeatureWeights:      featureWeights,
		Features:            features,
	}

	p.mu.Lock()
	p.BedBuffers[bedID] = record
	p.mu.Unlock()

	return record, nil
}

func (p *CoxPredictor) GetLatestByBed(bedID uint32) *models.VapRiskRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if record, ok := p.BedBuffers[bedID]; ok {
		return record
	}
	return nil
}

func (p *CoxPredictor) GetAllLatest() map[uint32]*models.VapRiskRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[uint32]*models.VapRiskRecord, len(p.BedBuffers))
	for k, v := range p.BedBuffers {
		result[k] = v
	}
	return result
}

func GetInstance() *CoxPredictor {
	return Instance
}
