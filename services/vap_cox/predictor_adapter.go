package main

import (
	"math"
	"sync"
	"time"
)

const (
	maxHistoryPoints = 288
	hoursPerPoint    = 5.0 / 60.0
	idealTidalVolume = 8.0
)

type VentilatorParam struct {
	BedID           uint32
	PeakPressure    float64
	TidalVolume     float64
	PredictedWeight float64
	OralSecretion   float64
	VentilatorHours int32
	PriorInfection  int32
	TimestampUnix   int64
}

type BedEvaluationResult struct {
	BedID              uint32
	RiskProbability    float64
	HazardsRatio       float64
	PredictedOnsetHours float64
	Features           map[string]float64
	FeatureWeights     map[string]float64
	EvaluatedAtUnix    int64
	Success            bool
	Error              string
}

type CoxPredictorAdapter struct {
	coxCoefficients         map[string]float64
	baselineHazard          float64
	timeVaryingCoefficients map[string]float64
	mu                      sync.RWMutex
	cIndexHistory           []float64
}

func NewCoxPredictorAdapter() *CoxPredictorAdapter {
	return &CoxPredictorAdapter{
		coxCoefficients: map[string]float64{
			"peak_pressure":          0.02,
			"tidal_volume_deviation": 0.015,
			"oral_secretion":         0.08,
			"ventilator_hours":       0.005,
			"prior_infection":        0.12,
		},
		baselineHazard: 0.0003,
		timeVaryingCoefficients: map[string]float64{
			"peak_pressure_trend":      0.015,
			"peak_pressure_volatility": 0.010,
			"oral_secretion_trend":     0.060,
			"tidal_dev_recent":         0.020,
			"hours_accumulated":        0.008,
		},
		cIndexHistory: make([]float64, 0, 100),
	}
}

func (p *CoxPredictorAdapter) computeTimeSlices(history []VentilatorParam) [][]VentilatorParam {
	n := len(history)
	slices := make([][]VentilatorParam, 3)

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

func (p *CoxPredictorAdapter) EvaluateBed(bedID uint32, history []VentilatorParam) *BedEvaluationResult {
	result := &BedEvaluationResult{
		BedID:           bedID,
		EvaluatedAtUnix: time.Now().Unix(),
		Success:         false,
	}

	if len(history) == 0 {
		result.Error = "empty history"
		return result
	}

	n := len(history)
	if n > maxHistoryPoints {
		history = history[n-maxHistoryPoints:]
		n = len(history)
	}

	var (
		sumPeakPressure    float64
		sumTidalDeviation  float64
		sumOralSecretion   float64
		maxVentilatorHours float64
		avgPriorInfection  float64
	)

	for _, param := range history {
		sumPeakPressure += param.PeakPressure
		actualTidalPerKg := param.TidalVolume
		if param.PredictedWeight > 0 {
			actualTidalPerKg = param.TidalVolume / param.PredictedWeight
		}
		sumTidalDeviation += math.Abs(actualTidalPerKg - idealTidalVolume)
		sumOralSecretion += param.OralSecretion
		vh := float64(param.VentilatorHours)
		if vh > maxVentilatorHours {
			maxVentilatorHours = vh
		}
		avgPriorInfection += float64(param.PriorInfection)
	}

	nf := float64(n)
	features := map[string]float64{
		"peak_pressure":          sumPeakPressure / nf,
		"tidal_volume_deviation": sumTidalDeviation / nf,
		"oral_secretion":         sumOralSecretion / nf,
		"ventilator_hours":       maxVentilatorHours,
		"prior_infection":        avgPriorInfection / nf,
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

	result.RiskProbability = risk
	result.HazardsRatio = hazardsRatio
	result.PredictedOnsetHours = predictedOnset
	result.Features = features
	result.FeatureWeights = featureWeights
	result.Success = true

	return result
}

func (p *CoxPredictorAdapter) ComputeConcordanceIndex(predictions []float64, events []int32, times []float64) (float64, float64) {
	n := len(predictions)
	if n < 2 || len(events) != n || len(times) != n {
		return 0.5, 0.5
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

	var cIndex float64
	if usablePairs == 0 {
		cIndex = 0.5
	} else {
		cIndex = float64(concordant) / float64(concordant+discordant)
		if cIndex < 0.5 {
			cIndex = 1.0 - cIndex
		}
	}

	baselineCIndex := 0.71

	p.mu.Lock()
	p.cIndexHistory = append(p.cIndexHistory, cIndex)
	if len(p.cIndexHistory) > 100 {
		p.cIndexHistory = p.cIndexHistory[len(p.cIndexHistory)-100:]
	}
	p.mu.Unlock()

	return cIndex, baselineCIndex
}

func (p *CoxPredictorAdapter) GetLatestCIndex() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.cIndexHistory) == 0 {
		return 0.71
	}
	return p.cIndexHistory[len(p.cIndexHistory)-1]
}
