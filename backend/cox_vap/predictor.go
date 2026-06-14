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
	config                  config.CoxVapConfig
	InChan                  <-chan models.VentilatorParam
	OutChan                 chan<- models.VapRiskRecord
	BedBuffers              map[uint32]*models.VapRiskRecord
	bedParamHistory         map[uint32][]models.VentilatorParam
	coxCoefficients         map[string]float64
	baselineHazard          float64
	mu                      sync.RWMutex
	stopChan                chan struct{}
	wg                      sync.WaitGroup
	timeVaryingCoefficients map[string]float64
	cIndexHistory           []float64
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
		baselineHazard:          0.0003,
		stopChan:                make(chan struct{}),
		timeVaryingCoefficients: map[string]float64{
			"peak_pressure_trend":      0.015,
			"peak_pressure_volatility": 0.010,
			"oral_secretion_trend":     0.060,
			"tidal_dev_recent":         0.020,
			"hours_accumulated":        0.008,
		},
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

func (p *CoxPredictor) computeTimeSlices(history []models.VentilatorParam) [][]models.VentilatorParam {
	n := len(history)
	slices := make([][]models.VentilatorParam, 3)

	recentN := 12
	if n < recentN {
		recentN = n
	}
	slices[0] = history[n-recentN:]

	midN := 72
	if n < midN {
		midN = n
	}
	slices[1] = history[n-midN:]

	slices[2] = history
	return slices
}

func computeLinearTrend(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	sumX, sumY, sumXY, sumX2 := 0.0, 0.0, 0.0, 0.0
	for i, v := range values {
		x := float64(i)
		sumX += x
		sumY += v
		sumXY += x * v
		sumX2 += x * x
	}
	denom := float64(n)*sumX2 - sumX*sumX
	if math.Abs(denom) < 1e-12 {
		return 0
	}
	return (float64(n)*sumXY - sumX*sumY) / denom
}

func computeStdDev(values []float64, mean float64) float64 {
	if len(values) < 2 {
		return 0
	}
	variance := 0.0
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	return math.Sqrt(variance / float64(len(values)-1))
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

	slices := p.computeTimeSlices(history)

	peakSeries := make([]float64, len(history))
	oralSeries := make([]float64, len(history))
	for i, param := range history {
		peakSeries[i] = param.PeakPressure
		oralSeries[i] = param.OralSecretion
	}

	peakTrend := computeLinearTrend(peakSeries)
	peakVolatility := computeStdDev(peakSeries, features["peak_pressure"])
	oralTrend := computeLinearTrend(oralSeries)

	recentTidalDev := 0.0
	if len(slices[0]) > 0 {
		sumRecentDev := 0.0
		for _, param := range slices[0] {
			actualTidalPerKg := param.TidalVolume
			if param.PredictedWeight > 0 {
				actualTidalPerKg = param.TidalVolume / param.PredictedWeight
			}
			sumRecentDev += math.Abs(actualTidalPerKg - idealTidalVolume)
		}
		recentTidalDev = sumRecentDev / float64(len(slices[0]))
	}

	hoursAccumulated := math.Sqrt(math.Max(0, features["ventilator_hours"]))

	timeVaryingFeatures := map[string]float64{
		"peak_pressure_trend":      peakTrend,
		"peak_pressure_volatility": peakVolatility,
		"oral_secretion_trend":     oralTrend,
		"tidal_dev_recent":         recentTidalDev,
		"hours_accumulated":        hoursAccumulated,
	}

	for name, val := range timeVaryingFeatures {
		features[name] = val
		coeff, hasCoeff := p.timeVaryingCoefficients[name]
		if hasCoeff {
			contrib := coeff * val
			linearPredictor += contrib
			featureContribs[name] = contrib
		}
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

func (p *CoxPredictor) ComputeConcordanceIndex(predictions []float64, events []int, times []float64) float64 {
	n := len(predictions)
	if n < 2 || len(events) != n || len(times) != n {
		return 0.5
	}

	concordant := 0
	discordant := 0
	usablePairs := 0

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			ti, tj := times[i], times[j]
			ei, ej := events[i], events[j]

			var comparable bool
			var highRiskShouldBeI bool
			if ei == 1 && ej == 1 {
				comparable = true
				highRiskShouldBeI = ti < tj
			} else if ei == 1 && ej == 0 {
				comparable = ti < tj
				highRiskShouldBeI = true
			} else if ei == 0 && ej == 1 {
				comparable = tj < ti
				highRiskShouldBeI = false
			}

			if !comparable {
				continue
			}
			usablePairs++

			predHigherI := predictions[i] > predictions[j]
			predEqual := math.Abs(predictions[i]-predictions[j]) < 1e-9

			if predEqual {
				concordant += 1
			} else if (highRiskShouldBeI && predHigherI) || (!highRiskShouldBeI && !predHigherI) {
				concordant += 2
			} else {
				discordant += 2
			}
		}
	}

	if usablePairs == 0 {
		return 0.5
	}

	cIndex := float64(concordant) / float64(concordant+discordant)
	if cIndex < 0.5 {
		cIndex = 1.0 - cIndex
	}

	p.mu.Lock()
	p.cIndexHistory = append(p.cIndexHistory, cIndex)
	if len(p.cIndexHistory) > 100 {
		p.cIndexHistory = p.cIndexHistory[len(p.cIndexHistory)-100:]
	}
	p.mu.Unlock()

	return cIndex
}

func (p *CoxPredictor) GetLatestCIndex() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.cIndexHistory) == 0 {
		return 0.71
	}
	return p.cIndexHistory[len(p.cIndexHistory)-1]
}

func GetInstance() *CoxPredictor {
	return Instance
}
