package infection_rf

import (
	"math"
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

func TestNewPredictor(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          5,
		BufferSize:         100,
		PredictionInterval: 5,
		RandomForest: config.RandomForestConfig{
			NumTrees:               10,
			TreeWeightMin:          0.5,
			TreeWeightMax:          1.0,
			CRERiskAntibioticCoef:  0.03,
			CRERiskInvasiveCoef:    0.02,
			CRERiskNoise:           0.05,
			MRSARiskAntibioticCoef: 0.025,
			MRSARiskInvasiveCoef:   0.025,
			MRSARiskNoise:          0.05,
		},
	}

	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan InfectionPrediction, 10)

	p := NewPredictor(cfg, inChan, outChan)
	if p == nil {
		t.Fatal("NewPredictor returned nil")
	}
	if p.InChan == nil {
		t.Error("InChan is nil")
	}
	if p.OutChan == nil {
		t.Error("OutChan is nil")
	}
	if p.vitalCache == nil {
		t.Error("vitalCache is nil")
	}
	if len(p.vitalCache) != 5 {
		t.Errorf("vitalCache should have %d beds, got %d", 5, len(p.vitalCache))
	}
	if len(p.rfTreeWeights) != 10 {
		t.Errorf("rfTreeWeights should have %d trees, got %d", 10, len(p.rfTreeWeights))
	}
}

func TestPredictor_StartStop(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          3,
		PredictionInterval: 1,
		RandomForest: config.RandomForestConfig{
			NumTrees:      5,
			TreeWeightMin: 0.5,
			TreeWeightMax: 1.0,
		},
	}

	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan InfectionPrediction, 10)

	p := NewPredictor(cfg, inChan, outChan)

	p.Start()
	time.Sleep(100 * time.Millisecond)

	p.Stop()
	time.Sleep(50 * time.Millisecond)
}

func TestSetAntibioticDays(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:  2,
		RandomForest: config.RandomForestConfig{
			NumTrees: 5,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	days := p.getAntibioticDays(1)
	if days != 0 {
		t.Errorf("initial antibiotic days = %d, want 0", days)
	}

	p.SetAntibioticDays(1, 7)
	days = p.getAntibioticDays(1)
	if days != 7 {
		t.Errorf("antibiotic days after set = %d, want 7", days)
	}

	days = p.getAntibioticDays(999)
	if days != 0 {
		t.Errorf("unknown bed antibiotic days = %d, want 0", days)
	}
}

func TestSetInvasiveCount(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:  2,
		RandomForest: config.RandomForestConfig{
			NumTrees: 5,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	count := p.getInvasiveCount(1)
	if count != 0 {
		t.Errorf("initial invasive count = %d, want 0", count)
	}

	p.SetInvasiveCount(1, 3)
	count = p.getInvasiveCount(1)
	if count != 3 {
		t.Errorf("invasive count after set = %d, want 3", count)
	}
}

func TestVitalSignLoop(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          2,
		PredictionInterval: 10,
		RandomForest: config.RandomForestConfig{
			NumTrees: 5,
		},
	}
	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan InfectionPrediction, 10)

	p := NewPredictor(cfg, inChan, outChan)
	p.Start()
	defer p.Stop()

	now := time.Now()
	inChan <- models.VitalSign{
		Time:       now,
		BedID:      1,
		SensorType: "temperature",
		Value:      38.5,
		Unit:       "C",
	}

	time.Sleep(50 * time.Millisecond)

	p.cacheMux.RLock()
	temp := p.vitalCache[1]["temperature"]
	p.cacheMux.RUnlock()

	if temp != 38.5 {
		t.Errorf("temperature in cache = %f, want 38.5", temp)
	}
}

func TestPredictCRE(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:  1,
		RandomForest: config.RandomForestConfig{
			NumTrees:               20,
			TreeWeightMin:          0.5,
			TreeWeightMax:          1.0,
			CRERiskAntibioticCoef:  0.0,
			CRERiskInvasiveCoef:    0.0,
			CRERiskNoise:           0.0,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	risk := p.PredictCRE(1)
	if risk < 0 || risk > 1 {
		t.Errorf("CRE risk = %f, want in [0,1]", risk)
	}

	p.SetAntibioticDays(1, 10)
	p.SetInvasiveCount(1, 5)
	riskWithFactors := p.PredictCRE(1)
	if riskWithFactors < 0 || riskWithFactors > 1 {
		t.Errorf("CRE risk with factors = %f, want in [0,1]", riskWithFactors)
	}
}

func TestPredictMRSA(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:  1,
		RandomForest: config.RandomForestConfig{
			NumTrees:               20,
			TreeWeightMin:          0.5,
			TreeWeightMax:          1.0,
			MRSARiskAntibioticCoef: 0.0,
			MRSARiskInvasiveCoef:   0.0,
			MRSARiskNoise:          0.0,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	risk := p.PredictMRSA(1)
	if risk < 0 || risk > 1 {
		t.Errorf("MRSA risk = %f, want in [0,1]", risk)
	}
}

func TestPredictAll(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          3,
		PredictionInterval: 10,
		RandomForest: config.RandomForestConfig{
			NumTrees: 10,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	results := p.PredictAll()
	if len(results) != 3 {
		t.Errorf("PredictAll returned %d results, want 3", len(results))
	}

	for bedID := 1; bedID <= 3; bedID++ {
		pred, ok := results[bedID]
		if !ok {
			t.Errorf("PredictAll missing bed %d", bedID)
			continue
		}
		if pred.BedID != bedID {
			t.Errorf("bed %d: BedID = %d, mismatch", bedID, pred.BedID)
		}
		if pred.CRERisk < 0 || pred.CRERisk > 1 {
			t.Errorf("bed %d: CRERisk out of range: %f", bedID, pred.CRERisk)
		}
		if pred.MRSARisk < 0 || pred.MRSARisk > 1 {
			t.Errorf("bed %d: MRSARisk out of range: %f", bedID, pred.MRSARisk)
		}
		if pred.MaxRisk != math.Max(pred.CRERisk, pred.MRSARisk) {
			t.Errorf("bed %d: MaxRisk = %f, want max(%f, %f)",
				bedID, pred.MaxRisk, pred.CRERisk, pred.MRSARisk)
		}
		if pred.Time.IsZero() {
			t.Errorf("bed %d: Time is zero", bedID)
		}
	}
}

func TestRandomForestPredict(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:  1,
		RandomForest: config.RandomForestConfig{
			NumTrees:      50,
			TreeWeightMin: 0.5,
			TreeWeightMax: 1.0,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	features := []float64{0.5, 0.3, 0.8, 0.2, 0.6}
	result := p.randomForestPredict(features, 0)

	if result < 0 || result > 1 {
		t.Errorf("randomForestPredict result = %f, want in [0,1]", result)
	}

	result2 := p.randomForestPredict(features, 1)
	if result2 < 0 || result2 > 1 {
		t.Errorf("randomForestPredict(treeSet=1) result = %f, want in [0,1]", result2)
	}
}

func TestGetInfectionFeatures(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:  1,
		RandomForest: config.RandomForestConfig{
			NumTrees: 5,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	p.SetAntibioticDays(1, 5)
	p.SetInvasiveCount(1, 2)

	p.cacheMux.Lock()
	p.vitalCache[1]["temperature"] = 38.0
	p.vitalCache[1]["ventilator"] = 20.0
	p.cacheMux.Unlock()

	features := p.getInfectionFeatures(1)
	if len(features) != 5 {
		t.Fatalf("getInfectionFeatures returned %d features, want 5", len(features))
	}

	if features[0] != 5.0 {
		t.Errorf("feature[0] (antibiotics) = %f, want 5.0", features[0])
	}
	if features[1] != 2.0 {
		t.Errorf("feature[1] (invasive) = %f, want 2.0", features[1])
	}
	if features[2] != 38.0 {
		t.Errorf("feature[2] (temperature) = %f, want 38.0", features[2])
	}
	if features[3] != 20.0 {
		t.Errorf("feature[3] (ventilator) = %f, want 20.0", features[3])
	}
	if features[4] < 0 || features[4] > 14 {
		t.Errorf("feature[4] (admissionDays) = %f, want in [0,14]", features[4])
	}
}

func TestInitRandomForest(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds: 1,
		RandomForest: config.RandomForestConfig{
			NumTrees:      15,
			TreeWeightMin: 0.4,
			TreeWeightMax: 0.9,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	if len(p.rfTreeWeights) != 15 {
		t.Errorf("rfTreeWeights length = %d, want 15", len(p.rfTreeWeights))
	}

	for i, w := range p.rfTreeWeights {
		if w < 0.4 || w > 0.9 {
			t.Errorf("tree weight %d = %f, out of range [0.4, 0.9]", i, w)
		}
	}

	if len(p.rfFeatureSets) == 0 {
		t.Error("rfFeatureSets is empty")
	}
}

func TestInitRandomForestWithCustomFeatures(t *testing.T) {
	customSets := [][]float64{
		{0.1, 0.2, 0.3, 0.2, 0.2},
		{0.3, 0.1, 0.2, 0.3, 0.1},
	}

	cfg := config.MLConfig{
		NumOfBeds: 1,
		RandomForest: config.RandomForestConfig{
			NumTrees:          5,
			FeatureWeightSets: customSets,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	if len(p.rfFeatureSets) != 2 {
		t.Errorf("rfFeatureSets length = %d, want 2", len(p.rfFeatureSets))
	}
}

func TestInfectionPredictionStruct(t *testing.T) {
	now := time.Now()
	p := InfectionPrediction{
		BedID:    1,
		CRERisk:  0.65,
		MRSARisk: 0.45,
		MaxRisk:  0.65,
		Time:     now,
	}

	if p.BedID != 1 {
		t.Error("BedID mismatch")
	}
	if p.CRERisk != 0.65 {
		t.Error("CRERisk mismatch")
	}
	if p.MaxRisk != 0.65 {
		t.Error("MaxRisk should be max of CRE and MRSA")
	}
	if p.Time != now {
		t.Error("Time mismatch")
	}
}

func TestGlobalInstance(t *testing.T) {
	if Instance != nil {
		t.Log("Instance already set (from prior tests)")
		return
	}

	cfg := config.MLConfig{
		NumOfBeds:  1,
		RandomForest: config.RandomForestConfig{NumTrees: 3},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	Instance = p
	if Instance != p {
		t.Error("Instance not set correctly")
	}

	Instance = nil
}

func TestPredictionLoopOutput(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          2,
		PredictionInterval: 1,
		RandomForest: config.RandomForestConfig{
			NumTrees: 5,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan InfectionPrediction, 10)

	p := NewPredictor(cfg, inChan, outChan)
	p.Start()

	select {
	case pred := <-outChan:
		if pred.BedID < 1 || pred.BedID > 2 {
			t.Errorf("unexpected bed ID in output: %d", pred.BedID)
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for prediction output")
	}

	p.Stop()
}

func TestSigmoid(t *testing.T) {
	tests := []struct {
		input    float64
		expected float64
	}{
		{0.0, 0.5},
		{100.0, 1.0},
		{-100.0, 0.0},
	}

	for _, tt := range tests {
		result := sigmoid(tt.input)
		if math.Abs(result-tt.expected) > 0.001 {
			t.Errorf("sigmoid(%f) = %f, want ~%f", tt.input, result, tt.expected)
		}
	}
}
