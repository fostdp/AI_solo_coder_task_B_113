package cox_vap

import (
	"sync"
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

func makeTestConfig() config.CoxVapConfig {
	return config.CoxVapConfig{
		UpdateIntervalSec: 1,
		ModelWeights:      []float64{0.02, 0.015, 0.08, 0.005, 0.12},
		RiskThreshold:     0.5,
	}
}

func makeTestParams(bedID uint32, n int) []models.VentilatorParam {
	params := make([]models.VentilatorParam, n)
	for i := 0; i < n; i++ {
		params[i] = models.VentilatorParam{
			Timestamp:       time.Now().Add(-time.Duration(n-i) * 5 * time.Minute),
			BedID:           bedID,
			PeakPressure:    25 + float64(i%5),
			TidalVolume:     500,
			PredictedWeight: 70,
			OralSecretion:   2,
			VentilatorHours: 12 + float64(i),
			PriorInfection:  1,
		}
	}
	return params
}

func TestNewCoxPredictor(t *testing.T) {
	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 50)
	cfg := makeTestConfig()

	p := NewCoxPredictor(cfg, in, out)

	if p == nil {
		t.Fatal("NewCoxPredictor returned nil")
	}
	if p.InChan != in {
		t.Error("InChan not set correctly")
	}
	if p.OutChan != out {
		t.Error("OutChan not set correctly")
	}
	if p.BedBuffers == nil {
		t.Error("BedBuffers not initialized")
	}
	if p.bedParamHistory == nil {
		t.Error("bedParamHistory not initialized")
	}
	if p.coxCoefficients == nil {
		t.Error("coxCoefficients not initialized")
	}
	if p.baselineHazard != 0.0003 {
		t.Errorf("baselineHazard = %v, want 0.0003", p.baselineHazard)
	}
}

func TestCoefficientsInitialization(t *testing.T) {
	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 50)
	cfg := makeTestConfig()

	p := NewCoxPredictor(cfg, in, out)

	expected := map[string]float64{
		"peak_pressure":          0.02,
		"tidal_volume_deviation": 0.015,
		"oral_secretion":         0.08,
		"ventilator_hours":       0.005,
		"prior_infection":        0.12,
	}

	if len(p.coxCoefficients) != len(expected) {
		t.Fatalf("got %d coefficients, want %d", len(p.coxCoefficients), len(expected))
	}

	for k, v := range expected {
		got, ok := p.coxCoefficients[k]
		if !ok {
			t.Errorf("missing coefficient %q", k)
			continue
		}
		if got != v {
			t.Errorf("coefficient %q = %v, want %v", k, got, v)
		}
	}
}

func TestFeatureCalculation(t *testing.T) {
	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 50)
	cfg := makeTestConfig()
	p := NewCoxPredictor(cfg, in, out)

	params := []models.VentilatorParam{
		{
			Timestamp:       time.Now(),
			BedID:           1,
			PeakPressure:    30,
			TidalVolume:     560,
			PredictedWeight: 70,
			OralSecretion:   3,
			VentilatorHours: 24,
			PriorInfection:  1,
		},
		{
			Timestamp:       time.Now(),
			BedID:           1,
			PeakPressure:    20,
			TidalVolume:     490,
			PredictedWeight: 70,
			OralSecretion:   1,
			VentilatorHours: 36,
			PriorInfection:  0,
		},
	}

	for _, param := range params {
		p.cacheParam(param)
	}

	record, err := p.EvaluateBed(1)
	if err != nil {
		t.Fatalf("EvaluateBed returned error: %v", err)
	}
	if record == nil {
		t.Fatal("EvaluateBed returned nil record")
	}

	expectedPeakPressure := (30.0 + 20.0) / 2.0
	if record.Features["peak_pressure"] != expectedPeakPressure {
		t.Errorf("peak_pressure = %v, want %v", record.Features["peak_pressure"], expectedPeakPressure)
	}

	tidalDev1 := 560.0/70.0 - 8.0
	tidalDev2 := 8.0 - 490.0/70.0
	expectedTidalDev := (tidalDev1 + tidalDev2) / 2.0
	if record.Features["tidal_volume_deviation"] != expectedTidalDev {
		t.Errorf("tidal_volume_deviation = %v, want %v", record.Features["tidal_volume_deviation"], expectedTidalDev)
	}

	expectedOral := (3.0 + 1.0) / 2.0
	if record.Features["oral_secretion"] != expectedOral {
		t.Errorf("oral_secretion = %v, want %v", record.Features["oral_secretion"], expectedOral)
	}

	if record.Features["ventilator_hours"] != 36 {
		t.Errorf("ventilator_hours = %v, want 36", record.Features["ventilator_hours"])
	}

	expectedPrior := (1.0 + 0.0) / 2.0
	if record.Features["prior_infection"] != expectedPrior {
		t.Errorf("prior_infection = %v, want %v", record.Features["prior_infection"], expectedPrior)
	}
}

func TestHazardsRatioRange(t *testing.T) {
	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 50)
	cfg := makeTestConfig()
	p := NewCoxPredictor(cfg, in, out)

	tests := []struct {
		name   string
		params []models.VentilatorParam
	}{
		{
			name: "low_risk",
			params: []models.VentilatorParam{{
				Timestamp:       time.Now(),
				BedID:           1,
				PeakPressure:    10,
				TidalVolume:     560,
				PredictedWeight: 70,
				OralSecretion:   0,
				VentilatorHours: 1,
				PriorInfection:  0,
			}},
		},
		{
			name: "high_risk",
			params: []models.VentilatorParam{{
				Timestamp:       time.Now(),
				BedID:           2,
				PeakPressure:    50,
				TidalVolume:     980,
				PredictedWeight: 70,
				OralSecretion:   5,
				VentilatorHours: 200,
				PriorInfection:  2,
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, param := range tt.params {
				p.cacheParam(param)
			}
			record, _ := p.EvaluateBed(tt.params[0].BedID)
			if record == nil {
				t.Fatal("record is nil")
			}
			if record.HazardsRatio <= 0 {
				t.Errorf("HazardsRatio = %v, want > 0", record.HazardsRatio)
			}
		})
	}
}

func TestRiskProbInRange(t *testing.T) {
	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 50)
	cfg := makeTestConfig()
	p := NewCoxPredictor(cfg, in, out)

	params := makeTestParams(1, 50)
	for _, param := range params {
		p.cacheParam(param)
	}

	record, err := p.EvaluateBed(1)
	if err != nil {
		t.Fatalf("EvaluateBed error: %v", err)
	}
	if record == nil {
		t.Fatal("record is nil")
	}

	if record.RiskProb < 0 || record.RiskProb > 1 {
		t.Errorf("RiskProb = %v, want in [0, 1]", record.RiskProb)
	}
}

func TestEvaluateBedOutput(t *testing.T) {
	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 50)
	cfg := makeTestConfig()
	p := NewCoxPredictor(cfg, in, out)

	param := models.VentilatorParam{
		Timestamp:       time.Now(),
		BedID:           5,
		PeakPressure:    25,
		TidalVolume:     560,
		PredictedWeight: 70,
		OralSecretion:   2,
		VentilatorHours: 48,
		PriorInfection:  1,
	}
	p.cacheParam(param)

	record, err := p.EvaluateBed(5)
	if err != nil {
		t.Fatalf("EvaluateBed error: %v", err)
	}
	if record == nil {
		t.Fatal("record is nil")
	}

	if record.BedID != 5 {
		t.Errorf("BedID = %v, want 5", record.BedID)
	}
	if record.Timestamp.IsZero() {
		t.Error("Timestamp is zero")
	}
	if len(record.FeatureWeights) != 5 {
		t.Errorf("FeatureWeights has %d entries, want 5", len(record.FeatureWeights))
	}
	if len(record.Features) != 5 {
		t.Errorf("Features has %d entries, want 5", len(record.Features))
	}

	totalWeight := 0.0
	for _, w := range record.FeatureWeights {
		totalWeight += w
	}
	if totalWeight < 99.99 || totalWeight > 100.01 {
		t.Errorf("FeatureWeights sum = %v, want ~100", totalWeight)
	}

	buffered := p.GetLatestByBed(5)
	if buffered == nil {
		t.Fatal("GetLatestByBed returned nil")
	}
	if buffered.BedID != 5 {
		t.Errorf("buffered BedID = %v, want 5", buffered.BedID)
	}
}

func TestGlobalSingleton(t *testing.T) {
	if Instance != nil {
		Instance = nil
	}

	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 50)
	cfg := makeTestConfig()

	p := NewCoxPredictor(cfg, in, out)

	if GetInstance() == nil {
		t.Error("GetInstance() returned nil after NewCoxPredictor")
	}
	if GetInstance() != p {
		t.Error("GetInstance() does not return the created instance")
	}
	if Instance != p {
		t.Error("Instance var does not match created instance")
	}
}

func TestMultiBedConcurrentEvaluation(t *testing.T) {
	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 50)
	cfg := makeTestConfig()
	p := NewCoxPredictor(cfg, in, out)

	numBeds := uint32(10)
	numRoutines := 5
	var wg sync.WaitGroup

	wg.Add(numRoutines)
	for r := 0; r < numRoutines; r++ {
		go func(routineID int) {
			defer wg.Done()
			for bed := uint32(1); bed <= numBeds; bed++ {
				for i := 0; i < 10; i++ {
					param := models.VentilatorParam{
						Timestamp:       time.Now(),
						BedID:           bed,
						PeakPressure:    20 + float64(routineID+i%5),
						TidalVolume:     500 + float64(i*10),
						PredictedWeight: 70,
						OralSecretion:   float64(i % 4),
						VentilatorHours: 10 + float64(bed),
						PriorInfection:  float64(i % 2),
					}
					p.cacheParam(param)
				}
			}
		}(r)
	}
	wg.Wait()

	var evalWg sync.WaitGroup
	evalWg.Add(int(numBeds))
	for bed := uint32(1); bed <= numBeds; bed++ {
		go func(bedID uint32) {
			defer evalWg.Done()
			record, err := p.EvaluateBed(bedID)
			if err != nil {
				t.Errorf("EvaluateBed(%d) error: %v", bedID, err)
				return
			}
			if record == nil {
				t.Errorf("EvaluateBed(%d) returned nil", bedID)
				return
			}
			if record.BedID != bedID {
				t.Errorf("record.BedID = %d, want %d", record.BedID, bedID)
			}
			if record.RiskProb < 0 || record.RiskProb > 1 {
				t.Errorf("RiskProb out of range for bed %d: %v", bedID, record.RiskProb)
			}
		}(bed)
	}
	evalWg.Wait()

	allLatest := p.GetAllLatest()
	if len(allLatest) != int(numBeds) {
		t.Errorf("GetAllLatest() has %d beds, want %d", len(allLatest), int(numBeds))
	}
	for bed := uint32(1); bed <= numBeds; bed++ {
		if _, ok := allLatest[bed]; !ok {
			t.Errorf("bed %d missing from GetAllLatest()", bed)
		}
		latest := p.GetLatestByBed(bed)
		if latest == nil {
			t.Errorf("GetLatestByBed(%d) returned nil", bed)
		}
	}
}

func TestCacheParamSlidingWindow(t *testing.T) {
	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 50)
	cfg := makeTestConfig()
	p := NewCoxPredictor(cfg, in, out)

	bedID := uint32(1)
	numPoints := 400

	for i := 0; i < numPoints; i++ {
		param := models.VentilatorParam{
			Timestamp:       time.Now().Add(-time.Duration(numPoints-i) * time.Minute),
			BedID:           bedID,
			PeakPressure:    float64(i),
			TidalVolume:     500,
			PredictedWeight: 70,
			OralSecretion:   1,
			VentilatorHours: float64(i),
			PriorInfection:  0,
		}
		p.cacheParam(param)
	}

	p.mu.RLock()
	history := p.bedParamHistory[bedID]
	p.mu.RUnlock()

	if len(history) != maxHistoryPoints {
		t.Errorf("history length = %d, want %d", len(history), maxHistoryPoints)
	}

	expectedFirstPeak := float64(numPoints - maxHistoryPoints)
	if history[0].PeakPressure != expectedFirstPeak {
		t.Errorf("first PeakPressure = %v, want %v", history[0].PeakPressure, expectedFirstPeak)
	}
}

func TestGetLatestByBedNotFound(t *testing.T) {
	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 50)
	cfg := makeTestConfig()
	p := NewCoxPredictor(cfg, in, out)

	result := p.GetLatestByBed(999)
	if result != nil {
		t.Errorf("GetLatestByBed(999) = %v, want nil", result)
	}
}

func TestEvaluateAllBedsSendsToChannel(t *testing.T) {
	in := make(chan models.VentilatorParam, 1000)
	out := make(chan models.VapRiskRecord, 10)
	cfg := makeTestConfig()
	p := NewCoxPredictor(cfg, in, out)

	for bed := uint32(1); bed <= 3; bed++ {
		for i := 0; i < 5; i++ {
			param := models.VentilatorParam{
				Timestamp:       time.Now(),
				BedID:           bed,
				PeakPressure:    25,
				TidalVolume:     560,
				PredictedWeight: 70,
				OralSecretion:   2,
				VentilatorHours: 24,
				PriorInfection:  1,
			}
			p.cacheParam(param)
		}
	}

	p.EvaluateAllBeds()

	time.Sleep(50 * time.Millisecond)
	got := len(out)
	if got != 3 {
		t.Errorf("got %d records on OutChan, want 3", got)
	}
}
