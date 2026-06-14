package cox_vap

import (
	"math"
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

func newPredictor() *CoxPredictor {
	return NewCoxPredictor(
		config.CoxVapConfig{UpdateIntervalSec: 1, RiskThreshold: 0.5},
		make(chan models.VentilatorParam, 1000),
		make(chan models.VapRiskRecord, 50),
	)
}

func makeParam(bedID uint32, peakPressure, tidalVolume, predictedWeight, oralSecretion float64, ventilatorHours int, priorInfection float64) models.VentilatorParam {
	return models.VentilatorParam{
		BedID:           bedID,
		PeakPressure:    peakPressure,
		TidalVolume:     tidalVolume,
		PredictedWeight: predictedWeight,
		OralSecretion:   oralSecretion,
		VentilatorHours: ventilatorHours,
		PriorInfection:  priorInfection,
		Time:            time.Now(),
	}
}

func cacheN(p *CoxPredictor, param models.VentilatorParam, n int) {
	for i := 0; i < n; i++ {
		p.cacheParam(param)
	}
}

func TestNormal_HighPeakPressureAndHeavySecretion_RiskAccumulation(t *testing.T) {
	p := newPredictor()

	paramA := makeParam(1, 45, 560, 70, 5, 168, 2)
	paramB := makeParam(2, 15, 560, 70, 0, 1, 0)

	cacheN(p, paramA, 10)
	cacheN(p, paramB, 10)

	recA, errA := p.EvaluateBed(1)
	recB, errB := p.EvaluateBed(2)

	if errA != nil {
		t.Fatalf("EvaluateBed(1) error: %v", errA)
	}
	if errB != nil {
		t.Fatalf("EvaluateBed(2) error: %v", errB)
	}
	if recA == nil || recB == nil {
		t.Fatal("EvaluateBed returned nil record")
	}

	if !(recA.RiskProbability > recB.RiskProbability) {
		t.Errorf("A.RiskProbability(%v) should be strictly > B.RiskProbability(%v)", recA.RiskProbability, recB.RiskProbability)
	}
	if !(recA.HazardsRatio > recB.HazardsRatio) {
		t.Errorf("A.HazardsRatio(%v) should be strictly > B.HazardsRatio(%v)", recA.HazardsRatio, recB.HazardsRatio)
	}

	oralContrib := recA.FeatureWeights["oral_secretion"]
	peakContrib := recA.FeatureWeights["peak_pressure"]
	ventContrib := recA.FeatureWeights["ventilator_hours"]
	priorContrib := recA.FeatureWeights["prior_infection"]

	if peakContrib < ventContrib {
		t.Errorf("peak_pressure(%.2f%%) should be >= ventilator_hours(%.2f%%) as top contributor", peakContrib, ventContrib)
	}
	if oralContrib <= priorContrib {
		t.Errorf("oral_secretion(%.2f%%) should be > prior_infection(%.2f%%)", oralContrib, priorContrib)
	}
	combined := oralContrib + peakContrib
	if combined < 40 {
		t.Errorf("oral_secretion(%.2f%%) + peak_pressure(%.2f%%) = %.2f%%, should be significant (>=40%%)", oralContrib, peakContrib, combined)
	}
}

func TestNormal_RiskMonotonicallyIncreasesWithVentilatorHours(t *testing.T) {
	p := newPredictor()

	hours := []int{1, 24, 72, 168, 720}
	var risks []float64

	for i, vh := range hours {
		bedID := uint32(i + 1)
		param := makeParam(bedID, 25, 560, 70, 2, vh, 0.5)
		cacheN(p, param, 10)

		rec, err := p.EvaluateBed(bedID)
		if err != nil {
			t.Fatalf("EvaluateBed(%d) error: %v", bedID, err)
		}
		if rec == nil {
			t.Fatalf("EvaluateBed(%d) returned nil", bedID)
		}
		risks = append(risks, rec.RiskProbability)
	}

	for i := 1; i < len(risks); i++ {
		if !(risks[i] > risks[i-1]) {
			t.Errorf("Risk not monotonically increasing: hours[%d]=%d risk=%.6f <= hours[%d]=%d risk=%.6f",
				i-1, hours[i-1], risks[i-1], i, hours[i], risks[i])
		}
	}
}

func TestNormal_CoxHazardsRatioConsistentWithClinicalExpectation(t *testing.T) {
	p := newPredictor()

	highRiskParam := makeParam(1, 45, 700, 70, 4, 120, 2)
	cacheN(p, highRiskParam, 10)
	highRec, err := p.EvaluateBed(1)
	if err != nil {
		t.Fatalf("EvaluateBed high risk error: %v", err)
	}
	if highRec == nil {
		t.Fatal("high risk record is nil")
	}

	if !(highRec.HazardsRatio > 1.0) {
		t.Errorf("high risk HazardsRatio = %v, want > 1.0", highRec.HazardsRatio)
	}

	features := highRec.Features
	expectedLP := 0.02*features["peak_pressure"] +
		0.015*features["tidal_volume_deviation"] +
		0.08*features["oral_secretion"] +
		0.005*features["ventilator_hours"] +
		0.12*features["prior_infection"]
	expectedHR := math.Exp(expectedLP)
	if math.Abs(highRec.HazardsRatio-expectedHR) > 1e-9 {
		t.Errorf("high risk HR = %v, want exp(ΣβX) = %v", highRec.HazardsRatio, expectedHR)
	}

	lowRiskParam := makeParam(2, 15, 560, 70, 0.5, 12, 0)
	cacheN(p, lowRiskParam, 10)
	lowRec, err := p.EvaluateBed(2)
	if err != nil {
		t.Fatalf("EvaluateBed low risk error: %v", err)
	}
	if lowRec == nil {
		t.Fatal("low risk record is nil")
	}

	lowFeatures := lowRec.Features
	lowLP := 0.02*lowFeatures["peak_pressure"] +
		0.015*lowFeatures["tidal_volume_deviation"] +
		0.08*lowFeatures["oral_secretion"] +
		0.005*lowFeatures["ventilator_hours"] +
		0.12*lowFeatures["prior_infection"]
	lowExpectedHR := math.Exp(lowLP)
	if math.Abs(lowRec.HazardsRatio-lowExpectedHR) > 1e-9 {
		t.Errorf("low risk HR = %v, want exp(ΣβX) = %v", lowRec.HazardsRatio, lowExpectedHR)
	}
}

func TestNormal_FeatureWeightsReflectClinicalContribution(t *testing.T) {
	p := newPredictor()

	param := makeParam(1, 50, 560, 70, 0, 24, 0)
	cacheN(p, param, 10)

	rec, err := p.EvaluateBed(1)
	if err != nil {
		t.Fatalf("EvaluateBed error: %v", err)
	}
	if rec == nil {
		t.Fatal("record is nil")
	}

	peakWeight := rec.FeatureWeights["peak_pressure"]
	oralWeight := rec.FeatureWeights["oral_secretion"]

	if !(peakWeight > oralWeight) {
		t.Errorf("peak_pressure weight(%.4f) should be > oral_secretion weight(%.4f) when oral_secretion=0", peakWeight, oralWeight)
	}

	peakContrib := 0.02 * rec.Features["peak_pressure"]
	oralContrib := 0.08 * rec.Features["oral_secretion"]
	if math.Abs(oralContrib) > 1e-12 {
		t.Errorf("oral_secretion contribution should be ~0, got %.6f", oralContrib)
	}
	if peakContrib <= 0 {
		t.Errorf("peak_pressure contribution should be > 0, got %.6f", peakContrib)
	}
}

func TestBoundary_NoSecretion_BaselineRisk(t *testing.T) {
	p := newPredictor()

	param := makeParam(1, 20, 560, 70, 0, 24, 0)
	cacheN(p, param, 10)

	rec, err := p.EvaluateBed(1)
	if err != nil {
		t.Fatalf("EvaluateBed error: %v", err)
	}
	if rec == nil {
		t.Fatal("record is nil")
	}

	if !(rec.RiskProbability > 0) {
		t.Errorf("RiskProbability = %v, want > 0", rec.RiskProbability)
	}

	expectedHR := math.Exp(0.02*20 + 0.005*24)
	tolerance := 0.05
	if math.Abs(rec.HazardsRatio-expectedHR) > tolerance {
		t.Errorf("HazardsRatio = %v, want approximately %v (tolerance %v)", rec.HazardsRatio, expectedHR, tolerance)
	}
}

func TestBoundary_ZeroVentilatorHours(t *testing.T) {
	p := newPredictor()

	param := makeParam(1, 25, 560, 70, 2, 0, 0.5)
	cacheN(p, param, 10)

	rec, err := p.EvaluateBed(1)
	if err != nil {
		t.Fatalf("EvaluateBed error: %v", err)
	}
	if rec == nil {
		t.Fatal("record is nil")
	}

	if rec.RiskProbability < 0 || rec.RiskProbability > 1 {
		t.Errorf("RiskProbability = %v, want in [0,1]", rec.RiskProbability)
	}

	normalParam := makeParam(2, 25, 560, 70, 2, 72, 0.5)
	cacheN(p, normalParam, 10)
	normalRec, _ := p.EvaluateBed(2)
	if normalRec != nil && !(rec.RiskProbability < normalRec.RiskProbability) {
		t.Errorf("zero ventilator_hours risk(%v) should be lower than normal risk(%v)", rec.RiskProbability, normalRec.RiskProbability)
	}
}

func TestBoundary_SingleDataPoint(t *testing.T) {
	p := newPredictor()

	param := makeParam(1, 25, 560, 70, 2, 24, 0.5)
	p.cacheParam(param)

	rec, err := p.EvaluateBed(1)
	if err != nil {
		t.Fatalf("EvaluateBed error: %v", err)
	}
	if rec == nil {
		t.Fatal("record is nil")
	}

	if rec.RiskProbability < 0 || rec.RiskProbability > 1 {
		t.Errorf("RiskProbability = %v, want in [0,1]", rec.RiskProbability)
	}
	if math.IsNaN(rec.HazardsRatio) || math.IsInf(rec.HazardsRatio, 0) {
		t.Errorf("HazardsRatio = %v, want finite", rec.HazardsRatio)
	}
	for name, fw := range rec.FeatureWeights {
		if math.IsNaN(fw) || math.IsInf(fw, 0) {
			t.Errorf("FeatureWeights[%q] = %v, want finite", name, fw)
		}
	}
	for name, f := range rec.Features {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			t.Errorf("Features[%q] = %v, want finite", name, f)
		}
	}
}

func TestBoundary_AllNormalVitals_LowRisk(t *testing.T) {
	p := newPredictor()

	param := makeParam(1, 20, 560, 70, 0, 1, 0)
	cacheN(p, param, 10)

	rec, err := p.EvaluateBed(1)
	if err != nil {
		t.Fatalf("EvaluateBed error: %v", err)
	}
	if rec == nil {
		t.Fatal("record is nil")
	}

	if !(rec.RiskProbability < 0.01) {
		t.Errorf("all-normal risk = %v, want very low (<0.01)", rec.RiskProbability)
	}
	if !(rec.HazardsRatio < 2.0) {
		t.Errorf("all-normal HazardsRatio = %v, want < 2.0", rec.HazardsRatio)
	}
}

func TestAbnormal_MissingCovariate_PredictedWeightZero(t *testing.T) {
	p := newPredictor()

	paramZeroWeight := makeParam(1, 25, 560, 0, 2, 48, 0.5)
	paramNormalWeight := makeParam(2, 25, 560, 70, 2, 48, 0.5)

	cacheN(p, paramZeroWeight, 10)
	cacheN(p, paramNormalWeight, 10)

	recZero, errZero := p.EvaluateBed(1)
	recNormal, errNormal := p.EvaluateBed(2)

	if errZero != nil {
		t.Fatalf("EvaluateBed zero-weight error: %v", errZero)
	}
	if errNormal != nil {
		t.Fatalf("EvaluateBed normal-weight error: %v", errNormal)
	}
	if recZero == nil || recNormal == nil {
		t.Fatal("EvaluateBed returned nil record")
	}

	deviationZero := recZero.Features["tidal_volume_deviation"]
	deviationNormal := recNormal.Features["tidal_volume_deviation"]

	if !(math.Abs(deviationZero-552) < 1.0) {
		t.Errorf("zero-weight tidal_volume_deviation = %v, want ~552", deviationZero)
	}
	if !(math.Abs(deviationNormal) < 1e-9) {
		t.Errorf("normal-weight tidal_volume_deviation = %v, want ~0", deviationNormal)
	}

	if !(recZero.RiskProbability > recNormal.RiskProbability) {
		t.Errorf("zero-weight risk(%v) should be > normal-weight risk(%v)", recZero.RiskProbability, recNormal.RiskProbability)
	}
}

func TestAbnormal_EmptyHistory_ReturnsNil(t *testing.T) {
	p := newPredictor()

	rec, err := p.EvaluateBed(999)
	if err != nil {
		t.Errorf("EvaluateBed empty history error: %v", err)
	}
	if rec != nil {
		t.Errorf("EvaluateBed empty history = %v, want nil", rec)
	}
}

func TestAbnormal_ExtremePeakPressure_RiskCappedAtOne(t *testing.T) {
	p := newPredictor()

	param := makeParam(1, 10000, 560, 70, 100, 100000, 100)
	cacheN(p, param, 10)

	rec, err := p.EvaluateBed(1)
	if err != nil {
		t.Fatalf("EvaluateBed error: %v", err)
	}
	if rec == nil {
		t.Fatal("record is nil")
	}

	if !(rec.RiskProbability <= 1.0) {
		t.Errorf("RiskProbability = %v, want <= 1.0", rec.RiskProbability)
	}
	if rec.RiskProbability < 0 {
		t.Errorf("RiskProbability = %v, want >= 0", rec.RiskProbability)
	}
	if !(math.Abs(rec.RiskProbability-1.0) < 1e-9) {
		t.Errorf("extreme values RiskProbability = %v, expected to be clamped at 1.0", rec.RiskProbability)
	}
}

func TestAbnormal_NegativeFeatureValues_HandledGracefully(t *testing.T) {
	p := newPredictor()

	param := models.VentilatorParam{
		BedID:           1,
		PeakPressure:    -10,
		TidalVolume:     560,
		PredictedWeight: 70,
		OralSecretion:   -5,
		VentilatorHours: 24,
		PriorInfection:  0,
		Time:            time.Now(),
	}
	cacheN(p, param, 10)

	rec, err := p.EvaluateBed(1)
	if err != nil {
		t.Fatalf("EvaluateBed error: %v", err)
	}
	if rec == nil {
		t.Fatal("record is nil")
	}

	if rec.RiskProbability < 0 || rec.RiskProbability > 1 {
		t.Errorf("RiskProbability = %v, want in [0,1]", rec.RiskProbability)
	}
	if math.IsNaN(rec.HazardsRatio) || math.IsInf(rec.HazardsRatio, 0) {
		t.Errorf("HazardsRatio = %v, want finite", rec.HazardsRatio)
	}
}
