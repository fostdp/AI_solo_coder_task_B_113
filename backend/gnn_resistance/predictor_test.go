package gnn_resistance

import (
	"math/rand"
	"sync"
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

func newTestConfig() config.GNNConfig {
	return config.GNNConfig{
		PythonServiceURL:  "http://localhost:9999",
		UpdateIntervalSec: 60,
		NumOfBeds:         10,
		NumFeatures:       5,
	}
}

func TestNewGNNSpreadPredictor(t *testing.T) {
	cfg := newTestConfig()
	p := NewGNNSpreadPredictor(cfg)

	if p == nil {
		t.Fatal("NewGNNSpreadPredictor returned nil")
	}

	numBeds := 10
	if len(p.GraphAdjacency) != numBeds {
		t.Errorf("GraphAdjacency rows = %d, want %d", len(p.GraphAdjacency), numBeds)
	}
	for i, row := range p.GraphAdjacency {
		if len(row) != numBeds {
			t.Errorf("GraphAdjacency row %d cols = %d, want %d", i, len(row), numBeds)
		}
	}

	if len(p.NodeFeatures) != numBeds {
		t.Errorf("NodeFeatures rows = %d, want %d", len(p.NodeFeatures), numBeds)
	}
	numFeatures := 5
	for i, row := range p.NodeFeatures {
		if len(row) != numFeatures {
			t.Errorf("NodeFeatures row %d cols = %d, want %d", i, len(row), numFeatures)
		}
	}

	for i := 0; i < numBeds; i++ {
		if p.GraphAdjacency[i][i] != 1.0 {
			t.Errorf("GraphAdjacency[%d][%d] = %f, want 1.0", i, i, p.GraphAdjacency[i][i])
		}
	}

	for i := 0; i < numBeds; i++ {
		for j := 0; j < numBeds; j++ {
			if i != j {
				v := p.GraphAdjacency[i][j]
				if v < 0.1 || v > 0.9 {
					t.Errorf("GraphAdjacency[%d][%d] = %f, want in [0.1, 0.9]", i, j, v)
				}
			}
		}
	}

	if p.httpClient == nil {
		t.Error("httpClient is nil")
	}
	if p.BedCulture == nil {
		t.Error("BedCulture is nil")
	}
	if p.BedPrediction == nil {
		t.Error("BedPrediction is nil")
	}
	if p.stopChan == nil {
		t.Error("stopChan is nil")
	}
}

func TestBuildAdjacencyMatrix(t *testing.T) {
	cfg := config.GNNConfig{
		PythonServiceURL:  "http://localhost:9999",
		UpdateIntervalSec: 60,
		NumOfBeds:         4,
		NumFeatures:       5,
	}
	p := NewGNNSpreadPredictor(cfg)

	beds := []models.Bed{
		{ID: 1, LocationX: 0, LocationY: 0},
		{ID: 2, LocationX: 1, LocationY: 0},
		{ID: 3, LocationX: 100, LocationY: 100},
		{ID: 4, LocationX: 101, LocationY: 100},
	}

	adj := p.BuildAdjacencyMatrix(beds)

	if len(adj) != 4 {
		t.Fatalf("adj rows = %d, want 4", len(adj))
	}

	for i := 0; i < 4; i++ {
		if adj[i][i] != 1.0 {
			t.Errorf("adj[%d][%d] = %f, want 1.0 (diagonal)", i, i, adj[i][i])
		}
	}

	d12 := adj[0][1]
	d13 := adj[0][2]
	if d12 <= d13 {
		t.Errorf("adj[0][1](%f) should be > adj[0][2](%f) (closer beds)", d12, d13)
	}

	d34 := adj[2][3]
	if d34 <= d13 {
		t.Errorf("adj[2][3](%f) should be > adj[0][2](%f)", d34, d13)
	}

	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			if adj[i][j] != adj[j][i] {
				t.Errorf("adj not symmetric: adj[%d][%d]=%f, adj[%d][%d]=%f",
					i, j, adj[i][j], j, i, adj[j][i])
			}
		}
	}
}

func TestFallbackPrediction_Range(t *testing.T) {
	cfg := newTestConfig()
	p := NewGNNSpreadPredictor(cfg)

	rand.Seed(42)
	pred := p.FallbackPrediction(3, "MRSA")

	if pred == nil {
		t.Fatal("FallbackPrediction returned nil")
	}
	if pred.SourceBed != 3 {
		t.Errorf("SourceBed = %d, want 3", pred.SourceBed)
	}
	if pred.BacteriaName != "MRSA" {
		t.Errorf("BacteriaName = %s, want MRSA", pred.BacteriaName)
	}
	if pred.IsFallback != true {
		t.Error("IsFallback should be true")
	}

	if pred.SpreadProb < 0.3 || pred.SpreadProb > 1.0 {
		t.Errorf("SpreadProb = %f, want in [0.3, 1.0]", pred.SpreadProb)
	}

	if len(pred.Path) == 0 {
		t.Fatal("Path is empty")
	}
	if pred.Path[0] != 3 {
		t.Errorf("Path[0] = %d, want 3 (source bed)", pred.Path[0])
	}

	if len(pred.Path) > 6 {
		t.Errorf("Path length = %d, want <= 6 (source + top 5)", len(pred.Path))
	}

	if len(pred.EdgeWeights) != len(pred.Path)-1 {
		t.Errorf("EdgeWeights len = %d, want %d (path-1)", len(pred.EdgeWeights), len(pred.Path)-1)
	}

	for i, w := range pred.EdgeWeights {
		if w < 0.0 || w > 1.0 {
			t.Errorf("EdgeWeights[%d] = %f, want in [0,1]", i, w)
		}
	}

	if pred.PredictedAt.IsZero() {
		t.Error("PredictedAt is zero")
	}

	pred2 := p.FallbackPrediction(5, "CRE")
	foundDifferent := pred2.SpreadProb != pred.SpreadProb
	for i := 0; i < 20 && !foundDifferent; i++ {
		another := p.FallbackPrediction(5, "CRE")
		if another.SpreadProb != pred2.SpreadProb {
			foundDifferent = true
		}
	}
	t.Logf("SpreadProb randomness check: pred1=%f, pred2=%f, varied=%v",
		pred.SpreadProb, pred2.SpreadProb, foundDifferent)
}

func TestBuildNodeFeatures_Dimensions(t *testing.T) {
	cfg := newTestConfig()
	p := NewGNNSpreadPredictor(cfg)

	features := p.BuildNodeFeatures()

	numBeds := 10
	numFeatures := 5

	if len(features) != numBeds {
		t.Fatalf("features rows = %d, want %d", len(features), numBeds)
	}
	for i, row := range features {
		if len(row) != numFeatures {
			t.Errorf("features row %d cols = %d, want %d", i, len(row), numFeatures)
		}
	}

	for i := 0; i < numBeds; i++ {
		for j := 0; j < numFeatures; j++ {
			if features[i][j] != 0.0 {
				t.Errorf("features[%d][%d] = %f, want 0 (no culture data)", i, j, features[i][j])
			}
		}
	}

	now := time.Now()
	p.UpdateCultureResult(models.CultureResult{
		ID:           1,
		BedID:        2,
		BacteriaName: "MRSA",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	})
	p.UpdateCultureResult(models.CultureResult{
		ID:           2,
		BedID:        5,
		BacteriaName: "CRE",
		Result:       "negative",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	features2 := p.BuildNodeFeatures()

	bed2idx := 1
	if features2[bed2idx][0] != 1.0 {
		t.Errorf("bed2 isolation_status = %f, want 1.0", features2[bed2idx][0])
	}
	if features2[bed2idx][3] != 1.0 {
		t.Errorf("bed2 culture_positive = %f, want 1.0 (positive result)", features2[bed2idx][3])
	}

	bed5idx := 4
	if features2[bed5idx][0] != 1.0 {
		t.Errorf("bed5 isolation_status = %f, want 1.0", features2[bed5idx][0])
	}
	if features2[bed5idx][3] != 0.0 {
		t.Errorf("bed5 culture_positive = %f, want 0.0 (negative result)", features2[bed5idx][3])
	}

	bed1idx := 0
	for j := 0; j < numFeatures; j++ {
		if features2[bed1idx][j] != 0.0 {
			t.Errorf("bed1 (no culture) features[%d] = %f, want 0", j, features2[bed1idx][j])
		}
	}
}

func TestUpdateCultureResult_And_GetPrediction(t *testing.T) {
	cfg := newTestConfig()
	p := NewGNNSpreadPredictor(cfg)

	now := time.Now()
	cr1 := models.CultureResult{
		ID:           101,
		BedID:        7,
		BacteriaName: "VRE",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	}

	p.UpdateCultureResult(cr1)

	p.mu.RLock()
	cultures, ok := p.BedCulture[7]
	p.mu.RUnlock()

	if !ok {
		t.Fatal("BedCulture[7] not found after UpdateCultureResult")
	}
	if len(cultures) != 1 {
		t.Errorf("BedCulture[7] len = %d, want 1", len(cultures))
	}
	if cultures[0].BacteriaName != "VRE" {
		t.Errorf("BacteriaName = %s, want VRE", cultures[0].BacteriaName)
	}

	cr2 := models.CultureResult{
		ID:           102,
		BedID:        7,
		BacteriaName: "MRSA",
		Result:       "positive",
		CollectedAt:  now.Add(time.Hour),
		ReportedAt:   now.Add(time.Hour),
	}
	p.UpdateCultureResult(cr2)

	p.mu.RLock()
	cultures2 := p.BedCulture[7]
	p.mu.RUnlock()

	if len(cultures2) != 2 {
		t.Errorf("BedCulture[7] len = %d, want 2 after second update", len(cultures2))
	}

	fakePred := &models.ResistancePrediction{
		SourceBed:    7,
		BacteriaName: "MRSA",
		SpreadProb:   0.75,
		Path:         []uint32{7, 8, 9},
		EdgeWeights:  []float64{0.8, 0.6},
		PredictedAt:  now,
		IsFallback:   false,
	}

	p.mu.Lock()
	p.BedPrediction[7] = fakePred
	p.mu.Unlock()

	got := p.GetPrediction(7)
	if got == nil {
		t.Fatal("GetPrediction(7) returned nil")
	}
	if got.SpreadProb != 0.75 {
		t.Errorf("SpreadProb = %f, want 0.75", got.SpreadProb)
	}
	if got.SourceBed != 7 {
		t.Errorf("SourceBed = %d, want 7", got.SourceBed)
	}

	gotNil := p.GetPrediction(999)
	if gotNil != nil {
		t.Error("GetPrediction(999) should be nil for non-existent bed")
	}

	allPreds := p.GetAllPredictions()
	if len(allPreds) != 1 {
		t.Errorf("GetAllPredictions len = %d, want 1", len(allPreds))
	}
	if allPreds[7] != fakePred {
		t.Error("GetAllPredictions missing bed 7")
	}
}

func TestPredictSpread_FallbackOnInvalidURL(t *testing.T) {
	cfg := config.GNNConfig{
		PythonServiceURL:  "http://invalid-url-that-does-not-exist-12345:9999",
		UpdateIntervalSec: 60,
		NumOfBeds:         8,
		NumFeatures:       5,
	}
	p := NewGNNSpreadPredictor(cfg)

	now := time.Now()
	p.UpdateCultureResult(models.CultureResult{
		ID:           1,
		BedID:        3,
		BacteriaName: "MRSA",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	p.BuildNodeFeatures()

	pred, err := p.PredictSpread(3, "MRSA")

	if pred == nil {
		t.Fatal("PredictSpread returned nil prediction even for fallback")
	}

	if err == nil {
		t.Log("Note: PredictSpread did not return error (connection may have timed out slowly)")
	} else {
		t.Logf("Got expected error: %v", err)
	}

	if !pred.IsFallback {
		t.Error("IsFallback should be true when Python service is unreachable")
	}

	if pred.SourceBed != 3 {
		t.Errorf("SourceBed = %d, want 3", pred.SourceBed)
	}
	if pred.BacteriaName != "MRSA" {
		t.Errorf("BacteriaName = %s, want MRSA", pred.BacteriaName)
	}

	if pred.SpreadProb < 0.3 || pred.SpreadProb > 1.0 {
		t.Errorf("SpreadProb = %f, want in [0.3, 1.0]", pred.SpreadProb)
	}

	if len(pred.Path) == 0 {
		t.Error("Path is empty in fallback")
	}
	if pred.Path[0] != 3 {
		t.Errorf("Path[0] = %d, want 3", pred.Path[0])
	}

	cached := p.GetPrediction(3)
	if cached == nil {
		t.Fatal("Fallback prediction was not cached")
	}
	if !cached.IsFallback {
		t.Error("Cached prediction should be fallback")
	}
	if cached.SpreadProb != pred.SpreadProb {
		t.Errorf("Cached SpreadProb mismatch")
	}
}

func TestPredictAllSpread_MultipleBeds(t *testing.T) {
	cfg := config.GNNConfig{
		PythonServiceURL:  "http://127.0.0.1:1",
		UpdateIntervalSec: 60,
		NumOfBeds:         6,
		NumFeatures:       5,
	}
	p := NewGNNSpreadPredictor(cfg)

	now := time.Now()
	targetBeds := []uint32{1, 3, 5}
	bacteria := []string{"CRE", "MRSA", "VRE"}

	for i, bed := range targetBeds {
		p.UpdateCultureResult(models.CultureResult{
			ID:           uint32(i + 1),
			BedID:        bed,
			BacteriaName: bacteria[i],
			Result:       "positive",
			CollectedAt:  now,
			ReportedAt:   now,
		})
	}

	p.PredictAllSpread()

	allPreds := p.GetAllPredictions()
	if len(allPreds) != len(targetBeds) {
		t.Errorf("GetAllPredictions len = %d, want %d", len(allPreds), len(targetBeds))
	}

	for i, bed := range targetBeds {
		pred, ok := allPreds[bed]
		if !ok {
			t.Errorf("Missing prediction for bed %d", bed)
			continue
		}
		if pred.SourceBed != bed {
			t.Errorf("bed %d: SourceBed = %d, mismatch", bed, pred.SourceBed)
		}
		if pred.BacteriaName != bacteria[i] {
			t.Errorf("bed %d: BacteriaName = %s, want %s", bed, pred.BacteriaName, bacteria[i])
		}
		if !pred.IsFallback {
			t.Errorf("bed %d: IsFallback should be true (no Python service)", bed)
		}
		if pred.SpreadProb < 0.3 || pred.SpreadProb > 1.0 {
			t.Errorf("bed %d: SpreadProb = %f, out of range", bed, pred.SpreadProb)
		}
	}

	pred2 := p.GetPrediction(2)
	if pred2 != nil {
		t.Error("Bed 2 should not have prediction (no culture data)")
	}
}

func TestGlobalSingleton(t *testing.T) {
	oldInstance := Instance
	Instance = nil
	instanceOnce = sync.Once{}

	cfg1 := newTestConfig()
	p1 := NewGNNSpreadPredictor(cfg1)

	if Instance != p1 {
		t.Error("Instance should equal p1 after first NewGNNSpreadPredictor")
	}

	got1 := GetInstance()
	if got1 != p1 {
		t.Error("GetInstance() should return p1")
	}

	cfg2 := config.GNNConfig{
		PythonServiceURL:  "http://different:8888",
		UpdateIntervalSec: 120,
		NumOfBeds:         20,
		NumFeatures:       5,
	}
	p2 := NewGNNSpreadPredictor(cfg2)

	if Instance != p1 {
		t.Error("Instance should still be p1 (singleton pattern via sync.Once)")
	}
	if p2 != p1 {
		t.Log("Note: second NewGNNSpreadPredictor returns new instance but Instance global unchanged")
	}
	_ = p2

	Instance = oldInstance
}

func TestStartStop(t *testing.T) {
	cfg := config.GNNConfig{
		PythonServiceURL:  "http://127.0.0.1:1",
		UpdateIntervalSec: 1,
		NumOfBeds:         5,
		NumFeatures:       5,
	}
	p := NewGNNSpreadPredictor(cfg)

	now := time.Now()
	p.UpdateCultureResult(models.CultureResult{
		ID:           1,
		BedID:        2,
		BacteriaName: "MRSA",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	p.Start()
	time.Sleep(1500 * time.Millisecond)
	p.Stop()

	preds := p.GetAllPredictions()
	t.Logf("After Start/Stop, predictions count: %d", len(preds))
	if len(preds) == 0 {
		t.Log("Note: No predictions generated (ticker may not have fired)")
	}
}

func TestBuildNodeFeatures_BaselineRisk(t *testing.T) {
	cfg := config.GNNConfig{
		PythonServiceURL:  "http://localhost:9999",
		UpdateIntervalSec: 60,
		NumOfBeds:         5,
		NumFeatures:       5,
	}
	p := NewGNNSpreadPredictor(cfg)

	now := time.Now()
	numCultures := 15
	for i := 0; i < numCultures; i++ {
		p.UpdateCultureResult(models.CultureResult{
			ID:           uint32(i + 1),
			BedID:        3,
			BacteriaName: "MRSA",
			Result:       "positive",
			CollectedAt:  now.Add(time.Duration(i) * time.Hour),
			ReportedAt:   now.Add(time.Duration(i) * time.Hour),
		})
	}

	features := p.BuildNodeFeatures()
	bed3idx := 2

	baselineRisk := features[bed3idx][4]
	if baselineRisk > 0.9 {
		t.Errorf("baselineRisk = %f, should be capped at 0.9", baselineRisk)
	}
	if baselineRisk < 0.3 {
		t.Errorf("baselineRisk = %f, should be >= 0.3 for culture-positive bed", baselineRisk)
	}
	t.Logf("bed3 baselineRisk = %f (with %d cultures)", baselineRisk, numCultures)
}
