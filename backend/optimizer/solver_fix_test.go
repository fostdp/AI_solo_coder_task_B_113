package optimizer

import (
	"math"
	"testing"
	"time"

	"field-hospital-icu/config"
)

func TestFix_L1Regularization_ReducesChangeRateBelow10Percent(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:              true,
		SolveIntervalSec:     10,
		NegativePressureBeds: 5,
		NursesPerShift:       3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	mock := &MockRiskProvider{
		risks: map[uint32]float64{},
	}
	for i := uint32(1); i <= 10; i++ {
		mock.risks[i] = 0.1 + float64(i-1)*0.85/9.0
	}
	opt.SetInfectionRiskProvider(mock)

	for i := uint32(1); i <= 10; i++ {
		opt.bedOccupancy[i] = true
	}

	opt.SolveAndBroadcast()
	sol1 := opt.GetLatestSolution()
	if sol1 == nil {
		t.Fatal("First solve returned nil solution")
	}
	rate1 := opt.GetLatestChangeRate()
	t.Logf("First solve change rate: %.4f", rate1)

	time.Sleep(10 * time.Millisecond)

	opt.SolveAndBroadcast()
	rate2 := opt.GetLatestChangeRate()
	t.Logf("Second solve change rate: %.4f", rate2)

	if rate2 >= 0.1 {
		t.Errorf("Second solve change rate = %.4f, want < 0.1", rate2)
	}
	if rate2 >= rate1 {
		t.Errorf("Second solve change rate %.4f should be less than first %.4f", rate2, rate1)
	}
}

func TestFix_StabilityConstraint_PreservesPreviousAssignments(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)
	opt.SetStabilityLambda(1.0)

	mock := &MockRiskProvider{
		risks: map[uint32]float64{},
	}
	for i := uint32(1); i <= 5; i++ {
		mock.risks[i] = 0.9
	}
	for i := uint32(6); i <= 10; i++ {
		mock.risks[i] = 0.1
	}
	opt.SetInfectionRiskProvider(mock)

	for i := uint32(1); i <= 10; i++ {
		opt.bedOccupancy[i] = true
	}

	opt.SolveAndBroadcast()
	sol1 := opt.GetLatestSolution()
	if sol1 == nil {
		t.Fatal("First solve returned nil solution")
	}

	initialNPBeds := make(map[uint32]bool)
	for bedID, room := range sol1.Assignments {
		if len(room) >= 2 && room[:2] == "NP" {
			initialNPBeds[bedID] = true
		}
	}
	t.Logf("Initial NP beds: %v", initialNPBeds)

	if len(initialNPBeds) != 5 {
		t.Fatalf("Expected 5 NP beds initially, got %d", len(initialNPBeds))
	}

	mock.risks[6] = 0.95
	opt.SetInfectionRiskProvider(mock)

	time.Sleep(10 * time.Millisecond)
	opt.SolveAndBroadcast()
	sol2 := opt.GetLatestSolution()
	if sol2 == nil {
		t.Fatal("Second solve returned nil solution")
	}

	rate := opt.GetLatestChangeRate()
	t.Logf("Change rate after risk modification: %.4f", rate)

	if rate >= 0.3 {
		t.Errorf("Change rate = %.4f, want < 0.3", rate)
	}

	preservedNPBeds := 0
	for bedID := range initialNPBeds {
		room, exists := sol2.Assignments[bedID]
		if exists && len(room) >= 2 && room[:2] == "NP" {
			preservedNPBeds++
		}
	}
	t.Logf("Preserved NP beds: %d", preservedNPBeds)

	if preservedNPBeds < 3 {
		t.Errorf("Preserved NP beds = %d, want >= 3", preservedNPBeds)
	}
}

func TestFix_L1RegularizationCost_IncreasesWithMoreChanges(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	prev := map[uint32]string{
		1: "NP-001",
		2: "NP-002",
		3: "WARD-001",
	}
	prevSchedule := map[string][]uint32{
		"Nurse-001": {1, 2},
		"Nurse-002": {3},
	}

	opt.mu.Lock()
	opt.prevAssignments = prev
	opt.prevSchedule = prevSchedule
	opt.mu.Unlock()

	curr1 := map[uint32]string{
		1: "NP-001",
		2: "NP-002",
		3: "WARD-001",
	}
	emptySchedule := map[string][]uint32{
		"Nurse-001": {},
		"Nurse-002": {},
	}
	cost1 := opt.l1RegularizationCost(curr1, emptySchedule)
	t.Logf("Cost with no changes: %.4f", cost1)

	curr2 := map[uint32]string{
		1: "WARD-001",
		2: "WARD-002",
		3: "NP-001",
	}
	cost2 := opt.l1RegularizationCost(curr2, emptySchedule)
	t.Logf("Cost with all changes: %.4f", cost2)

	if cost2 <= cost1 {
		t.Errorf("Cost with all changes (%.4f) should be greater than cost with no changes (%.4f)", cost2, cost1)
	}
	if cost1 >= 0.01 {
		t.Errorf("Cost with no changes = %.4f, want < 0.01", cost1)
	}
}

func TestFix_ChangeRateCalculation_L1DistanceCorrect(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	prev := map[uint32]string{
		1: "NP-001",
		2: "NP-002",
		3: "WARD-001",
		4: "WARD-002",
		5: "WARD-003",
	}
	curr := map[uint32]string{
		1: "NP-001",
		2: "NP-002",
		3: "NP-003",
		4: "WARD-002",
		5: "NP-004",
	}

	rate := opt.computeAssignmentChangeRate(prev, curr)
	t.Logf("Computed change rate: %.4f", rate)

	expectedRate := 0.4
	if math.Abs(rate-expectedRate) >= 0.01 {
		t.Errorf("Change rate = %.4f, want %.4f (within 0.01 tolerance)", rate, expectedRate)
	}
}

func TestFix_SetStabilityLambda_ClampedToValidRange(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	opt.SetStabilityLambda(-1.0)
	if opt.stabilityLambda != 0 {
		t.Errorf("stabilityLambda = %.2f after SetStabilityLambda(-1.0), want 0", opt.stabilityLambda)
	}

	opt.SetStabilityLambda(5.0)
	if opt.stabilityLambda != 2.0 {
		t.Errorf("stabilityLambda = %.2f after SetStabilityLambda(5.0), want 2.0", opt.stabilityLambda)
	}

	opt.SetStabilityLambda(0.7)
	if math.Abs(opt.stabilityLambda-0.7) >= 0.001 {
		t.Errorf("stabilityLambda = %.4f after SetStabilityLambda(0.7), want 0.7", opt.stabilityLambda)
	}
}

func TestFix_ZeroLambda_NoStabilityConstraint(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)
	opt.SetStabilityLambda(0)

	mock := &MockRiskProvider{
		risks: map[uint32]float64{},
	}
	for i := uint32(1); i <= 10; i++ {
		if i <= 5 {
			mock.risks[i] = 0.9
		} else {
			mock.risks[i] = 0.1
		}
	}
	opt.SetInfectionRiskProvider(mock)

	for i := uint32(1); i <= 10; i++ {
		opt.bedOccupancy[i] = true
	}

	opt.SolveAndBroadcast()
	sol1 := opt.GetLatestSolution()
	if sol1 == nil {
		t.Fatal("First solve returned nil solution")
	}
	t.Logf("First solve NP assignments:")
	for bedID, room := range sol1.Assignments {
		if len(room) >= 2 && room[:2] == "NP" {
			t.Logf("  Bed %d -> %s", bedID, room)
		}
	}

	for i := uint32(1); i <= 10; i++ {
		if i <= 5 {
			mock.risks[i] = 0.1
		} else {
			mock.risks[i] = 0.9
		}
	}
	opt.SetInfectionRiskProvider(mock)

	time.Sleep(10 * time.Millisecond)
	opt.SolveAndBroadcast()
	sol2 := opt.GetLatestSolution()
	if sol2 == nil {
		t.Fatal("Second solve returned nil solution")
	}
	t.Logf("Second solve NP assignments:")
	for bedID, room := range sol2.Assignments {
		if len(room) >= 2 && room[:2] == "NP" {
			t.Logf("  Bed %d -> %s", bedID, room)
		}
	}

	rate := opt.GetLatestChangeRate()
	t.Logf("Change rate with lambda=0: %.4f", rate)

	if rate < 0.3 {
		t.Errorf("Change rate = %.4f with lambda=0, want >= 0.3 (no stability constraint)", rate)
	}
}
