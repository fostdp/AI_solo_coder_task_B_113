package gnn_resistance

import (
	"math"
	"sync"
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

func resetSingleton() {
	Instance = nil
	instanceOnce = sync.Once{}
}

func newPredictorWithBeds(numBeds int) *GNNSpreadPredictor {
	resetSingleton()
	cfg := config.GNNConfig{
		PythonServiceURL:  "http://localhost:9999",
		UpdateIntervalSec: 60,
		NumOfBeds:         numBeds,
		NumFeatures:       5,
	}
	return NewGNNSpreadPredictor(cfg)
}

func TestNormal_SpreadPathConsistentWithAdjacency(t *testing.T) {
	p := newPredictorWithBeds(4)

	beds := []models.Bed{
		{ID: 1, LocationX: 0, LocationY: 0},
		{ID: 2, LocationX: 5, LocationY: 0},
		{ID: 3, LocationX: 100, LocationY: 100},
		{ID: 4, LocationX: 105, LocationY: 100},
	}

	p.BuildAdjacencyMatrix(beds)

	now := time.Now()
	p.UpdateCultureResult(models.CultureResult{
		ID:           1,
		BedID:        1,
		BacteriaName: "MRSA",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	pred := p.FallbackPrediction(1, "MRSA")

	if pred.Path[0] != 1 {
		t.Errorf("Path[0] = %d, want 1 (source bed)", pred.Path[0])
	}

	if pred.Path[1] != 2 {
		t.Errorf("Path[1] = %d, want 2 (closest neighbor)", pred.Path[1])
	}

	p.mu.RLock()
	adj := p.GraphAdjacency
	p.mu.RUnlock()

	type neighborInfo struct {
		bedID uint32
		weight float64
	}
	expectedOrder := make([]neighborInfo, 0, 3)
	for i := 0; i < 4; i++ {
		if i == 0 {
			continue
		}
		expectedOrder = append(expectedOrder, neighborInfo{
			bedID:  uint32(i + 1),
			weight: adj[0][i],
		})
	}

	for i := 0; i < len(expectedOrder); i++ {
		for j := i + 1; j < len(expectedOrder); j++ {
			if expectedOrder[i].weight < expectedOrder[j].weight {
				expectedOrder[i], expectedOrder[j] = expectedOrder[j], expectedOrder[i]
			}
		}
	}

	pathNoSource := pred.Path[1:]
	correctCount := 0
	totalCount := len(pathNoSource)
	for i, bedID := range pathNoSource {
		if i < len(expectedOrder) && bedID == expectedOrder[i].bedID {
			correctCount++
		}
	}

	accuracy := float64(correctCount) / float64(totalCount)
	if accuracy < 0.8 {
		t.Errorf("Path consistency = %.2f%%, want > 80%%", accuracy*100)
	}
}

func TestNormal_MultipleCultureResults_HigherSpreadProb(t *testing.T) {
	pA := newPredictorWithBeds(10)
	pB := newPredictorWithBeds(10)

	now := time.Now()

	pA.UpdateCultureResult(models.CultureResult{
		ID:           1,
		BedID:        3,
		BacteriaName: "MRSA",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	for i := 0; i < 5; i++ {
		pB.UpdateCultureResult(models.CultureResult{
			ID:           uint32(i + 1),
			BedID:        3,
			BacteriaName: "MRSA",
			Result:       "positive",
			CollectedAt:  now.Add(time.Duration(i) * time.Hour),
			ReportedAt:   now.Add(time.Duration(i) * time.Hour),
		})
	}

	featuresA := pA.BuildNodeFeatures()
	featuresB := pB.BuildNodeFeatures()

	bed3Idx := 2
	baselineA := featuresA[bed3Idx][4]
	baselineB := featuresB[bed3Idx][4]

	if !(baselineB > baselineA) {
		t.Errorf("B baseline_risk(%.4f) should be > A baseline_risk(%.4f)", baselineB, baselineA)
	}

	predA := pA.FallbackPrediction(3, "MRSA")
	predB := pB.FallbackPrediction(3, "MRSA")

	if predA.SpreadProb < 0.3 || predA.SpreadProb > 1.0 {
		t.Errorf("A SpreadProb = %.4f, want in [0.3, 1.0]", predA.SpreadProb)
	}
	if predB.SpreadProb < 0.3 || predB.SpreadProb > 1.0 {
		t.Errorf("B SpreadProb = %.4f, want in [0.3, 1.0]", predB.SpreadProb)
	}
}

func TestNormal_AdjacencyReflectsPhysicalDistance(t *testing.T) {
	p := newPredictorWithBeds(3)

	beds := []models.Bed{
		{ID: 1, LocationX: 0, LocationY: 0},
		{ID: 2, LocationX: 10, LocationY: 0},
		{ID: 3, LocationX: 50, LocationY: 50},
	}

	adj := p.BuildAdjacencyMatrix(beds)

	expected12 := math.Exp(-10.0 / 10.0)
	if math.Abs(adj[0][1]-expected12) > 0.01 {
		t.Errorf("adj[0][1] = %.4f, want ~%.4f (tolerance 0.01)", adj[0][1], expected12)
	}

	dist13 := math.Sqrt(50.0*50.0 + 50.0*50.0)
	expected13 := math.Exp(-dist13 / 10.0)
	if math.Abs(adj[0][2]-expected13) > 0.01 {
		t.Errorf("adj[0][2] = %.4f, want ~%.4f (tolerance 0.01)", adj[0][2], expected13)
	}

	if !(adj[0][1] > adj[0][2]) {
		t.Errorf("adj[0][1](%.4f) should be > adj[0][2](%.4f)", adj[0][1], adj[0][2])
	}
}

func TestNormal_PredictionCachedAndRetrievable(t *testing.T) {
	p := newPredictorWithBeds(10)

	now := time.Now()
	p.UpdateCultureResult(models.CultureResult{
		ID:           1,
		BedID:        3,
		BacteriaName: "CRE",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	pred := p.FallbackPrediction(3, "CRE")
	p.cachePrediction(pred)

	got := p.GetPrediction(3)

	if got == nil {
		t.Fatal("GetPrediction(3) returned nil")
	}
	if got.SpreadProb != pred.SpreadProb {
		t.Errorf("SpreadProb mismatch: cached=%.4f, original=%.4f", got.SpreadProb, pred.SpreadProb)
	}
	if got.SourceBed != 3 {
		t.Errorf("SourceBed = %d, want 3", got.SourceBed)
	}
	if !got.IsFallback {
		t.Error("IsFallback should be true")
	}
}

func TestBoundary_NoAntibioticHistory_LowRisk(t *testing.T) {
	p := newPredictorWithBeds(10)

	features := p.BuildNodeFeatures()

	numBeds := 10
	numFeatures := 5

	for i := 0; i < numBeds; i++ {
		if features[i][0] != 0.0 {
			t.Errorf("bed %d isolation_status = %.4f, want 0", i+1, features[i][0])
		}
		if features[i][1] != 0.0 {
			t.Errorf("bed %d abx_days = %.4f, want 0", i+1, features[i][1])
		}
		if features[i][2] != 0.0 {
			t.Errorf("bed %d invasive_count = %.4f, want 0", i+1, features[i][2])
		}
		if features[i][3] != 0.0 {
			t.Errorf("bed %d culture_positive = %.4f, want 0", i+1, features[i][3])
		}
		if features[i][4] != 0.1 {
			t.Errorf("bed %d baseline_risk = %.4f, want 0.1", i+1, features[i][4])
		}
		for j := 0; j < numFeatures; j++ {
			if features[i][j] < 0 || features[i][j] > 1 {
				t.Errorf("bed %d feature[%d] = %.4f, out of [0,1] range", i+1, j, features[i][j])
			}
		}
	}

	p.PredictAllSpread()

	allPreds := p.GetAllPredictions()
	if len(allPreds) != 0 {
		t.Errorf("GetAllPredictions len = %d, want 0 (no culture data)", len(allPreds))
	}

	got := p.GetPrediction(1)
	if got != nil {
		t.Error("GetPrediction(1) should be nil for bed with no culture")
	}
}

func TestBoundary_SingleIsolatedBed_SmallGraph(t *testing.T) {
	p := newPredictorWithBeds(1)

	now := time.Now()
	p.UpdateCultureResult(models.CultureResult{
		ID:           1,
		BedID:        1,
		BacteriaName: "MRSA",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	pred := p.FallbackPrediction(1, "MRSA")

	if pred == nil {
		t.Fatal("FallbackPrediction returned nil")
	}

	if len(pred.Path) != 1 {
		t.Errorf("Path len = %d, want 1 (only source bed)", len(pred.Path))
	}
	if pred.Path[0] != 1 {
		t.Errorf("Path[0] = %d, want 1", pred.Path[0])
	}

	if len(pred.EdgeWeights) != 0 {
		t.Errorf("EdgeWeights len = %d, want 0", len(pred.EdgeWeights))
	}

	if pred.SpreadProb < 0.3 || pred.SpreadProb > 1.0 {
		t.Errorf("SpreadProb = %.4f, want in [0.3, 1.0]", pred.SpreadProb)
	}

	if !pred.IsFallback {
		t.Error("IsFallback should be true")
	}
}

func TestBoundary_AllBedsNoCulture_NoPredictions(t *testing.T) {
	p := newPredictorWithBeds(50)

	p.PredictAllSpread()

	allPreds := p.GetAllPredictions()
	if len(allPreds) != 0 {
		t.Errorf("GetAllPredictions len = %d, want 0 (no culture data for any bed)", len(allPreds))
	}

	for bedID := uint32(1); bedID <= 50; bedID++ {
		got := p.GetPrediction(bedID)
		if got != nil {
			t.Errorf("GetPrediction(%d) should be nil", bedID)
		}
	}
}

func TestBoundary_CultureNegative_ResultEffect(t *testing.T) {
	p := newPredictorWithBeds(10)

	now := time.Now()
	p.UpdateCultureResult(models.CultureResult{
		ID:           1,
		BedID:        3,
		BacteriaName: "E.coli",
		Result:       "negative",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	features := p.BuildNodeFeatures()

	bed3Idx := 2
	if features[bed3Idx][0] != 1.0 {
		t.Errorf("isolation_status = %.4f, want 1.0", features[bed3Idx][0])
	}
	if features[bed3Idx][1] != 2.0 {
		t.Errorf("abx_days = %.4f, want 2.0", features[bed3Idx][1])
	}
	if features[bed3Idx][2] != 1.0 {
		t.Errorf("invasive_count = %.4f, want 1.0", features[bed3Idx][2])
	}
	if features[bed3Idx][3] != 0.0 {
		t.Errorf("culture_positive = %.4f, want 0.0 (negative result)", features[bed3Idx][3])
	}
	if features[bed3Idx][4] != 0.35 {
		t.Errorf("baseline_risk = %.4f, want 0.35", features[bed3Idx][4])
	}

	pred := p.FallbackPrediction(3, "E.coli")

	if pred == nil {
		t.Fatal("FallbackPrediction returned nil")
	}
	if len(pred.Path) < 2 {
		t.Errorf("Path len = %d, want >= 2 (source + at least one neighbor)", len(pred.Path))
	}
	if pred.Path[0] != 3 {
		t.Errorf("Path[0] = %d, want 3", pred.Path[0])
	}
}

func TestAbnormal_DisconnectedGraph_FallbackDegradation(t *testing.T) {
	p := newPredictorWithBeds(5)

	p.mu.Lock()
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			p.GraphAdjacency[i][j] = 0
		}
		p.GraphAdjacency[i][i] = 1
	}
	p.mu.Unlock()

	now := time.Now()
	p.UpdateCultureResult(models.CultureResult{
		ID:           1,
		BedID:        3,
		BacteriaName: "CRE",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	pred := p.FallbackPrediction(3, "CRE")

	if pred == nil {
		t.Fatal("FallbackPrediction returned nil even with disconnected graph")
	}

	if len(pred.Path) == 0 {
		t.Error("Path should not be empty")
	}

	if pred.Path[0] != 3 {
		t.Errorf("Path[0] = %d, want 3", pred.Path[0])
	}

	if !pred.IsFallback {
		t.Error("IsFallback should be true")
	}

	if pred.SpreadProb < 0.3 || pred.SpreadProb > 1.0 {
		t.Errorf("SpreadProb = %.4f, want in [0.3, 1.0]", pred.SpreadProb)
	}

	for i, w := range pred.EdgeWeights {
		if w < 0.0 || w > 1.0 {
			t.Errorf("EdgeWeights[%d] = %.4f, want in [0, 1]", i, w)
		}
	}
}

func TestAbnormal_PythonServiceTimeout_FallbackUsed(t *testing.T) {
	resetSingleton()
	cfg := config.GNNConfig{
		PythonServiceURL:  "http://192.0.2.1:9999",
		UpdateIntervalSec: 60,
		NumOfBeds:         10,
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

	p.BuildNodeFeatures()

	done := make(chan struct{})
	var pred *models.ResistancePrediction
	var err error

	go func() {
		pred, err = p.PredictSpread(2, "MRSA")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("PredictSpread timed out (expected fallback within HTTP timeout)")
	}

	if pred == nil {
		t.Fatal("PredictSpread returned nil even for fallback")
	}

	if !pred.IsFallback {
		t.Error("IsFallback should be true when Python service is unreachable")
	}

	if pred.SpreadProb < 0.3 || pred.SpreadProb > 1.0 {
		t.Errorf("SpreadProb = %.4f, want in [0.3, 1.0]", pred.SpreadProb)
	}

	if len(pred.Path) == 0 {
		t.Error("Path should not be empty")
	}

	if pred.SourceBed != 2 {
		t.Errorf("SourceBed = %d, want 2", pred.SourceBed)
	}

	t.Logf("PredictSpread error: %v", err)
}

func TestAbnormal_InvalidBedID_GracefulHandling(t *testing.T) {
	p := newPredictorWithBeds(10)

	pred1 := p.FallbackPrediction(0, "Unknown")

	if pred1 == nil {
		t.Fatal("FallbackPrediction(0) returned nil")
	}
	if len(pred1.Path) == 0 {
		t.Error("FallbackPrediction(0) returned empty Path")
	}
	if !pred1.IsFallback {
		t.Error("FallbackPrediction(0) IsFallback should be true")
	}
	if pred1.SpreadProb < 0.3 || pred1.SpreadProb > 1.0 {
		t.Errorf("FallbackPrediction(0) SpreadProb = %.4f, want in [0.3, 1.0]", pred1.SpreadProb)
	}

	pred2 := p.FallbackPrediction(999, "Unknown")

	if pred2 == nil {
		t.Fatal("FallbackPrediction(999) returned nil")
	}
	if len(pred2.Path) == 0 {
		t.Error("FallbackPrediction(999) returned empty Path")
	}
	if !pred2.IsFallback {
		t.Error("FallbackPrediction(999) IsFallback should be true")
	}
	if pred2.SpreadProb < 0.3 || pred2.SpreadProb > 1.0 {
		t.Errorf("FallbackPrediction(999) SpreadProb = %.4f, want in [0.3, 1.0]", pred2.SpreadProb)
	}
}

func TestAbnormal_ConcurrentUpdateAndPredict(t *testing.T) {
	p := newPredictorWithBeds(20)

	var wg sync.WaitGroup
	done := make(chan struct{})

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(bedID uint32) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					now := time.Now()
					p.UpdateCultureResult(models.CultureResult{
						ID:           bedID,
						BedID:        bedID,
						BacteriaName: "MRSA",
						Result:       "positive",
						CollectedAt:  now,
						ReportedAt:   now,
					})
					time.Sleep(10 * time.Millisecond)
				}
			}
		}(uint32(i + 1))
	}

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(bedID uint32) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					pred := p.FallbackPrediction(bedID, "MRSA")
					if pred != nil {
						p.cachePrediction(pred)
					}
					time.Sleep(15 * time.Millisecond)
				}
			}
		}(uint32(i + 1))
	}

	time.Sleep(2 * time.Second)
	close(done)
	wg.Wait()

	allPreds := p.GetAllPredictions()
	if len(allPreds) < 1 {
		t.Errorf("GetAllPredictions len = %d, want >= 1", len(allPreds))
	}

	t.Logf("Final predictions count: %d", len(allPreds))
}

