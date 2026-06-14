package gnn_resistance

import (
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

func TestFix_AsyncCall_DoesNotBlockCaller(t *testing.T) {
	p := newPredictorWithBeds(10)

	now := time.Now()
	p.UpdateCultureResult(models.CultureResult{
		ID:           1,
		BedID:        3,
		BacteriaName: "MRSA",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	start := time.Now()
	resultChan := p.PredictSpreadAsync(3, "MRSA")
	elapsed := time.Since(start)

	if elapsed >= 100*time.Millisecond {
		t.Errorf("PredictSpreadAsync took %v, want < 100ms (should not block)", elapsed)
	}

	if resultChan == nil {
		t.Fatal("PredictSpreadAsync returned nil channel")
	}

	pending := p.GetPendingCount()
	if pending < 1 {
		t.Errorf("GetPendingCount() = %d, want >= 1 (request should be in progress)", pending)
	}
}

func TestFix_TimeoutFallback_ReturnsWithinDeadline(t *testing.T) {
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

	start := time.Now()
	pred := p.PredictSpreadAsyncWithFallback(2, "MRSA")
	elapsed := time.Since(start)

	if elapsed >= 2500*time.Millisecond {
		t.Errorf("PredictSpreadAsyncWithFallback took %v, want < 2500ms", elapsed)
	}

	if pred == nil {
		t.Fatal("PredictSpreadAsyncWithFallback returned nil")
	}

	if !pred.IsFallback {
		t.Error("pred.IsFallback should be true for timeout fallback")
	}

	if pred.SpreadProb < 0.3 || pred.SpreadProb > 1.0 {
		t.Errorf("pred.SpreadProb = %.4f, want in [0.3, 1.0]", pred.SpreadProb)
	}

	p99 := p.GetP99Latency()
	if p99 > 3000 {
		t.Errorf("GetP99Latency() = %dms, want <= 3000ms", p99)
	}
}

func TestFix_P99LatencyTracking_RecordsCorrectly(t *testing.T) {
	p := newPredictorWithBeds(10)

	now := time.Now()
	for i := 0; i < 20; i++ {
		p.UpdateCultureResult(models.CultureResult{
			ID:           uint32(i + 1),
			BedID:        uint32((i % 5) + 1),
			BacteriaName: "MRSA",
			Result:       "positive",
			CollectedAt:  now.Add(time.Duration(i) * time.Second),
			ReportedAt:   now.Add(time.Duration(i) * time.Second),
		})
	}

	for i := 0; i < 20; i++ {
		bedID := uint32((i % 5) + 1)
		p.PredictSpreadAsyncWithFallback(bedID, "MRSA")
	}

	stats := p.GetLatencyStats()
	if stats["count"] != 20 {
		t.Errorf("GetLatencyStats()[\"count\"] = %d, want 20", stats["count"])
	}

	p99 := p.GetP99Latency()
	if p99 >= 1000 {
		t.Errorf("GetP99Latency() = %dms, want < 1000ms (fallback should be fast)", p99)
	}

	p50 := stats["p50"]
	p95 := stats["p95"]
	p99Stat := stats["p99"]
	if !(p50 <= p95 && p95 <= p99Stat) {
		t.Errorf("Latency percentile order incorrect: p50=%d, p95=%d, p99=%d, want p50 <= p95 <= p99", p50, p95, p99Stat)
	}
}

func TestFix_WaitForPrediction_DeadlineSemantics(t *testing.T) {
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
		BedID:        5,
		BacteriaName: "CRE",
		Result:       "positive",
		CollectedAt:  now,
		ReportedAt:   now,
	})

	resultChan := p.PredictSpreadAsync(5, "CRE")

	start := time.Now()
	pred := p.WaitForPrediction(resultChan, 5, "CRE", 500)
	elapsed := time.Since(start)

	if elapsed >= 800*time.Millisecond {
		t.Errorf("WaitForPrediction took %v, want < 800ms", elapsed)
	}

	if pred == nil {
		t.Fatal("WaitForPrediction returned nil")
	}

	if !pred.IsFallback {
		t.Error("pred.IsFallback should be true after timeout")
	}

	if pred.SourceBed != 5 {
		t.Errorf("pred.SourceBed = %d, want 5", pred.SourceBed)
	}

	if pred.BacteriaName != "CRE" && pred.BacteriaName != "" {
		t.Errorf("pred.BacteriaName = %q, want \"CRE\" or \"\"", pred.BacteriaName)
	}
}

func TestFix_HTTPClientTimeout_ReducedTo2Seconds(t *testing.T) {
	p := newPredictorWithBeds(10)

	if p.httpClient.Timeout != 2*time.Second {
		t.Errorf("httpClient.Timeout = %v, want 2s", p.httpClient.Timeout)
	}
}

func TestFix_PredictAllSpread_NonBlockingConcurrent(t *testing.T) {
	p := newPredictorWithBeds(20)

	now := time.Now()
	for i := 1; i <= 5; i++ {
		p.UpdateCultureResult(models.CultureResult{
			ID:           uint32(i),
			BedID:        uint32(i),
			BacteriaName: "MRSA",
			Result:       "positive",
			CollectedAt:  now,
			ReportedAt:   now,
		})
	}

	start := time.Now()
	p.PredictAllSpread()
	elapsed := time.Since(start)

	if elapsed >= 500*time.Millisecond {
		t.Errorf("PredictAllSpread took %v, want < 500ms (should be non-blocking)", elapsed)
	}
}
