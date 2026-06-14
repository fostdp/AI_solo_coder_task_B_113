package optimizer

import (
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

type MockRiskProvider struct {
	risks map[uint32]float64
}

func (m *MockRiskProvider) GetInfectionRisk(bedID uint32) float64 {
	if r, ok := m.risks[bedID]; ok {
		return r
	}
	return 0.0
}

func newDefaultConfig() config.OptimizerConfig {
	return config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:  10,
		NegativePressureBeds: 5,
		NursesPerShift:   3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":   0.2,
		},
	}
}

func TestNewBedOptimizer(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	if opt == nil {
		t.Fatal("NewBedOptimizer returned nil")
	}
	if opt.cfg.SolveIntervalSec != 10 {
		t.Errorf("SolveIntervalSec = %d, want 10", opt.cfg.SolveIntervalSec)
	}
	if opt.cfg.NegativePressureBeds != 5 {
		t.Errorf("NegativePressureBeds = %d, want 5", opt.cfg.NegativePressureBeds)
	}
	if opt.bedOccupancy == nil {
		t.Error("bedOccupancy map is nil")
	}
	if opt.latestSuggestion == nil {
		t.Error("latestSuggestion is nil")
	}
	if opt.stopChan == nil {
		t.Error("stopChan is nil")
	}
	if Instance != opt {
		t.Error("Instance not set correctly after NewBedOptimizer")
	}
}

func TestSolveNegativePressure_CountCorrect(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	beds := make([]uint32, 20)
	bedRisks := make(map[uint32]float64)
	for i := uint32(0); i < 20; i++ {
		beds[i] = i + 1
		bedRisks[i+1] = float64(i) / 20.0
	}

	result := opt.SolveNegativePressure(beds, bedRisks, 5)

	npCount := 0
	wardCount := 0
	for _, room := range result {
		if len(room) >= 2 && room[:2] == "NP" {
			npCount++
		} else if len(room) >= 4 && room[:4] == "WARD" {
			wardCount++
		}
	}

	if npCount != 5 {
		t.Errorf("NP beds count = %d, want 5", npCount)
	}
	if wardCount != 15 {
		t.Errorf("WARD beds count = %d, want 15", wardCount)
	}
	if len(result) != 20 {
		t.Errorf("Total assignments = %d, want 20", len(result))
	}
}

func TestSolveNegativePressure_HighRiskPriority(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	beds := []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	bedRisks := map[uint32]float64{
		1: 0.1,
		2: 0.9,
		3: 0.8,
		4: 0.2,
		5: 0.95,
		6: 0.3,
		7: 0.85,
		8: 0.4,
		9: 0.88,
		10: 0.05,
	}

	result := opt.SolveNegativePressure(beds, bedRisks, 5)

	npBeds := make([]uint32, 0)
	for bedID, room := range result {
		if len(room) >= 2 && room[:2] == "NP" {
			npBeds = append(npBeds, bedID)
		}
	}

	highRiskBeds := map[uint32]bool{5: true, 2: true, 9: true, 7: true, 3: true}
	for _, bedID := range npBeds {
		if !highRiskBeds[bedID] {
			t.Errorf("Bed %d (risk=%.2f) assigned to NP but not in top 5 risks", bedID, bedRisks[bedID])
		}
	}

	lowRiskBeds := map[uint32]bool{10: true, 1: true, 4: true, 6: true, 8: true}
	for bedID, room := range result {
		if lowRiskBeds[bedID] {
			if len(room) >= 2 && room[:2] == "NP" {
				t.Errorf("Low-risk bed %d (risk=%.2f) should not be in NP", bedID, bedRisks[bedID])
			}
		}
	}
}

func TestSolveNurseSchedule_Balance(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	beds := make([]uint32, 25)
	for i := uint32(0); i < 25; i++ {
		beds[i] = i + 1
	}

	totalNurses := 9
	schedule := opt.SolveNurseSchedule(beds, totalNurses)

	counts := make([]int, 0, totalNurses)
	for _, bedList := range schedule {
		counts = append(counts, len(bedList))
	}

	if len(schedule) != totalNurses {
		t.Errorf("Nurse count = %d, want %d", len(schedule), totalNurses)
	}

	totalAssigned := 0
	for _, c := range counts {
		totalAssigned += c
	}
	if totalAssigned != 25 {
		t.Errorf("Total assigned beds = %d, want 25", totalAssigned)
	}

	minCount := counts[0]
	maxCount := counts[0]
	for _, c := range counts {
		if c < minCount {
			minCount = c
		}
		if c > maxCount {
			maxCount = c
		}
	}
	diff := maxCount - minCount
	if diff > 1 {
		t.Errorf("Workload imbalance: max=%d, min=%d, diff=%d (want <=1)", maxCount, minCount, diff)
	}

	sum := 0.0
	for _, c := range counts {
		sum += float64(c)
	}
	mean := sum / float64(len(counts))
	variance := 0.0
	for _, c := range counts {
		d := float64(c) - mean
		variance += d * d
	}
	variance /= float64(len(counts))
	if variance > 1.0 {
		t.Errorf("Variance too high: %.4f (want <= 1.0)", variance)
	}
	t.Logf("Nurse counts: %v, variance=%.4f", counts, variance)
}

func TestComputeCost(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	beds := []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	assignments := map[uint32]string{
		1: "NP-001",
		2: "NP-002",
		3: "NP-003",
		4: "NP-004",
		5: "NP-005",
		6: "WARD-001",
		7: "WARD-002",
		8: "WARD-003",
		9: "WARD-004",
		10: "WARD-005",
	}
	schedule := map[string][]uint32{
		"Nurse-001": {1, 2, 3, 4},
		"Nurse-002": {5, 6, 7},
		"Nurse-003": {8, 9, 10},
	}
	bedRisks := map[uint32]float64{
		1: 0.9,
		2: 0.85,
		3: 0.8,
		4: 0.75,
		5: 0.7,
		6: 0.95,
		7: 0.1,
		8: 0.2,
		9: 0.15,
		10: 0.05,
	}

	obj := opt.ComputeCost(assignments, schedule, bedRisks)

	if obj.InfectionRisk <= 0 {
		t.Error("InfectionRisk should be > 0 (bed 6 has high risk but in WARD)")
	}
	t.Logf("InfectionRisk = %.4f", obj.InfectionRisk)

	if obj.NurseWorkloadBalance < 0 {
		t.Error("NurseWorkloadBalance should be >= 0")
	}
	t.Logf("NurseWorkloadBalance = %.4f", obj.NurseWorkloadBalance)

	if obj.RoomUtilization < 0 || obj.RoomUtilization > 1 {
		t.Errorf("RoomUtilization = %.4f, should be in [0,1]", obj.RoomUtilization)
	}
	t.Logf("RoomUtilization = %.4f", obj.RoomUtilization)

	if obj.TransportDistance < 0 {
		t.Error("TransportDistance should be >= 0")
	}
	t.Logf("TransportDistance = %.4f", obj.TransportDistance)

	if obj.TotalCost <= 0 {
		t.Error("TotalCost should be > 0")
	}
	t.Logf("TotalCost = %.4f", obj.TotalCost)
}

func TestGenerateSuggestions(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	prev := &models.OptimizerSolution{
		Assignments: map[uint32]string{
			1: "WARD-001",
			2: "WARD-002",
			3: "NP-001",
		},
		Schedule: map[string][]uint32{
			"Nurse-001": {1},
			"Nurse-002": {2},
			"Nurse-003": {3},
		},
	}

	curr := &models.OptimizerSolution{
		Assignments: map[uint32]string{
			1: "NP-001",
			2: "WARD-002",
			3: "NP-002",
		},
		Schedule: map[string][]uint32{
			"Nurse-001": {2},
			"Nurse-002": {1},
			"Nurse-003": {3},
		},
	}

	suggestions := opt.GenerateSuggestions(prev, curr)

	t.Logf("Generated %d suggestions", len(suggestions))

	roomSwapCount := 0
	nurseReassignCount := 0
	for _, s := range suggestions {
		stype, _ := s["type"].(string)
		switch stype {
		case "room_swap":
			roomSwapCount++
			fromBed, _ := s["from_bed"].(uint32)
			if fromBed == 1 || fromBed == 3 {
				t.Logf("Valid room_swap for bed %d", fromBed)
			}
		case "nurse_reassign":
			nurseReassignCount++
		}
	}

	if roomSwapCount < 2 {
		t.Errorf("Expected at least 2 room_swap suggestions, got %d", roomSwapCount)
	}
	if nurseReassignCount < 2 {
		t.Errorf("Expected at least 2 nurse_reassign suggestions, got %d", nurseReassignCount)
	}

	if len(suggestions) > 1 {
		for i := 1; i < len(suggestions); i++ {
			pi, _ := suggestions[i-1]["priority"].(int)
			pj, _ := suggestions[i]["priority"].(int)
			if pi < pj {
				t.Error("Suggestions not sorted by priority descending")
			}
		}
	}
}

func TestEmptyInputs(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	t.Run("SolveNegativePressure_EmptyBeds", func(t *testing.T) {
		result := opt.SolveNegativePressure(nil, nil, 5)
		if len(result) != 0 {
			t.Errorf("Expected empty result for empty beds, got %d", len(result))
		}
	})

	t.Run("SolveNurseSchedule_EmptyBeds", func(t *testing.T) {
		schedule := opt.SolveNurseSchedule(nil, 9)
		if len(schedule) != 9 {
			t.Errorf("Expected 9 nurses for empty beds, got %d", len(schedule))
		}
		for nurse, beds := range schedule {
			if len(beds) != 0 {
				t.Errorf("Nurse %s has %d beds, want 0", nurse, len(beds))
			}
		}
	})

	t.Run("GenerateSuggestions_NilInputs", func(t *testing.T) {
		s1 := opt.GenerateSuggestions(nil, nil)
		if len(s1) != 0 {
			t.Errorf("Expected empty suggestions for both nil, got %d", len(s1))
		}

		prev := &models.OptimizerSolution{}
		s2 := opt.GenerateSuggestions(nil, prev)
		if len(s2) != 0 {
			t.Errorf("Expected empty suggestions for prev=nil, got %d", len(s2))
		}

		s3 := opt.GenerateSuggestions(prev, nil)
		if len(s3) != 0 {
			t.Errorf("Expected empty suggestions for curr=nil, got %d", len(s3))
		}
	})

	t.Run("ComputeCost_Empty", func(t *testing.T) {
		obj := opt.ComputeCost(nil, nil, nil)
		if obj.TotalCost < 0 {
			t.Errorf("TotalCost = %.4f, want >= 0", obj.TotalCost)
		}
	})
}

func TestGlobalSingleton(t *testing.T) {
	Instance = nil

	cfg := newDefaultConfig()
	opt1 := NewBedOptimizer(cfg)

	if Instance == nil {
		t.Fatal("Instance should be set after NewBedOptimizer")
	}
	if Instance != opt1 {
		t.Error("Instance should equal opt1")
	}

	got := GetInstance()
	if got != opt1 {
		t.Error("GetInstance() should return opt1")
	}

	cfg2 := newDefaultConfig()
	cfg2.NegativePressureBeds = 20
	opt2 := NewBedOptimizer(cfg2)

	if Instance != opt2 {
		t.Error("Instance should be updated to opt2")
	}
	if GetInstance() != opt2 {
		t.Error("GetInstance() should return the latest instance")
	}
	if GetInstance().cfg.NegativePressureBeds != 20 {
		t.Errorf("Latest instance cfg not updated")
	}

	Instance = nil
	if GetInstance() != nil {
		t.Error("GetInstance() should return nil after reset")
	}
}

func TestSetInfectionRiskProvider(t *testing.T) {
	cfg := newDefaultConfig()
	opt := NewBedOptimizer(cfg)

	if opt.infectionRiskProvider != nil {
		t.Error("Provider should be nil initially")
	}

	mock := &MockRiskProvider{
		risks: map[uint32]float64{
			1: 0.8,
			2: 0.3,
		},
	}

	opt.SetInfectionRiskProvider(mock)

	if opt.infectionRiskProvider == nil {
		t.Fatal("Provider should not be nil after SetInfectionRiskProvider")
	}

	r := opt.infectionRiskProvider.GetInfectionRisk(1)
	if r != 0.8 {
		t.Errorf("GetInfectionRisk(1) = %.2f, want 0.8", r)
	}

	r2 := opt.infectionRiskProvider.GetInfectionRisk(999)
	if r2 != 0.0 {
		t.Errorf("GetInfectionRisk(999) = %.2f, want 0.0", r2)
	}
}

func TestDecisionVariableStruct(t *testing.T) {
	dv := DecisionVariable{
		BedID:         42,
		AssignedRoom:  "NP-005",
		AssignedNurse: "Nurse-007",
	}

	if dv.BedID != 42 {
		t.Errorf("BedID = %d, want 42", dv.BedID)
	}
	if dv.AssignedRoom != "NP-005" {
		t.Errorf("AssignedRoom = %s, want NP-005", dv.AssignedRoom)
	}
	if dv.AssignedNurse != "Nurse-007" {
		t.Errorf("AssignedNurse = %s, want Nurse-007", dv.AssignedNurse)
	}
}

func TestObjectiveValueStruct(t *testing.T) {
	obj := ObjectiveValue{
		InfectionRisk:         1.5,
		NurseWorkloadBalance:  0.25,
		RoomUtilization:       0.8,
		TransportDistance:     45.5,
		TotalCost:           10.0,
	}

	if obj.InfectionRisk != 1.5 {
		t.Errorf("InfectionRisk = %.2f, want 1.5", obj.InfectionRisk)
	}
	if obj.NurseWorkloadBalance != 0.25 {
		t.Errorf("NurseWorkloadBalance = %.2f, want 0.25", obj.NurseWorkloadBalance)
	}
	if obj.RoomUtilization != 0.8 {
		t.Errorf("RoomUtilization = %.2f, want 0.8", obj.RoomUtilization)
	}
	if obj.TransportDistance != 45.5 {
		t.Errorf("TransportDistance = %.2f, want 45.5", obj.TransportDistance)
	}
	if obj.TotalCost != 10.0 {
		t.Errorf("TotalCost = %.2f, want 10.0", obj.TotalCost)
	}
}

func TestSolveAndBroadcast_GetLatestSolution(t *testing.T) {
	cfg := newDefaultConfig()
	cfg.SolveIntervalSec = 1
	opt := NewBedOptimizer(cfg)

	mock := &MockRiskProvider{
		risks: map[uint32]float64{},
	}
	for i := uint32(1); i <= 20; i++ {
		mock.risks[i] = float64(i) / 20.0
	}
	opt.SetInfectionRiskProvider(mock)

	opt.Start()
	defer opt.Stop()

	time.Sleep(100 * time.Millisecond)
	sol := opt.GetLatestSolution()
	if sol == nil {
		t.Fatal("GetLatestSolution() returned nil after Start")
	}
	if len(sol.Assignments) == 0 {
		t.Error("Assignments should not be empty")
	}
	if len(sol.Schedule) == 0 {
		t.Error("Schedule should not be empty")
	}
	if sol.Objective.TotalCost <= 0 {
		t.Errorf("TotalCost = %.4f, want > 0", sol.Objective.TotalCost)
	}
	t.Logf("Solution ID: %s, TotalCost: %.4f, Assignments: %d, Nurses: %d",
		sol.SolutionID, sol.Objective.TotalCost, len(sol.Assignments), len(sol.Schedule))

	suggestions := opt.GetSuggestions()
	t.Logf("Initial suggestions count: %d (expected 0 for first run)", len(suggestions))
}
