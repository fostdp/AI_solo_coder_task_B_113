package alert_ws

import (
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

func TestNewEngine(t *testing.T) {
	cfg := config.AlertConfig{
		SofaThreshold:       2.0,
		InfectionThreshold:  0.7,
		SMSGateway:          "http://localhost:9090/sms",
		DeduplicationWindow: 5,
	}

	sepsisIn := make(chan SepsisMsg, 10)
	infectionIn := make(chan InfectionMsg, 10)
	alertOut := make(chan models.Alert, 10)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)
	if e == nil {
		t.Fatal("NewEngine returned nil")
	}
	if e.SepsisIn == nil {
		t.Error("SepsisIn is nil")
	}
	if e.InfectionIn == nil {
		t.Error("InfectionIn is nil")
	}
	if e.AlertOut == nil {
		t.Error("AlertOut is nil")
	}
	if e.clients == nil {
		t.Error("clients map is nil")
	}
	if e.lastAlertTime == nil {
		t.Error("lastAlertTime map is nil")
	}
}

func TestEngine_StartStop(t *testing.T) {
	cfg := config.AlertConfig{
		SofaThreshold:       2.0,
		InfectionThreshold:  0.7,
		DeduplicationWindow: 1,
	}

	sepsisIn := make(chan SepsisMsg, 10)
	infectionIn := make(chan InfectionMsg, 10)
	alertOut := make(chan models.Alert, 10)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)

	e.Start()
	time.Sleep(50 * time.Millisecond)

	e.Stop()
	time.Sleep(50 * time.Millisecond)
}

func TestSeverityOf(t *testing.T) {
	cfg := config.AlertConfig{
		SofaThreshold: 2.0,
	}
	e := NewEngine(cfg, nil, nil, nil)

	tests := []struct {
		name      string
		trigger   float64
		threshold float64
		expected  string
	}{
		{"critical", 3.0, 2.0, "critical"},
		{"high", 2.5, 2.0, "high"},
		{"warning", 2.0, 2.0, "warning"},
		{"below threshold", 1.0, 2.0, "warning"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := e.severityOf(tt.trigger, tt.threshold)
			if result != tt.expected {
				t.Errorf("severityOf(%f, %f) = %s, want %s",
					tt.trigger, tt.threshold, result, tt.expected)
			}
		})
	}
}

func TestHashAlertTypeKey(t *testing.T) {
	e := NewEngine(config.AlertConfig{}, nil, nil, nil)

	key := e.hashAlertTypeKey(5, "sepsis")
	expected := "5:sepsis"
	if key != expected {
		t.Errorf("hashAlertTypeKey(5, sepsis) = %s, want %s", key, expected)
	}

	key2 := e.hashAlertTypeKey(123, "cre_infection")
	expected2 := "123:cre_infection"
	if key2 != expected2 {
		t.Errorf("hashAlertTypeKey(123, cre_infection) = %s, want %s", key2, expected2)
	}
}

func TestSepsisMsgStruct(t *testing.T) {
	now := time.Now()
	msg := SepsisMsg{
		BedID:       1,
		Probability: 0.85,
		SOFAScore:   3,
		Time:        now,
	}

	if msg.BedID != 1 {
		t.Error("BedID mismatch")
	}
	if msg.Probability != 0.85 {
		t.Error("Probability mismatch")
	}
	if msg.SOFAScore != 3 {
		t.Error("SOFAScore mismatch")
	}
	if msg.Time != now {
		t.Error("Time mismatch")
	}
}

func TestInfectionMsgStruct(t *testing.T) {
	now := time.Now()
	msg := InfectionMsg{
		BedID:    1,
		CRERisk:  0.75,
		MRSARisk: 0.65,
		Time:     now,
	}

	if msg.BedID != 1 {
		t.Error("BedID mismatch")
	}
	if msg.CRERisk != 0.75 {
		t.Error("CRERisk mismatch")
	}
	if msg.MRSARisk != 0.65 {
		t.Error("MRSARisk mismatch")
	}
	if msg.Time != now {
		t.Error("Time mismatch")
	}
}

func TestCheckAndTrigger_Sepsis(t *testing.T) {
	cfg := config.AlertConfig{
		SofaThreshold:       2.0,
		DeduplicationWindow: 0,
	}

	sepsisIn := make(chan SepsisMsg, 10)
	infectionIn := make(chan InfectionMsg, 10)
	alertOut := make(chan models.Alert, 10)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)

	e.checkAndTrigger(1, "sepsis", 3.0, 2.0, "Test sepsis alert")

	select {
	case alert := <-alertOut:
		if alert.BedID != 1 {
			t.Errorf("alert BedID = %d, want 1", alert.BedID)
		}
		if alert.AlertType != "sepsis" {
			t.Errorf("alert type = %s, want sepsis", alert.AlertType)
		}
		if alert.TriggerValue != 3.0 {
			t.Errorf("trigger value = %f, want 3.0", alert.TriggerValue)
		}
		if alert.Threshold != 2.0 {
			t.Errorf("threshold = %f, want 2.0", alert.Threshold)
		}
		if alert.Severity != "critical" {
			t.Errorf("severity = %s, want critical", alert.Severity)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for alert")
	}
}

func TestCheckAndTrigger_Infection(t *testing.T) {
	cfg := config.AlertConfig{
		InfectionThreshold:  0.7,
		DeduplicationWindow: 0,
	}

	sepsisIn := make(chan SepsisMsg, 10)
	infectionIn := make(chan InfectionMsg, 10)
	alertOut := make(chan models.Alert, 10)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)

	e.checkAndTrigger(2, "cre_infection", 0.85, 0.7, "Test CRE alert")

	select {
	case alert := <-alertOut:
		if alert.BedID != 2 {
			t.Errorf("alert BedID = %d, want 2", alert.BedID)
		}
		if alert.AlertType != "cre_infection" {
			t.Errorf("alert type = %s, want cre_infection", alert.AlertType)
		}
		if alert.Severity != "high" {
			t.Errorf("severity = %s, want high", alert.Severity)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for alert")
	}
}

func TestDeduplication(t *testing.T) {
	cfg := config.AlertConfig{
		SofaThreshold:       2.0,
		DeduplicationWindow: 5,
	}

	sepsisIn := make(chan SepsisMsg, 10)
	infectionIn := make(chan InfectionMsg, 10)
	alertOut := make(chan models.Alert, 10)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)

	e.checkAndTrigger(1, "sepsis", 3.0, 2.0, "First alert")

	select {
	case <-alertOut:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first alert not received")
	}

	e.checkAndTrigger(1, "sepsis", 3.5, 2.0, "Duplicate alert")

	select {
	case <-alertOut:
		t.Error("duplicate alert should have been deduplicated")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDeduplicationDifferentBeds(t *testing.T) {
	cfg := config.AlertConfig{
		SofaThreshold:       2.0,
		DeduplicationWindow: 5,
	}

	sepsisIn := make(chan SepsisMsg, 10)
	infectionIn := make(chan InfectionMsg, 10)
	alertOut := make(chan models.Alert, 10)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)

	e.checkAndTrigger(1, "sepsis", 3.0, 2.0, "Bed 1 alert")
	e.checkAndTrigger(2, "sepsis", 3.0, 2.0, "Bed 2 alert")

	count := 0
	timeout := time.After(200 * time.Millisecond)
loop:
	for {
		select {
		case <-alertOut:
			count++
		case <-timeout:
			break loop
		}
	}

	if count != 2 {
		t.Errorf("expected 2 alerts for different beds, got %d", count)
	}
}

func TestDeduplicationDifferentTypes(t *testing.T) {
	cfg := config.AlertConfig{
		InfectionThreshold:  0.7,
		DeduplicationWindow: 5,
	}

	sepsisIn := make(chan SepsisMsg, 10)
	infectionIn := make(chan InfectionMsg, 10)
	alertOut := make(chan models.Alert, 10)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)

	e.checkAndTrigger(1, "cre_infection", 0.8, 0.7, "CRE alert")
	e.checkAndTrigger(1, "mrsa_infection", 0.8, 0.7, "MRSA alert")

	count := 0
	timeout := time.After(200 * time.Millisecond)
loop:
	for {
		select {
		case <-alertOut:
			count++
		case <-timeout:
			break loop
		}
	}

	if count != 2 {
		t.Errorf("expected 2 alerts for different types, got %d", count)
	}
}

func TestHandleSepsisChannel(t *testing.T) {
	cfg := config.AlertConfig{
		SofaThreshold:       2.0,
		DeduplicationWindow: 0,
	}

	sepsisIn := make(chan SepsisMsg, 10)
	infectionIn := make(chan InfectionMsg, 10)
	alertOut := make(chan models.Alert, 10)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)
	e.Start()
	defer e.Stop()

	sepsisIn <- SepsisMsg{
		BedID:       1,
		Probability: 0.9,
		SOFAScore:   3,
		Time:        time.Now(),
	}

	select {
	case alert := <-alertOut:
		if alert.BedID != 1 {
			t.Errorf("alert BedID = %d, want 1", alert.BedID)
		}
		if alert.AlertType != "sepsis" {
			t.Errorf("alert type = %s, want sepsis", alert.AlertType)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timed out waiting for sepsis alert via channel")
	}
}

func TestHandleInfectionChannel(t *testing.T) {
	cfg := config.AlertConfig{
		InfectionThreshold:  0.7,
		DeduplicationWindow: 0,
	}

	sepsisIn := make(chan SepsisMsg, 10)
	infectionIn := make(chan InfectionMsg, 10)
	alertOut := make(chan models.Alert, 10)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)
	e.Start()
	defer e.Stop()

	infectionIn <- InfectionMsg{
		BedID:    2,
		CRERisk:  0.85,
		MRSARisk: 0.6,
		Time:     time.Now(),
	}

	select {
	case alert := <-alertOut:
		if alert.BedID != 2 {
			t.Errorf("alert BedID = %d, want 2", alert.BedID)
		}
		if alert.AlertType != "cre_infection" {
			t.Errorf("alert type = %s, want cre_infection", alert.AlertType)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timed out waiting for infection alert via channel")
	}
}

func TestHandleInfectionDualAlert(t *testing.T) {
	cfg := config.AlertConfig{
		InfectionThreshold:  0.5,
		DeduplicationWindow: 0,
	}

	sepsisIn := make(chan SepsisMsg, 10)
	infectionIn := make(chan InfectionMsg, 10)
	alertOut := make(chan models.Alert, 10)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)
	e.Start()
	defer e.Stop()

	infectionIn <- InfectionMsg{
		BedID:    3,
		CRERisk:  0.8,
		MRSARisk: 0.7,
		Time:     time.Now(),
	}

	count := 0
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case <-alertOut:
			count++
		case <-timeout:
			break loop
		}
	}

	if count != 2 {
		t.Errorf("expected 2 alerts (CRE + MRSA), got %d", count)
	}
}

func TestBroadcastRiskUpdate(t *testing.T) {
	cfg := config.AlertConfig{}
	e := NewEngine(cfg, nil, nil, nil)

	testData := []map[string]interface{}{
		{"bed_id": 1, "risk": 0.5},
		{"bed_id": 2, "risk": 0.8},
	}

	e.BroadcastRiskUpdate(testData)
}

func TestGlobalEngineInstance(t *testing.T) {
	if EngineInstance != nil {
		t.Log("EngineInstance already set (from prior tests)")
		return
	}

	cfg := config.AlertConfig{}
	sepsisIn := make(chan SepsisMsg)
	infectionIn := make(chan InfectionMsg)
	alertOut := make(chan models.Alert)

	e := NewEngine(cfg, sepsisIn, infectionIn, alertOut)
	EngineInstance = e

	if EngineInstance != e {
		t.Error("EngineInstance not set correctly")
	}

	EngineInstance = nil
}

func TestAlertOutputChannelNil(t *testing.T) {
	cfg := config.AlertConfig{
		SofaThreshold:       2.0,
		DeduplicationWindow: 0,
	}

	e := NewEngine(cfg, nil, nil, nil)
	e.AlertOut = nil

	e.checkAndTrigger(1, "sepsis", 3.0, 2.0, "Test with nil channel")
}
