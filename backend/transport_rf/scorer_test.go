package transport_rf

import (
	"math/rand"
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

type mockVitalProvider struct{}

func (m *mockVitalProvider) GetVitalStabilityScore(bedID uint32) float64 {
	return 50.0 + rand.Float64()*30
}

type mockInfectionProvider struct{}

func (m *mockInfectionProvider) GetInfectionRisk(bedID uint32) float64 {
	return 0.2 + rand.Float64()*0.5
}

type mockLowVitalProvider struct{}

func (m *mockLowVitalProvider) GetVitalStabilityScore(bedID uint32) float64 {
	return 30.0
}

type mockHighInfectionProvider struct{}

func (m *mockHighInfectionProvider) GetInfectionRisk(bedID uint32) float64 {
	return 0.9
}

func setupTestScorer() (*TransportScorer, chan models.TransportRequest, chan models.TransportRiskResult) {
	cfg := config.TransportConfig{
		ForestTrees:    50,
		ScoreThreshold: 60,
	}
	inChan := make(chan models.TransportRequest, 100)
	outChan := make(chan models.TransportRiskResult, 100)
	scorer := NewTransportScorer(cfg, inChan, outChan)
	return scorer, inChan, outChan
}

func TestNewTransportScorer(t *testing.T) {
	scorer, _, _ := setupTestScorer()

	if scorer == nil {
		t.Fatal("NewTransportScorer returned nil")
	}
	if scorer.cfg.ForestTrees != 50 {
		t.Errorf("Expected ForestTrees=50, got %d", scorer.cfg.ForestTrees)
	}
	if len(scorer.trees) != 50 {
		t.Errorf("Expected 50 trees, got %d", len(scorer.trees))
	}
	if scorer.trees[0] == nil || scorer.trees[0].Root == nil {
		t.Error("First tree root is nil")
	}
	if scorer.LatestResults == nil {
		t.Error("LatestResults map is nil")
	}
	if scorer.stopChan == nil {
		t.Error("stopChan is nil")
	}
}

func TestScoreOutputRange(t *testing.T) {
	scorer, _, _ := setupTestScorer()
	rand.Seed(time.Now().UnixNano())

	for i := 0; i < 100; i++ {
		req := models.TransportRequest{
			RequestID:  uint32(i + 1),
			BedID:      uint32(rand.Intn(50) + 1),
			FromBedID:  1,
			ToBedID:    2,
			Distance:   rand.Float64() * 4000,
			Urgent:     rand.Intn(2) == 1,
			Priority:   rand.Intn(3) + 1,
			HourOfDay:  rand.Intn(24),
			PatientAge: 30 + rand.Intn(50),
		}
		result, err := scorer.ScoreRequest(req)
		if err != nil {
			t.Fatalf("ScoreRequest returned error: %v", err)
		}
		if result.RiskScore < 0 || result.RiskScore > 100 {
			t.Errorf("RiskScore out of range [0,100]: got %d for request %d", result.RiskScore, req.RequestID)
		}
	}
}

func TestUrgentIncreasesRisk(t *testing.T) {
	scorer, _, _ := setupTestScorer()
	rand.Seed(42)

	urgentTotal := 0
	nonUrgentTotal := 0
	nTrials := 200

	for i := 0; i < nTrials; i++ {
		reqUrgent := models.TransportRequest{
			RequestID:  uint32(i*2 + 1),
			BedID:      5,
			FromBedID:  1,
			ToBedID:    10,
			Distance:   2000,
			Urgent:     true,
			Priority:   2,
			HourOfDay:  12,
			PatientAge: 50,
		}
		resU, _ := scorer.ScoreRequest(reqUrgent)
		urgentTotal += resU.RiskScore

		reqNonUrgent := models.TransportRequest{
			RequestID:  uint32(i*2 + 2),
			BedID:      5,
			FromBedID:  1,
			ToBedID:    10,
			Distance:   2000,
			Urgent:     false,
			Priority:   2,
			HourOfDay:  12,
			PatientAge: 50,
		}
		resN, _ := scorer.ScoreRequest(reqNonUrgent)
		nonUrgentTotal += resN.RiskScore
	}

	urgentAvg := float64(urgentTotal) / float64(nTrials)
	nonUrgentAvg := float64(nonUrgentTotal) / float64(nTrials)

	t.Logf("Urgent avg: %.2f, Non-urgent avg: %.2f", urgentAvg, nonUrgentAvg)

	if urgentAvg <= nonUrgentAvg {
		t.Errorf("Expected urgent risk (%.2f) > non-urgent risk (%.2f)", urgentAvg, nonUrgentAvg)
	}
}

func TestDistanceAffectsRisk(t *testing.T) {
	scorer, _, _ := setupTestScorer()
	rand.Seed(123)

	nearTotal := 0
	farTotal := 0
	nTrials := 200

	for i := 0; i < nTrials; i++ {
		reqNear := models.TransportRequest{
			RequestID:  uint32(i*2 + 1),
			BedID:      3,
			FromBedID:  1,
			ToBedID:    2,
			Distance:   100,
			Urgent:     false,
			Priority:   1,
			HourOfDay:  10,
			PatientAge: 40,
		}
		resN, _ := scorer.ScoreRequest(reqNear)
		nearTotal += resN.RiskScore

		reqFar := models.TransportRequest{
			RequestID:  uint32(i*2 + 2),
			BedID:      3,
			FromBedID:  1,
			ToBedID:    50,
			Distance:   4500,
			Urgent:     false,
			Priority:   1,
			HourOfDay:  10,
			PatientAge: 40,
		}
		resF, _ := scorer.ScoreRequest(reqFar)
		farTotal += resF.RiskScore
	}

	nearAvg := float64(nearTotal) / float64(nTrials)
	farAvg := float64(farTotal) / float64(nTrials)

	t.Logf("Near avg: %.2f, Far avg: %.2f", nearAvg, farAvg)

	if farAvg <= nearAvg {
		t.Errorf("Expected far distance risk (%.2f) > near distance risk (%.2f)", farAvg, nearAvg)
	}
}

func TestInfectionAffectsRisk(t *testing.T) {
	scorer1, _, _ := setupTestScorer()
	scorer2, _, _ := setupTestScorer()

	scorer1.SetProviders(&mockVitalProvider{}, &mockInfectionProvider{})
	scorer2.SetProviders(&mockVitalProvider{}, &mockHighInfectionProvider{})

	lowInfTotal := 0
	highInfTotal := 0
	nTrials := 100

	for i := 0; i < nTrials; i++ {
		req := models.TransportRequest{
			RequestID:  uint32(i + 1),
			BedID:      7,
			FromBedID:  1,
			ToBedID:    5,
			Distance:   1500,
			Urgent:     false,
			Priority:   2,
			HourOfDay:  14,
			PatientAge: 60,
		}
		resLow, _ := scorer1.ScoreRequest(req)
		lowInfTotal += resLow.RiskScore

		resHigh, _ := scorer2.ScoreRequest(req)
		highInfTotal += resHigh.RiskScore
	}

	lowAvg := float64(lowInfTotal) / float64(nTrials)
	highAvg := float64(highInfTotal) / float64(nTrials)

	t.Logf("Low infection avg: %.2f, High infection avg: %.2f", lowAvg, highAvg)

	if highAvg <= lowAvg {
		t.Errorf("Expected high infection risk (%.2f) > low infection risk (%.2f)", highAvg, lowAvg)
	}
}

func TestRecommendationsNotEmpty(t *testing.T) {
	scorer, _, _ := setupTestScorer()

	testCases := []models.TransportRequest{
		{
			RequestID:  1,
			BedID:      1,
			Distance:   500,
			Urgent:     true,
			Priority:   3,
			HourOfDay:  8,
			PatientAge: 30,
		},
		{
			RequestID:  2,
			BedID:      2,
			Distance:   2000,
			Urgent:     false,
			Priority:   1,
			HourOfDay:  3,
			PatientAge: 70,
		},
		{
			RequestID:  3,
			BedID:      3,
			Distance:   50,
			Urgent:     true,
			Priority:   2,
			HourOfDay:  12,
			PatientAge: 45,
		},
	}

	for _, tc := range testCases {
		result, err := scorer.ScoreRequest(tc)
		if err != nil {
			t.Fatalf("ScoreRequest failed for req %d: %v", tc.RequestID, err)
		}
		if len(result.Recommendations) == 0 {
			t.Errorf("Recommendations is empty for request %d", tc.RequestID)
		}
		t.Logf("Req %d recs: %v", tc.RequestID, result.Recommendations)
	}
}

func TestGetAndSet(t *testing.T) {
	scorer, _, _ := setupTestScorer()

	vsp := &mockVitalProvider{}
	ip := &mockInfectionProvider{}
	scorer.SetProviders(vsp, ip)

	if scorer.vitalStabilityProvider == nil {
		t.Error("vitalStabilityProvider not set")
	}
	if scorer.infectionProvider == nil {
		t.Error("infectionProvider not set")
	}

	req := models.TransportRequest{
		RequestID:  42,
		BedID:      5,
		FromBedID:  1,
		ToBedID:    10,
		Distance:   1000,
		Urgent:     true,
		Priority:   2,
		HourOfDay:  15,
		PatientAge: 55,
	}
	result, _ := scorer.ScoreRequest(req)

	got := scorer.GetResult(42)
	if got == nil {
		t.Fatal("GetResult returned nil for existing request")
	}
	if got.RequestID != 42 {
		t.Errorf("Expected RequestID=42, got %d", got.RequestID)
	}
	if got.RiskScore != result.RiskScore {
		t.Errorf("RiskScore mismatch: stored=%d, got=%d", result.RiskScore, got.RiskScore)
	}

	gotMissing := scorer.GetResult(999)
	if gotMissing != nil {
		t.Error("GetResult should return nil for non-existent request")
	}

	allResults := scorer.GetAllResults()
	if len(allResults) != 1 {
		t.Errorf("Expected 1 result in GetAllResults, got %d", len(allResults))
	}
	if _, ok := allResults[42]; !ok {
		t.Error("GetAllResults missing request 42")
	}
}

func TestGlobalSingleton(t *testing.T) {
	scorer1, _, _ := setupTestScorer()
	got1 := GetInstance()
	if got1 != scorer1 {
		t.Error("GetInstance() not returning first created scorer")
	}

	cfg := config.TransportConfig{ForestTrees: 30}
	in2 := make(chan models.TransportRequest, 100)
	out2 := make(chan models.TransportRiskResult, 100)
	scorer2 := NewTransportScorer(cfg, in2, out2)

	got2 := GetInstance()
	if got2 != scorer2 {
		t.Error("GetInstance() not returning latest created scorer")
	}
	if got2 == scorer1 {
		t.Error("GetInstance() should have updated to scorer2")
	}
	if len(scorer2.trees) != 30 {
		t.Errorf("Expected 30 trees in second scorer, got %d", len(scorer2.trees))
	}
}

func TestRiskLevelMapping(t *testing.T) {
	scorer, _, _ := setupTestScorer()
	rand.Seed(999)

	foundLevels := make(map[string]bool)
	reqID := uint32(1)

	for i := 0; i < 500; i++ {
		req := models.TransportRequest{
			RequestID:  reqID,
			BedID:      uint32(rand.Intn(20) + 1),
			Distance:   rand.Float64() * 5000,
			Urgent:     rand.Intn(2) == 1,
			Priority:   rand.Intn(3) + 1,
			HourOfDay:  rand.Intn(24),
			PatientAge: 20 + rand.Intn(70),
		}
		result, _ := scorer.ScoreRequest(req)
		foundLevels[result.RiskLevel] = true
		reqID++

		validLevels := map[string]bool{"low": true, "medium": true, "high": true, "critical": true}
		if !validLevels[result.RiskLevel] {
			t.Errorf("Invalid risk level: %s (score=%d)", result.RiskLevel, result.RiskScore)
		}

		switch {
		case result.RiskScore < 30 && result.RiskLevel != "low":
			t.Errorf("Score %d should be low, got %s", result.RiskScore, result.RiskLevel)
		case result.RiskScore >= 30 && result.RiskScore < 60 && result.RiskLevel != "medium":
			t.Errorf("Score %d should be medium, got %s", result.RiskScore, result.RiskLevel)
		case result.RiskScore >= 60 && result.RiskScore < 80 && result.RiskLevel != "high":
			t.Errorf("Score %d should be high, got %s", result.RiskScore, result.RiskLevel)
		case result.RiskScore >= 80 && result.RiskLevel != "critical":
			t.Errorf("Score %d should be critical, got %s", result.RiskScore, result.RiskLevel)
		}
	}

	t.Logf("Found risk levels: %v", foundLevels)
}

func TestStartStopChannels(t *testing.T) {
	scorer, inChan, outChan := setupTestScorer()

	scorer.Start()

	req := models.TransportRequest{
		RequestID:  100,
		BedID:      5,
		FromBedID:  1,
		ToBedID:    8,
		Distance:   1500,
		Urgent:     true,
		Priority:   2,
		HourOfDay:  11,
		PatientAge: 50,
	}
	inChan <- req

	time.Sleep(100 * time.Millisecond)

	select {
	case result := <-outChan:
		if result.RequestID != 100 {
			t.Errorf("Expected RequestID=100, got %d", result.RequestID)
		}
	default:
		t.Error("No result received on OutChan after sending request")
	}

	scorer.Stop()

	stopped := false
	select {
	case <-scorer.stopChan:
		stopped = true
	default:
	}
	if !stopped {
		t.Error("stopChan should be closed after Stop()")
	}
}
