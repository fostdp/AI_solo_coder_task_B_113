package transport_rf

import (
	"math"
	"sync"
	"testing"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

type mockVeryLowVital struct{}

func (m *mockVeryLowVital) GetVitalStabilityScore(bedID uint32) float64 { return 20.0 }

type mockVeryHighInfection struct{}

func (m *mockVeryHighInfection) GetInfectionRisk(bedID uint32) float64 { return 0.95 }

type mockStableVital struct{}

func (m *mockStableVital) GetVitalStabilityScore(bedID uint32) float64 { return 90.0 }

type mockLowInfection struct{}

func (m *mockLowInfection) GetInfectionRisk(bedID uint32) float64 { return 0.1 }

type mockNegativeVital struct{}

func (m *mockNegativeVital) GetVitalStabilityScore(bedID uint32) float64 { return -50.0 }

type mockOverflowInfection struct{}

func (m *mockOverflowInfection) GetInfectionRisk(bedID uint32) float64 { return 5.0 }

type mockMaxVital struct{}

func (m *mockMaxVital) GetVitalStabilityScore(bedID uint32) float64 { return 100.0 }

type mockZeroInfection struct{}

func (m *mockZeroInfection) GetInfectionRisk(bedID uint32) float64 { return 0.0 }

type mockMinVital struct{}

func (m *mockMinVital) GetVitalStabilityScore(bedID uint32) float64 { return 0.0 }

type mockMaxInfection struct{}

func (m *mockMaxInfection) GetInfectionRisk(bedID uint32) float64 { return 1.0 }

func setupScenarioScorer() *TransportScorer {
	cfg := config.TransportConfig{
		ForestTrees:    50,
		ScoreThreshold: 60,
	}
	inChan := make(chan models.TransportRequest, 100)
	outChan := make(chan models.TransportRiskResult, 100)
	return NewTransportScorer(cfg, inChan, outChan)
}

func TestNormal_HighSOFAPlusLongDistance_ScoreAbove80(t *testing.T) {
	scorer := setupScenarioScorer()
	scorer.SetProviders(&mockVeryLowVital{}, &mockVeryHighInfection{})

	totalScore := 0
	totalProb := 0.0
	nTrials := 100

	for i := 0; i < nTrials; i++ {
		req := models.TransportRequest{
			RequestID:  uint32(i + 1),
			BedID:      1,
			FromBedID:  1,
			ToBedID:    10,
			Distance:   4500,
			Urgent:     true,
			Priority:   3,
			HourOfDay:  12,
			PatientAge: 65,
		}
		result, err := scorer.ScoreRequest(req)
		if err != nil {
			t.Fatalf("ScoreRequest returned error: %v", err)
		}
		totalScore += result.RiskScore
		totalProb += result.AdverseEventProb
	}

	avgScore := float64(totalScore) / float64(nTrials)
	avgProb := totalProb / float64(nTrials)
	expectedProb := avgScore / 100.0 * 0.4

	t.Logf("Average score: %.2f", avgScore)
	t.Logf("Average prob: %.4f, expected prob: %.4f", avgProb, expectedProb)

	if avgScore <= 60 {
		t.Errorf("Expected average score > 60, got %.2f", avgScore)
	}

	if math.Abs(avgProb-expectedProb) >= 0.01 {
		t.Errorf("AdverseEventProb mismatch: got %.4f, expected %.4f (error >= 0.01)", avgProb, expectedProb)
	}
}

func TestNormal_MultipleRiskFactors_SynergisticEffect(t *testing.T) {
	scorerA := setupScenarioScorer()
	scorerA.SetProviders(&mockVeryLowVital{}, &mockVeryHighInfection{})

	scorerB := setupScenarioScorer()
	scorerB.SetProviders(&mockStableVital{}, &mockLowInfection{})

	scorerC := setupScenarioScorer()
	scorerC.SetProviders(&mockVitalProvider{}, &mockInfectionProvider{})

	nTrials := 100
	totalA, totalB, totalC := 0, 0, 0

	for i := 0; i < nTrials; i++ {
		reqA := models.TransportRequest{
			RequestID:  uint32(i + 1),
			BedID:      1,
			Distance:   4000,
			Urgent:     true,
			Priority:   3,
			HourOfDay:  12,
			PatientAge: 70,
		}
		resA, _ := scorerA.ScoreRequest(reqA)
		totalA += resA.RiskScore

		reqB := models.TransportRequest{
			RequestID:  uint32(i + 1000),
			BedID:      2,
			Distance:   100,
			Urgent:     false,
			Priority:   1,
			HourOfDay:  12,
			PatientAge: 30,
		}
		resB, _ := scorerB.ScoreRequest(reqB)
		totalB += resB.RiskScore

		reqC := models.TransportRequest{
			RequestID:  uint32(i + 2000),
			BedID:      3,
			Distance:   2000,
			Urgent:     false,
			Priority:   2,
			HourOfDay:  12,
			PatientAge: 50,
		}
		resC, _ := scorerC.ScoreRequest(reqC)
		totalC += resC.RiskScore
	}

	avgA := float64(totalA) / float64(nTrials)
	avgB := float64(totalB) / float64(nTrials)
	avgC := float64(totalC) / float64(nTrials)

	t.Logf("Group A avg: %.2f, Group B avg: %.2f, Group C avg: %.2f", avgA, avgB, avgC)

	if !(avgA > avgC && avgC > avgB) {
		t.Errorf("Expected avg(A) > avg(C) > avg(B), got %.2f > %.2f > %.2f", avgA, avgC, avgB)
	}

	if avgA < 0 || avgA > 100 {
		t.Errorf("Group A average %.2f out of range [0,100]", avgA)
	}
	if avgB < 0 || avgB > 100 {
		t.Errorf("Group B average %.2f out of range [0,100]", avgB)
	}
	if avgC < 0 || avgC > 100 {
		t.Errorf("Group C average %.2f out of range [0,100]", avgC)
	}
}

func TestNormal_FeatureContribReflectsRiskFactors(t *testing.T) {
	scorer := setupScenarioScorer()
	scorer.SetProviders(&mockVeryLowVital{}, &mockVeryHighInfection{})

	req := models.TransportRequest{
		RequestID:  1,
		BedID:      1,
		Distance:   4000,
		Urgent:     true,
		Priority:   3,
		HourOfDay:  12,
		PatientAge: 60,
	}

	result, err := scorer.ScoreRequest(req)
	if err != nil {
		t.Fatalf("ScoreRequest returned error: %v", err)
	}

	featureNames := []string{"vital_stability", "infection_risk", "distance", "urgent", "priority", "day_hours"}
	sum := 0.0

	for _, name := range featureNames {
		contrib, ok := result.FeatureContrib[name]
		if !ok {
			t.Errorf("FeatureContrib missing %s", name)
			continue
		}
		if contrib < 0 {
			t.Errorf("FeatureContrib[%s] = %.4f, expected >= 0", name, contrib)
		}
		sum += contrib
		t.Logf("FeatureContrib[%s] = %.4f", name, contrib)
	}

	t.Logf("Total sum of contributions: %.4f", sum)

	if math.Abs(sum-1.0) >= 0.1 {
		t.Errorf("Sum of feature contributions = %.4f, expected ≈ 1.0 (tolerance 0.1)", sum)
	}
}

func TestNormal_RecommendationsTriggeredCorrectly(t *testing.T) {
	scorer := setupScenarioScorer()

	mockVital40 := &mockLowVitalProvider{}
	mockInf80 := &mockHighInfectionProvider{}
	scorer.SetProviders(mockVital40, mockInf80)

	req1 := models.TransportRequest{
		RequestID:  1,
		BedID:      1,
		Distance:   2000,
		Urgent:     false,
		Priority:   2,
		HourOfDay:  10,
		PatientAge: 50,
	}

	result1, err := scorer.ScoreRequest(req1)
	if err != nil {
		t.Fatalf("ScoreRequest returned error: %v", err)
	}

	t.Logf("Test case 1 recommendations: %v", result1.Recommendations)

	expectedRecs := []string{"先稳定生命体征", "加强防护装备", "选择最短路径", "建议错峰转运"}
	for _, rec := range expectedRecs {
		found := false
		for _, r := range result1.Recommendations {
			if r == rec {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected recommendation '%s' not found in %v", rec, result1.Recommendations)
		}
	}

	scorer2 := setupScenarioScorer()
	scorer2.SetProviders(&mockStableVital{}, &mockLowInfection{})

	req2 := models.TransportRequest{
		RequestID:  2,
		BedID:      2,
		Distance:   200,
		Urgent:     true,
		Priority:   1,
		HourOfDay:  14,
		PatientAge: 30,
	}

	result2, err := scorer2.ScoreRequest(req2)
	if err != nil {
		t.Fatalf("ScoreRequest returned error: %v", err)
	}

	t.Logf("Test case 2 recommendations: %v", result2.Recommendations)

	unexpectedRecs := []string{"先稳定生命体征", "加强防护装备", "选择最短路径", "建议错峰转运"}
	for _, rec := range unexpectedRecs {
		for _, r := range result2.Recommendations {
			if r == rec {
				t.Errorf("Unexpected recommendation '%s' found in %v", rec, result2.Recommendations)
			}
		}
	}
}

func TestBoundary_ZeroDistance_ScoreDependsOnVitals(t *testing.T) {
	scorerA := setupScenarioScorer()
	scorerA.SetProviders(&mockVeryLowVital{}, &mockVeryHighInfection{})

	scorerB := setupScenarioScorer()
	scorerB.SetProviders(&mockStableVital{}, &mockLowInfection{})

	nTrials := 100
	totalA, totalB := 0, 0

	for i := 0; i < nTrials; i++ {
		req := models.TransportRequest{
			RequestID:  uint32(i + 1),
			BedID:      1,
			Distance:   0,
			Urgent:     false,
			Priority:   2,
			HourOfDay:  12,
			PatientAge: 50,
		}

		resA, _ := scorerA.ScoreRequest(req)
		totalA += resA.RiskScore

		req.RequestID = uint32(i + 1000)
		resB, _ := scorerB.ScoreRequest(req)
		totalB += resB.RiskScore
	}

	avgA := float64(totalA) / float64(nTrials)
	avgB := float64(totalB) / float64(nTrials)
	diff := avgA - avgB

	t.Logf("Group A avg: %.2f, Group B avg: %.2f, difference: %.2f", avgA, avgB, diff)

	if !(avgA > avgB) {
		t.Errorf("Expected avg(A) > avg(B), got %.2f vs %.2f", avgA, avgB)
	}

	if diff <= 10 {
		t.Errorf("Expected difference > 10, got %.2f", diff)
	}

	if avgA < 0 || avgA > 100 {
		t.Errorf("Group A average %.2f out of range [0,100]", avgA)
	}
	if avgB < 0 || avgB > 100 {
		t.Errorf("Group B average %.2f out of range [0,100]", avgB)
	}
}

func TestBoundary_MinimumVitals_LowRiskScore(t *testing.T) {
	scorer := setupScenarioScorer()
	scorer.SetProviders(&mockMaxVital{}, &mockZeroInfection{})

	nTrials := 100
	totalScore := 0

	for i := 0; i < nTrials; i++ {
		req := models.TransportRequest{
			RequestID:  uint32(i + 1),
			BedID:      1,
			Distance:   0,
			Urgent:     false,
			Priority:   1,
			HourOfDay:  12,
			PatientAge: 30,
		}
		result, _ := scorer.ScoreRequest(req)
		totalScore += result.RiskScore

		if i == 0 {
			t.Logf("First result recommendations: %v", result.Recommendations)
			hasValidRec := false
			for _, rec := range result.Recommendations {
				if rec == "建议错峰转运" || rec == "按标准流程转运" {
					hasValidRec = true
					break
				}
			}
			if !hasValidRec {
				t.Errorf("Expected recommendations to contain '建议错峰转运' or '按标准流程转运', got %v", result.Recommendations)
			}
		}
	}

	avgScore := float64(totalScore) / float64(nTrials)
	t.Logf("Average score: %.2f", avgScore)

	if avgScore >= 50 {
		t.Errorf("Expected average score < 50, got %.2f", avgScore)
	}
}

func TestBoundary_MaximumRisk_FactorsAtCeiling(t *testing.T) {
	scorer := setupScenarioScorer()
	scorer.SetProviders(&mockMinVital{}, &mockMaxInfection{})

	nTrials := 100
	totalScore := 0
	highOrCriticalCount := 0

	for i := 0; i < nTrials; i++ {
		req := models.TransportRequest{
			RequestID:  uint32(i + 1),
			BedID:      1,
			Distance:   5000,
			Urgent:     true,
			Priority:   3,
			HourOfDay:  12,
			PatientAge: 75,
		}
		result, _ := scorer.ScoreRequest(req)
		totalScore += result.RiskScore

		if result.RiskLevel == "high" || result.RiskLevel == "critical" {
			highOrCriticalCount++
		}
	}

	avgScore := float64(totalScore) / float64(nTrials)
	highOrCriticalRatio := float64(highOrCriticalCount) / float64(nTrials)

	t.Logf("Average score: %.2f", avgScore)
	t.Logf("High/Critical ratio: %.2f%%", highOrCriticalRatio*100)

	if avgScore < 60 {
		t.Errorf("Expected average score >= 60, got %.2f", avgScore)
	}

	if highOrCriticalRatio <= 0.5 {
		t.Errorf("Expected high/critical ratio > 50%%, got %.2f%%", highOrCriticalRatio*100)
	}
}

func TestBoundary_SameRequestDeterministic_WithSameForest(t *testing.T) {
	scorer := setupScenarioScorer()
	scorer.SetProviders(&mockVitalProvider{}, &mockInfectionProvider{})

	req := models.TransportRequest{
		RequestID:  42,
		BedID:      5,
		FromBedID:  1,
		ToBedID:    10,
		Distance:   2500,
		Urgent:     true,
		Priority:   2,
		HourOfDay:  14,
		PatientAge: 55,
	}

	result1, err1 := scorer.ScoreRequest(req)
	if err1 != nil {
		t.Fatalf("First ScoreRequest returned error: %v", err1)
	}

	result2, err2 := scorer.ScoreRequest(req)
	if err2 != nil {
		t.Fatalf("Second ScoreRequest returned error: %v", err2)
	}

	t.Logf("First score: %d, Second score: %d", result1.RiskScore, result2.RiskScore)

	if result1.RiskScore != result2.RiskScore {
		t.Errorf("Scores not deterministic: first=%d, second=%d", result1.RiskScore, result2.RiskScore)
	}
}

func TestAbnormal_GPSLost_DefaultDistanceUsed(t *testing.T) {
	scorer := setupScenarioScorer()
	scorer.SetProviders(&mockVitalProvider{}, &mockInfectionProvider{})

	req := models.TransportRequest{
		RequestID:  1,
		BedID:      1,
		Distance:   0,
		Urgent:     false,
		Priority:   2,
		HourOfDay:  12,
		PatientAge: 50,
	}

	result, err := scorer.ScoreRequest(req)
	if err != nil {
		t.Fatalf("ScoreRequest returned error: %v", err)
	}

	t.Logf("RiskScore: %d", result.RiskScore)
	t.Logf("Recommendations: %v", result.Recommendations)

	if result.RiskScore < 0 || result.RiskScore > 100 {
		t.Errorf("RiskScore out of range [0,100]: %d", result.RiskScore)
	}

	for _, rec := range result.Recommendations {
		if rec == "选择最短路径" {
			t.Errorf("Unexpected recommendation '选择最短路径' when Distance=0")
		}
	}
}

func TestAbnormal_NilProviders_DefaultValuesUsed(t *testing.T) {
	scorer := setupScenarioScorer()

	req := models.TransportRequest{
		RequestID:  1,
		BedID:      1,
		Distance:   2000,
		Urgent:     true,
		Priority:   2,
		HourOfDay:  12,
		PatientAge: 50,
	}

	result, err := scorer.ScoreRequest(req)
	if err != nil {
		t.Fatalf("ScoreRequest returned error: %v", err)
	}

	t.Logf("RiskScore: %d", result.RiskScore)
	t.Logf("FeatureContrib: %v", result.FeatureContrib)

	if result.RiskScore < 0 || result.RiskScore > 100 {
		t.Errorf("RiskScore out of range [0,100]: %d", result.RiskScore)
	}

	vitalContrib, ok := result.FeatureContrib["vital_stability"]
	if !ok {
		t.Error("FeatureContrib missing vital_stability")
	} else if vitalContrib < 0 {
		t.Errorf("vital_stability contribution should be >= 0, got %.4f", vitalContrib)
	}

	infContrib, ok := result.FeatureContrib["infection_risk"]
	if !ok {
		t.Error("FeatureContrib missing infection_risk")
	} else if infContrib < 0 {
		t.Errorf("infection_risk contribution should be >= 0, got %.4f", infContrib)
	}
}

func TestAbnormal_ExtremeFeatureValues_NoPanic(t *testing.T) {
	scorer := setupScenarioScorer()
	scorer.SetProviders(&mockNegativeVital{}, &mockOverflowInfection{})

	req := models.TransportRequest{
		RequestID:  1,
		BedID:      1,
		Distance:   10000,
		Urgent:     true,
		Priority:   3,
		HourOfDay:  25,
		PatientAge: 50,
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ScoreRequest panicked with: %v", r)
		}
	}()

	result, err := scorer.ScoreRequest(req)
	if err != nil {
		t.Fatalf("ScoreRequest returned error: %v", err)
	}

	t.Logf("RiskScore: %d", result.RiskScore)

	if result.RiskScore < 0 || result.RiskScore > 100 {
		t.Errorf("RiskScore out of range [0,100]: %d", result.RiskScore)
	}
}

func TestAbnormal_ConcurrentScoring_NoDataRace(t *testing.T) {
	scorer := setupScenarioScorer()
	scorer.SetProviders(&mockVitalProvider{}, &mockInfectionProvider{})

	nGoroutines := 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([]*models.TransportRiskResult, 0, nGoroutines)
	errors := make([]error, 0)

	for i := 0; i < nGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					errors = append(errors, r.(error))
					mu.Unlock()
				}
			}()

			req := models.TransportRequest{
				RequestID:  uint32(idx + 1),
				BedID:      uint32(idx + 1),
				Distance:   float64(idx*500 + 500),
				Urgent:     idx%2 == 0,
				Priority:   (idx % 3) + 1,
				HourOfDay:  (idx * 2) % 24,
				PatientAge: 30 + idx*5,
			}

			result, err := scorer.ScoreRequest(req)
			mu.Lock()
			if err != nil {
				errors = append(errors, err)
			} else {
				results = append(results, result)
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	if len(errors) > 0 {
		t.Fatalf("Errors during concurrent scoring: %v", errors)
	}

	latestResults := scorer.GetAllResults()
	t.Logf("LatestResults length: %d", len(latestResults))

	if len(latestResults) != nGoroutines {
		t.Errorf("Expected LatestResults length = %d, got %d", nGoroutines, len(latestResults))
	}

	for reqID, result := range latestResults {
		if result.RiskScore < 0 || result.RiskScore > 100 {
			t.Errorf("Request %d: RiskScore out of range [0,100]: %d", reqID, result.RiskScore)
		}
	}
}
