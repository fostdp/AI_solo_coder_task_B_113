package cox_vap

import (
	"math"
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

func TestFix_TimeVaryingFeatures_ComputedCorrectly(t *testing.T) {
	p := newPredictor()

	bedID := uint32(1)
	ventHours := 64
	for i := 0; i < 50; i++ {
		peakPressure := 10.0 + float64(i)*0.8
		param := makeParam(bedID, peakPressure, 560, 70, 2, ventHours, 0.5)
		p.cacheParam(param)
	}

	rec, err := p.EvaluateBed(bedID)
	if err != nil {
		t.Fatalf("EvaluateBed(%d) error: %v", bedID, err)
	}
	if rec == nil {
		t.Fatal("EvaluateBed returned nil record")
	}

	timeVaryingKeys := []string{
		"peak_pressure_trend",
		"peak_pressure_volatility",
		"oral_secretion_trend",
		"tidal_dev_recent",
		"hours_accumulated",
	}
	for _, key := range timeVaryingKeys {
		if _, ok := rec.Features[key]; !ok {
			t.Errorf("Features missing time-varying key: %s", key)
		}
	}

	peakTrend := rec.Features["peak_pressure_trend"]
	if !(peakTrend > 0) {
		t.Errorf("peak_pressure_trend = %v, want > 0 (increasing trend)", peakTrend)
	}
	t.Logf("peak_pressure_trend = %.6f", peakTrend)

	peakVol := rec.Features["peak_pressure_volatility"]
	if !(peakVol > 0) {
		t.Errorf("peak_pressure_volatility = %v, want > 0 (has fluctuation)", peakVol)
	}
	t.Logf("peak_pressure_volatility = %.6f", peakVol)

	expectedHoursAccum := math.Sqrt(float64(ventHours))
	actualHoursAccum := rec.Features["hours_accumulated"]
	if math.Abs(actualHoursAccum-expectedHoursAccum) > 1e-9 {
		t.Errorf("hours_accumulated = %v, want sqrt(%d) = %v", actualHoursAccum, ventHours, expectedHoursAccum)
	}
}

func TestFix_ConcordanceIndex_ImprovedAfterFix(t *testing.T) {
	p := newPredictor()

	predictions := []float64{
		0.95, 0.90, 0.85, 0.80, 0.75,
		0.30, 0.25, 0.20, 0.15, 0.10,
	}
	events := []int{
		1, 1, 1, 1, 1,
		0, 0, 0, 0, 0,
	}
	times := []float64{
		10, 15, 20, 25, 30,
		100, 120, 140, 160, 180,
	}

	cIndex := p.ComputeConcordanceIndex(predictions, events, times)
	t.Logf("Computed C-index = %.4f", cIndex)

	if !(cIndex > 0.71) {
		t.Errorf("C-index = %.4f, want > 0.71 (baseline before fix)", cIndex)
	}

	if !(cIndex >= 0.75) {
		t.Errorf("C-index = %.4f, want >= 0.75", cIndex)
	}

	latest := p.GetLatestCIndex()
	if math.Abs(latest-cIndex) > 1e-12 {
		t.Errorf("GetLatestCIndex() = %.4f, want %.4f (just computed value)", latest, cIndex)
	}
}

func TestFix_TimeVaryingCovariates_ImpactRiskScore(t *testing.T) {
	p := newPredictor()

	bed1ID := uint32(1)
	for i := 0; i < 50; i++ {
		peakPressure := 10.0 + float64(i)*(40.0/49.0)
		param := makeParam(bed1ID, peakPressure, 560, 70, 2, 48, 0.5)
		p.cacheParam(param)
	}

	bed2ID := uint32(2)
	for i := 0; i < 50; i++ {
		param := makeParam(bed2ID, 30.0, 560, 70, 2, 48, 0.5)
		p.cacheParam(param)
	}

	rec1, err1 := p.EvaluateBed(bed1ID)
	rec2, err2 := p.EvaluateBed(bed2ID)
	if err1 != nil {
		t.Fatalf("EvaluateBed(%d) error: %v", bed1ID, err1)
	}
	if err2 != nil {
		t.Fatalf("EvaluateBed(%d) error: %v", bed2ID, err2)
	}
	if rec1 == nil || rec2 == nil {
		t.Fatal("EvaluateBed returned nil record")
	}

	t.Logf("bed1 (rising trend): RiskProbability=%.6f, HazardsRatio=%.6f, peak_trend=%.6f",
		rec1.RiskProbability, rec1.HazardsRatio, rec1.Features["peak_pressure_trend"])
	t.Logf("bed2 (stable):       RiskProbability=%.6f, HazardsRatio=%.6f, peak_trend=%.6f",
		rec2.RiskProbability, rec2.HazardsRatio, rec2.Features["peak_pressure_trend"])

	if !(rec1.RiskProbability > rec2.RiskProbability) {
		t.Errorf("bed1.RiskProbability(%v) should be > bed2.RiskProbability(%v) (rising trend increases risk)",
			rec1.RiskProbability, rec2.RiskProbability)
	}

	if !(rec1.HazardsRatio > rec2.HazardsRatio) {
		t.Errorf("bed1.HazardsRatio(%v) should be > bed2.HazardsRatio(%v)",
			rec1.HazardsRatio, rec2.HazardsRatio)
	}

	bed1Trend := rec1.Features["peak_pressure_trend"]
	if !(bed1Trend > 0) {
		t.Errorf("bed1 peak_pressure_trend = %v, want > 0", bed1Trend)
	}
}

func TestFix_TimeSlices_ReturnsCorrectWindows(t *testing.T) {
	p := newPredictor()

	history200 := make([]models.VentilatorParam, 200)
	for i := 0; i < 200; i++ {
		history200[i] = makeParam(1, float64(i), 560, 70, 2, 24, 0.5)
	}

	slices := p.computeTimeSlices(history200)
	if len(slices) != 3 {
		t.Fatalf("computeTimeSlices returned %d slices, want 3", len(slices))
	}
	if len(slices[0]) != 12 {
		t.Errorf("len(recent slice) = %d, want 12", len(slices[0]))
	}
	if len(slices[1]) != 72 {
		t.Errorf("len(mid slice) = %d, want 72", len(slices[1]))
	}
	if len(slices[2]) != 200 {
		t.Errorf("len(all slice) = %d, want 200", len(slices[2]))
	}

	history5 := make([]models.VentilatorParam, 5)
	for i := 0; i < 5; i++ {
		history5[i] = makeParam(2, float64(i), 560, 70, 2, 24, 0.5)
	}

	slices5 := p.computeTimeSlices(history5)
	if len(slices5[0]) != 5 {
		t.Errorf("len(recent slice with 5 points) = %d, want 5", len(slices5[0]))
	}
	if len(slices5[1]) != 5 {
		t.Errorf("len(mid slice with 5 points) = %d, want 5", len(slices5[1]))
	}
	if len(slices5[2]) != 5 {
		t.Errorf("len(all slice with 5 points) = %d, want 5", len(slices5[2]))
	}
}

func TestFix_ConcordanceIndex_EdgeCases(t *testing.T) {
	p := newPredictor()

	cEmpty := p.ComputeConcordanceIndex(nil, nil, nil)
	if math.Abs(cEmpty-0.5) > 1e-9 {
		t.Errorf("empty data C-index = %v, want 0.5", cEmpty)
	}

	cSingle := p.ComputeConcordanceIndex(
		[]float64{0.5},
		[]int{1},
		[]float64{10.0},
	)
	if math.Abs(cSingle-0.5) > 1e-9 {
		t.Errorf("single data C-index = %v, want 0.5", cSingle)
	}

	cEqual := p.ComputeConcordanceIndex(
		[]float64{0.5, 0.5, 0.5, 0.5, 0.5},
		[]int{1, 1, 1, 0, 0},
		[]float64{10, 20, 30, 40, 50},
	)
	t.Logf("equal predictions C-index = %.4f", cEqual)
	if cEqual < 0.49 || cEqual > 0.51 {
		t.Errorf("equal predictions C-index = %v, want ~0.5", cEqual)
	}

	cPerfect := p.ComputeConcordanceIndex(
		[]float64{0.99, 0.90, 0.80, 0.70, 0.60, 0.50, 0.40, 0.30, 0.20, 0.10},
		[]int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		[]float64{5, 10, 15, 20, 25, 30, 35, 40, 45, 50},
	)
	t.Logf("perfect predictions C-index = %.4f", cPerfect)
	if !(cPerfect >= 0.9) {
		t.Errorf("perfect predictions C-index = %v, want >= 0.9", cPerfect)
	}
}

func TestFix_BackwardCompatibility_BaseFeaturesPreserved(t *testing.T) {
	p := newPredictor()

	bedID := uint32(1)
	for i := 0; i < 10; i++ {
		param := makeParam(bedID, 30, 560, 70, 3, 48, 1.0)
		p.cacheParam(param)
	}

	rec, err := p.EvaluateBed(bedID)
	if err != nil {
		t.Fatalf("EvaluateBed(%d) error: %v", bedID, err)
	}
	if rec == nil {
		t.Fatal("EvaluateBed returned nil record")
	}

	baseKeys := []string{
		"peak_pressure",
		"tidal_volume_deviation",
		"oral_secretion",
		"ventilator_hours",
		"prior_infection",
	}
	for _, key := range baseKeys {
		val, ok := rec.Features[key]
		if !ok {
			t.Errorf("Features missing base feature key: %s", key)
			continue
		}
		if math.IsNaN(val) {
			t.Errorf("Features[%s] is NaN", key)
		}
		if math.Abs(val) < 1e-12 {
			t.Errorf("Features[%s] is ~0, want non-zero", key)
		}
	}

	expectedLP := 0.0
	for name, coeff := range p.coxCoefficients {
		expectedLP += coeff * rec.Features[name]
	}
	for name, coeff := range p.timeVaryingCoefficients {
		expectedLP += coeff * rec.Features[name]
	}
	expectedHR := math.Exp(expectedLP)

	tolerance := 1e-9
	if math.Abs(rec.HazardsRatio-expectedHR) > tolerance {
		t.Errorf("HazardsRatio = %.10f, want exp(ΣβX including time-varying) = %.10f",
			rec.HazardsRatio, expectedHR)
	}
	t.Logf("HazardsRatio verification: actual=%.10f, expected=%.10f, diff=%.2e",
		rec.HazardsRatio, expectedHR, math.Abs(rec.HazardsRatio-expectedHR))
}
