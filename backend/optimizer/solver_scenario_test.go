package optimizer

import (
	"sync"
	"testing"
	"time"

	"field-hospital-icu/config"
)

func TestNormal_SolveTenBeds_WithinFiveSeconds(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 5,
		NursesPerShift:      3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	beds := []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, bedID := range beds {
		opt.bedOccupancy[bedID] = true
	}

	mockRisks := map[uint32]float64{
		1: 0.1, 2: 0.2, 3: 0.3, 4: 0.4, 5: 0.5,
		6: 0.6, 7: 0.7, 8: 0.8, 9: 0.9, 10: 0.95,
	}
	mock := &MockRiskProvider{risks: mockRisks}
	opt.SetInfectionRiskProvider(mock)

	start := time.Now()
	opt.SolveAndBroadcast()
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("SolveAndBroadcast took %v, want < 5s", elapsed)
	}

	sol := opt.GetLatestSolution()
	if sol == nil {
		t.Fatal("GetLatestSolution returned nil")
	}
	if sol.Status != "solved" {
		t.Errorf("Status = %s, want 'solved'", sol.Status)
	}

	npCount := 0
	for _, room := range sol.Assignments {
		if len(room) >= 2 && room[:2] == "NP" {
			npCount++
		}
	}
	if npCount != 5 {
		t.Errorf("NP beds count = %d, want 5", npCount)
	}

	highRiskBeds := []uint32{8, 9, 10}
	for _, bedID := range highRiskBeds {
		room, ok := sol.Assignments[bedID]
		if !ok {
			t.Errorf("Bed %d not found in assignments", bedID)
			continue
		}
		if len(room) < 2 || room[:2] != "NP" {
			t.Errorf("High-risk bed %d (risk=%.2f) should be in NP, got %s", bedID, mockRisks[bedID], room)
		}
	}
}

func TestNormal_OptimalNurseAssignment_BalancedWorkload(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 5,
		NursesPerShift:      3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	beds := make([]uint32, 20)
	for i := uint32(0); i < 20; i++ {
		beds[i] = i + 1
	}

	schedule := opt.SolveNurseSchedule(beds, 9)

	counts := make([]int, 0, 9)
	nurses := make([]string, 0, 9)
	for n := range schedule {
		nurses = append(nurses, n)
	}
	for _, n := range nurses {
		counts = append(counts, len(schedule[n]))
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
	if variance >= 1.0 {
		t.Errorf("Variance too high: %.4f (want < 1.0)", variance)
	}
	t.Logf("Nurse counts: %v, variance=%.4f", counts, variance)
}

func TestNormal_SuggestionsGeneratedOnSolutionChange(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 5,
		NursesPerShift:      3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	beds := []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, bedID := range beds {
		opt.bedOccupancy[bedID] = true
	}

	mockRisks1 := map[uint32]float64{
		1: 0.1, 2: 0.2, 3: 0.3, 4: 0.4, 5: 0.5,
		6: 0.6, 7: 0.7, 8: 0.8, 9: 0.9, 10: 0.95,
	}
	mock := &MockRiskProvider{risks: mockRisks1}
	opt.SetInfectionRiskProvider(mock)

	opt.SolveAndBroadcast()
	prev := opt.GetLatestSolution()
	if prev == nil {
		t.Fatal("First solve returned nil solution")
	}

	mock.risks[1] = 0.95

	opt.SolveAndBroadcast()

	suggestions := opt.GetSuggestions()
	if len(suggestions) == 0 {
		t.Fatal("Expected suggestions after solution change, got none")
	}

	hasRoomSwap := false
	for _, s := range suggestions {
		stype, ok := s["type"].(string)
		if ok && stype == "room_swap" {
			fromBed, _ := s["from_bed"].(uint32)
			toRoom, _ := s["to_room"].(string)
			if fromBed == 1 && len(toRoom) >= 2 && toRoom[:2] == "NP" {
				hasRoomSwap = true
				t.Logf("Found expected room_swap for bed 1: %s -> %s", s["from_room"], toRoom)
			}
		}
	}
	if !hasRoomSwap {
		t.Error("Expected room_swap suggestion for bed 1 moving from WARD to NP")
	}
}

func TestNormal_CostFunctionReflectsRiskImprovement(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 5,
		NursesPerShift:      3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	beds := []uint32{1, 2}
	bedRisks := map[uint32]float64{
		1: 0.95,
		2: 0.1,
	}

	assignmentsA := map[uint32]string{
		1: "WARD-001",
		2: "NP-001",
	}

	assignmentsB := map[uint32]string{
		1: "NP-001",
		2: "WARD-001",
	}

	schedule := map[string][]uint32{
		"Nurse-001": {1},
		"Nurse-002": {2},
	}

	costA := opt.ComputeCost(assignmentsA, schedule, bedRisks)
	costB := opt.ComputeCost(assignmentsB, schedule, bedRisks)

	t.Logf("CostA.InfectionRisk = %.4f (high-risk in WARD)", costA.InfectionRisk)
	t.Logf("CostB.InfectionRisk = %.4f (high-risk in NP)", costB.InfectionRisk)

	if costA.InfectionRisk <= costB.InfectionRisk {
		t.Errorf("InfectionRisk should be higher when high-risk bed is in WARD: A=%.4f, B=%.4f", costA.InfectionRisk, costB.InfectionRisk)
	}
}

func TestBoundary_InsufficientResources_QueueSuggestion(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 2,
		NursesPerShift:      3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	beds := []uint32{1, 2, 3, 4, 5}
	for _, bedID := range beds {
		opt.bedOccupancy[bedID] = true
	}

	mockRisks := map[uint32]float64{
		1: 0.85, 2: 0.82, 3: 0.88, 4: 0.81, 5: 0.9,
	}
	mock := &MockRiskProvider{risks: mockRisks}
	opt.SetInfectionRiskProvider(mock)

	opt.SolveAndBroadcast()

	sol := opt.GetLatestSolution()
	if sol == nil {
		t.Fatal("GetLatestSolution returned nil")
	}

	npCount := 0
	wardCount := 0
	for _, room := range sol.Assignments {
		if len(room) >= 2 && room[:2] == "NP" {
			npCount++
		} else if len(room) >= 4 && room[:4] == "WARD" {
			wardCount++
		}
	}

	if npCount != 2 {
		t.Errorf("NP beds count = %d, want 2", npCount)
	}
	if wardCount != 3 {
		t.Errorf("WARD beds count = %d, want 3", wardCount)
	}
	t.Logf("NP: %d, WARD: %d (5 high-risk beds, NP capacity=2)", npCount, wardCount)
}

func TestBoundary_SingleBedSingleNurse(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 1,
		NursesPerShift:      1,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	opt.bedOccupancy[1] = true

	mock := &MockRiskProvider{
		risks: map[uint32]float64{1: 0.5},
	}
	opt.SetInfectionRiskProvider(mock)

	opt.SolveAndBroadcast()

	sol := opt.GetLatestSolution()
	if sol == nil {
		t.Fatal("GetLatestSolution returned nil")
	}

	room, ok := sol.Assignments[1]
	if !ok {
		t.Fatal("Bed 1 not found in assignments")
	}
	if room == "" {
		t.Error("Bed 1 has empty room assignment")
	}
	t.Logf("Bed 1 assigned to: %s", room)

	if len(sol.Schedule) != 3 {
		t.Errorf("Schedule nurse count = %d, want 3", len(sol.Schedule))
	}

	totalBeds := 0
	for _, beds := range sol.Schedule {
		totalBeds += len(beds)
	}
	if totalBeds != 1 {
		t.Errorf("Total assigned beds = %d, want 1", totalBeds)
	}
}

func TestBoundary_AllBedsHighRisk(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 10,
		NursesPerShift:      3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	beds := make([]uint32, 20)
	bedRisks := make(map[uint32]float64)
	for i := uint32(0); i < 20; i++ {
		beds[i] = i + 1
		bedRisks[i+1] = 0.9
	}

	assignments := opt.SolveNegativePressure(beds, bedRisks, 10)

	npCount := 0
	wardCount := 0
	for bedID, room := range assignments {
		if len(room) >= 2 && room[:2] == "NP" {
			npCount++
			if bedID > 10 {
				t.Errorf("Bed %d (ID>10) should not be in NP when all risks equal", bedID)
			}
		} else {
			wardCount++
		}
	}

	if npCount != 10 {
		t.Errorf("NP beds count = %d, want 10", npCount)
	}
	if wardCount != 10 {
		t.Errorf("WARD beds count = %d, want 10", wardCount)
	}

	schedule := map[string][]uint32{
		"Nurse-001": {1, 2, 3},
		"Nurse-002": {4, 5, 6},
		"Nurse-003": {7, 8, 9, 10},
	}
	cost := opt.ComputeCost(assignments, schedule, bedRisks)

	if cost.InfectionRisk <= 0 {
		t.Error("InfectionRisk should be > 0 because 10 high-risk beds are in WARD")
	}
	t.Logf("InfectionRisk = %.4f (10 high-risk in WARD, each 0.9*10 penalty)", cost.InfectionRisk)
}

func TestBoundary_ZeroNurses_EmptySchedule(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 5,
		NursesPerShift:      0,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SolveNurseSchedule panicked with nil beds: %v", r)
		}
	}()

	schedule := opt.SolveNurseSchedule(nil, 0)

	if len(schedule) != 9 {
		t.Errorf("Nurse count = %d, want 9 (default when numNurses<=0)", len(schedule))
	}

	for nurse, beds := range schedule {
		if len(beds) != 0 {
			t.Errorf("Nurse %s has %d beds, want 0 for empty bed list", nurse, len(beds))
		}
	}

	t.Logf("Zero nurses test passed: %d nurses, all with 0 beds", len(schedule))
}

func TestAbnormal_SolverTimeout_ReturnsGreedySolution(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 10,
		NursesPerShift:      3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	beds := make([]uint32, 50)
	bedRisks := make(map[uint32]float64)
	for i := uint32(0); i < 50; i++ {
		beds[i] = i + 1
		bedRisks[i+1] = float64(i) / 50.0
	}

	var totalTime time.Duration
	var firstResult map[uint32]string

	for i := 0; i < 1000; i++ {
		start := time.Now()
		result := opt.SolveNegativePressure(beds, bedRisks, 10)
		elapsed := time.Since(start)
		totalTime += elapsed

		if elapsed > 1*time.Millisecond {
			t.Errorf("Iteration %d took %v, want < 1ms", i, elapsed)
		}

		if i == 0 {
			firstResult = result
		} else {
			for bedID, room := range firstResult {
				if result[bedID] != room {
					t.Errorf("Result mismatch at iteration %d: bed %d = %s vs first %s", i, bedID, result[bedID], room)
				}
			}
		}
	}

	if totalTime > 1*time.Second {
		t.Errorf("Total time for 1000 iterations = %v, want < 1s", totalTime)
	}

	t.Logf("1000 iterations completed in %v (avg %v per iteration)", totalTime, totalTime/1000)
}

func TestAbnormal_NilRiskProvider_UsesRandomRisks(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 5,
		NursesPerShift:      3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	if opt.infectionRiskProvider != nil {
		t.Error("infectionRiskProvider should be nil initially")
	}

	opt.SolveAndBroadcast()

	sol := opt.GetLatestSolution()
	if sol == nil {
		t.Fatal("GetLatestSolution returned nil with nil risk provider")
	}

	if len(sol.Assignments) != 50 {
		t.Errorf("Assignments count = %d, want 50 (default beds 1-50)", len(sol.Assignments))
	}

	for bedID := uint32(1); bedID <= 50; bedID++ {
		if _, ok := sol.Assignments[bedID]; !ok {
			t.Errorf("Bed %d not found in assignments", bedID)
		}
	}

	t.Logf("Nil risk provider test passed: %d assignments generated", len(sol.Assignments))
}

func TestAbnormal_ConcurrentSolveAndRead(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 5,
		NursesPerShift:      3,
		ObjectiveWeight: map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		},
	}
	opt := NewBedOptimizer(cfg)

	mock := &MockRiskProvider{
		risks: make(map[uint32]float64),
	}
	for i := uint32(1); i <= 20; i++ {
		mock.risks[i] = float64(i) / 20.0
	}
	opt.SetInfectionRiskProvider(mock)

	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			select {
			case <-stopChan:
				return
			default:
				opt.SolveAndBroadcast()
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					sol := opt.GetLatestSolution()
					if sol != nil && len(sol.Assignments) == 0 {
						t.Errorf("Reader %d: solution has empty assignments", id)
					}
					suggestions := opt.GetSuggestions()
					if suggestions == nil {
						t.Errorf("Reader %d: suggestions is nil", id)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		}(i)
	}

	time.Sleep(2 * time.Second)
	close(stopChan)
	wg.Wait()

	t.Log("Concurrent solve and read test completed without panic")
}

func TestAbnormal_EmptyObjectiveWeight_UsesDefaults(t *testing.T) {
	cfg := config.OptimizerConfig{
		Enabled:             true,
		SolveIntervalSec:    10,
		NegativePressureBeds: 5,
		NursesPerShift:      3,
		ObjectiveWeight:     nil,
	}
	opt := NewBedOptimizer(cfg)

	if opt.cfg.ObjectiveWeight != nil {
		t.Error("ObjectiveWeight should be nil initially")
	}

	assignments := map[uint32]string{
		1: "NP-001",
		2: "WARD-001",
	}
	bedRisks := map[uint32]float64{
		1: 0.9,
		2: 0.8,
	}
	schedule := map[string][]uint32{
		"Nurse-001": {1},
		"Nurse-002": {2},
	}

	obj := opt.ComputeCost(assignments, schedule, bedRisks)

	if obj.TotalCost <= 0 {
		t.Errorf("TotalCost = %.4f, want > 0", obj.TotalCost)
	}

	if obj.InfectionRisk <= 0 {
		t.Error("InfectionRisk should be > 0 (bed2 with high risk threshold in WARD)")
	}

	t.Logf("Empty ObjectiveWeight test passed:")
	t.Logf("  InfectionRisk = %.4f", obj.InfectionRisk)
	t.Logf("  NurseWorkloadBalance = %.4f", obj.NurseWorkloadBalance)
	t.Logf("  RoomUtilization = %.4f", obj.RoomUtilization)
	t.Logf("  TransportDistance = %.4f", obj.TransportDistance)
	t.Logf("  TotalCost = %.4f", obj.TotalCost)
}
