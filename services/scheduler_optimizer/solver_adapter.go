package main

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"
)

type BedRisk struct {
	BedID         uint32  `json:"bed_id"`
	InfectionRisk float64 `json:"infection_risk"`
}

type Suggestion struct {
	Type   string `json:"type"`
	BedID  uint32 `json:"bed_id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

type SolveRequest struct {
	BedRisks             []BedRisk            `json:"bed_risks"`
	NegativePressureBeds int32                `json:"negative_pressure_beds"`
	NursesPerShift       int32                `json:"nurses_per_shift"`
	StabilityLambda      float64              `json:"stability_lambda"`
	DeadlineMs           int64                `json:"deadline_ms"`
	UseGreedyFallback    bool                 `json:"use_greedy_fallback"`
	ObjectiveWeight      map[string]float64   `json:"objective_weight"`
}

type SolveResponse struct {
	SolutionID      string              `json:"solution_id"`
	Assignments     map[uint32]string   `json:"assignments"`
	Schedule        map[string][]uint32 `json:"schedule"`
	Objective       map[string]float64  `json:"objective"`
	UnmetNeeds      []string            `json:"unmet_needs"`
	Suggestions     []Suggestion        `json:"suggestions"`
	Status          string              `json:"status"`
	ChangeRate      float64             `json:"change_rate"`
	SolveTimeMs     int64               `json:"solve_time_ms"`
	IsGreedyFallback bool               `json:"is_greedy_fallback"`
}

type OptimizerObjective struct {
	InfectionRisk         float64
	NurseWorkloadBalance  float64
	RoomUtilization       float64
	TransportDistance     float64
	TotalCost             float64
}

type SolverAdapter struct {
	stabilityLambda   float64
	changeRateHistory []float64
	prevAssignments   map[uint32]string
	prevSchedule      map[string][]uint32
	totalSolves       int64
	mu                sync.RWMutex
	solutionsCache    map[string]*CachedSolution
}

type CachedSolution struct {
	Assignments map[uint32]string
	Schedule    map[string][]uint32
	Objective   map[string]float64
}

func NewSolverAdapter() *SolverAdapter {
	return &SolverAdapter{
		stabilityLambda:   0.5,
		changeRateHistory: make([]float64, 0, 100),
		prevAssignments:   make(map[uint32]string),
		prevSchedule:      make(map[string][]uint32),
		totalSolves:       0,
		solutionsCache:    make(map[string]*CachedSolution),
	}
}

func (s *SolverAdapter) SetStabilityLambda(lambda float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lambda < 0 {
		lambda = 0
	}
	if lambda > 2.0 {
		lambda = 2.0
	}
	s.stabilityLambda = lambda
}

func (s *SolverAdapter) GetStabilityLambda() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stabilityLambda
}

func (s *SolverAdapter) GetChangeRateStats() (float64, float64, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	latest := 0.3
	avg := 0.3
	if len(s.changeRateHistory) > 0 {
		latest = s.changeRateHistory[len(s.changeRateHistory)-1]
		sum := 0.0
		for _, r := range s.changeRateHistory {
			sum += r
		}
		avg = sum / float64(len(s.changeRateHistory))
	}
	return latest, avg, s.totalSolves
}

func (s *SolverAdapter) SolveNegativePressure(beds []uint32, bedRisks map[uint32]float64, capacity int) map[uint32]string {
	result := make(map[uint32]string)
	if len(beds) == 0 {
		return result
	}

	type bedRiskPair struct {
		bedID uint32
		risk  float64
	}
	pairs := make([]bedRiskPair, 0, len(beds))
	for _, bedID := range beds {
		pairs = append(pairs, bedRiskPair{bedID: bedID, risk: bedRisks[bedID]})
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].risk > pairs[j].risk
	})

	npCount := 0
	wardCount := 0
	for _, pair := range pairs {
		if npCount < capacity {
			npCount++
			result[pair.bedID] = fmt.Sprintf("NP-%03d", npCount)
		} else {
			wardCount++
			result[pair.bedID] = fmt.Sprintf("WARD-%03d", wardCount)
		}
	}

	return result
}

func (s *SolverAdapter) SolveNurseSchedule(beds []uint32, numNurses int) map[string][]uint32 {
	result := make(map[string][]uint32)
	if numNurses <= 0 {
		numNurses = 9
	}
	if len(beds) == 0 {
		for i := 1; i <= numNurses; i++ {
			nurseID := fmt.Sprintf("Nurse-%03d", i)
			result[nurseID] = make([]uint32, 0)
		}
		return result
	}

	for i := 1; i <= numNurses; i++ {
		nurseID := fmt.Sprintf("Nurse-%03d", i)
		result[nurseID] = make([]uint32, 0)
	}

	sortedBeds := make([]uint32, len(beds))
	copy(sortedBeds, beds)
	sort.Slice(sortedBeds, func(i, j int) bool {
		return sortedBeds[i] < sortedBeds[j]
	})

	for idx, bedID := range sortedBeds {
		nurseIdx := (idx % numNurses) + 1
		nurseID := fmt.Sprintf("Nurse-%03d", nurseIdx)
		result[nurseID] = append(result[nurseID], bedID)
	}

	s.localSearchNurseBalance(result)
	s.twoOptNurseSchedule(result, sortedBeds)

	return result
}

func (s *SolverAdapter) localSearchNurseBalance(schedule map[string][]uint32) {
	nurses := make([]string, 0, len(schedule))
	for n := range schedule {
		nurses = append(nurses, n)
	}
	sort.Strings(nurses)

	for iter := 0; iter < 10; iter++ {
		maxLen := 0
		minLen := math.MaxInt32
		var maxNurse, minNurse string

		for _, n := range nurses {
			l := len(schedule[n])
			if l > maxLen {
				maxLen = l
				maxNurse = n
			}
			if l < minLen {
				minLen = l
				minNurse = n
			}
		}

		if maxLen-minLen <= 1 {
			break
		}

		if len(schedule[maxNurse]) > 0 {
			transferBed := schedule[maxNurse][len(schedule[maxNurse])-1]
			schedule[maxNurse] = schedule[maxNurse][:len(schedule[maxNurse])-1]
			schedule[minNurse] = append(schedule[minNurse], transferBed)
		}
	}
}

func (s *SolverAdapter) twoOptNurseSchedule(schedule map[string][]uint32, beds []uint32) {
	nurses := make([]string, 0, len(schedule))
	for n := range schedule {
		nurses = append(nurses, n)
	}
	sort.Strings(nurses)

	if len(nurses) < 2 {
		return
	}

	for iter := 0; iter < 5; iter++ {
		improved := false
		for i := 0; i < len(nurses); i++ {
			for j := i + 1; j < len(nurses); j++ {
				n1 := nurses[i]
				n2 := nurses[j]
				if len(schedule[n1]) > 1 && len(schedule[n2]) > 1 {
					b1 := schedule[n1][len(schedule[n1])-1]
					b2 := schedule[n2][len(schedule[n2])-1]
					schedule[n1][len(schedule[n1])-1] = b2
					schedule[n2][len(schedule[n2])-1] = b1
					if s.evaluateWorkloadBalance(schedule) {
						improved = true
					} else {
						schedule[n1][len(schedule[n1])-1] = b1
						schedule[n2][len(schedule[n2])-1] = b2
					}
				}
			}
		}
		if !improved {
			break
		}
	}
}

func (s *SolverAdapter) evaluateWorkloadBalance(schedule map[string][]uint32) bool {
	counts := make([]int, 0, len(schedule))
	for _, beds := range schedule {
		counts = append(counts, len(beds))
	}
	if len(counts) < 2 {
		return false
	}
	sort.Ints(counts)
	return counts[len(counts)-1]-counts[0] <= 1
}

func (s *SolverAdapter) ComputeCost(assignments map[uint32]string, schedule map[string][]uint32, bedRisks map[uint32]float64, npCapacity int, weights map[string]float64) OptimizerObjective {
	var obj OptimizerObjective

	highRiskThreshold := 0.7
	npPenalty := 0.0
	usedNPBeds := 0

	for bedID, room := range assignments {
		if len(room) >= 2 && room[:2] == "NP" {
			usedNPBeds++
		}
		risk := bedRisks[bedID]
		if risk >= highRiskThreshold {
			if len(room) < 2 || room[:2] != "NP" {
				npPenalty += risk * 10.0
			}
		}
	}
	obj.InfectionRisk = npPenalty

	counts := make([]int, 0, len(schedule))
	nurses := make([]string, 0, len(schedule))
	for n := range schedule {
		nurses = append(nurses, n)
	}
	sort.Strings(nurses)
	for _, n := range nurses {
		counts = append(counts, len(schedule[n]))
	}

	if len(counts) > 0 {
		sum := 0.0
		for _, c := range counts {
			sum += float64(c)
		}
		mean := sum / float64(len(counts))
		variance := 0.0
		for _, c := range counts {
			diff := float64(c) - mean
			variance += diff * diff
		}
		variance /= float64(len(counts))
		obj.NurseWorkloadBalance = variance
	} else {
		obj.NurseWorkloadBalance = 0.0
	}

	if npCapacity <= 0 {
		npCapacity = 10
	}
	if npCapacity > 0 {
		emptyNP := float64(npCapacity - usedNPBeds)
		if emptyNP < 0 {
			emptyNP = 0
		}
		obj.RoomUtilization = 1.0 - (emptyNP / float64(npCapacity))
	} else {
		obj.RoomUtilization = 1.0
	}

	totalDistance := 0.0
	for bedID := range assignments {
		totalDistance += float64(bedID%10) * 0.5
	}
	obj.TransportDistance = totalDistance

	if weights == nil {
		weights = map[string]float64{
			"infection_risk":         0.35,
			"nurse_workload_balance": 0.25,
			"room_utilization":       0.2,
			"transport_distance":     0.2,
		}
	}

	w1 := weights["infection_risk"]
	w2 := weights["nurse_workload_balance"]
	w3 := weights["room_utilization"]
	w4 := weights["transport_distance"]

	utilizationCost := (1.0 - obj.RoomUtilization)
	obj.TotalCost = w1*obj.InfectionRisk + w2*obj.NurseWorkloadBalance + w3*utilizationCost + w4*obj.TransportDistance

	return obj
}

func (s *SolverAdapter) computeAssignmentChangeRate(prev, curr map[uint32]string) float64 {
	if len(prev) == 0 || len(curr) == 0 {
		return 1.0
	}
	total := 0
	changed := 0
	for bedID, currRoom := range curr {
		total++
		prevRoom, exists := prev[bedID]
		if !exists || prevRoom != currRoom {
			changed++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(changed) / float64(total)
}

func (s *SolverAdapter) computeScheduleChangeRate(prev, curr map[string][]uint32) float64 {
	if len(prev) == 0 || len(curr) == 0 {
		return 1.0
	}
	prevBedNurse := make(map[uint32]string)
	for nurse, beds := range prev {
		for _, bedID := range beds {
			prevBedNurse[bedID] = nurse
		}
	}
	currBedNurse := make(map[uint32]string)
	for nurse, beds := range curr {
		for _, bedID := range beds {
			currBedNurse[bedID] = nurse
		}
	}
	total := 0
	changed := 0
	for bedID, currNurse := range currBedNurse {
		total++
		prevNurse, exists := prevBedNurse[bedID]
		if !exists || prevNurse != currNurse {
			changed++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(changed) / float64(total)
}

func (s *SolverAdapter) l1RegularizationCost(assignments map[uint32]string, schedule map[string][]uint32, lambda float64) float64 {
	s.mu.RLock()
	hasPrev := len(s.prevAssignments) > 0 && len(s.prevSchedule) > 0
	prevAssign := make(map[uint32]string)
	prevSched := make(map[string][]uint32)
	for k, v := range s.prevAssignments {
		prevAssign[k] = v
	}
	for k, v := range s.prevSchedule {
		prevSched[k] = append([]uint32{}, v...)
	}
	s.mu.RUnlock()

	if !hasPrev {
		return 0
	}

	assignPenalty := 0.0
	for bedID, currRoom := range assignments {
		prevRoom := prevAssign[bedID]
		if prevRoom != "" && prevRoom != currRoom {
			assignPenalty += 1.0
		}
	}

	prevBedNurse := make(map[uint32]string)
	for nurse, beds := range prevSched {
		for _, bedID := range beds {
			prevBedNurse[bedID] = nurse
		}
	}

	nursePenalty := 0.0
	for nurse, beds := range schedule {
		for _, bedID := range beds {
			prevNurse := prevBedNurse[bedID]
			if prevNurse != "" && prevNurse != nurse {
				nursePenalty += 1.0
			}
		}
	}

	totalBeds := float64(len(assignments))
	if totalBeds > 0 {
		assignPenalty /= totalBeds
		nursePenalty /= totalBeds
	}

	return lambda * (assignPenalty + nursePenalty)
}

func (s *SolverAdapter) solveWithStability(beds []uint32, bedRisks map[uint32]float64, capacity int, lambda float64) map[uint32]string {
	baseResult := s.SolveNegativePressure(beds, bedRisks, capacity)

	s.mu.RLock()
	hasPrev := len(s.prevAssignments) > 0
	prevAssign := make(map[uint32]string)
	for k, v := range s.prevAssignments {
		prevAssign[k] = v
	}
	s.mu.RUnlock()

	if !hasPrev || lambda <= 0 {
		return baseResult
	}

	prevNPBeds := make(map[uint32]bool)
	for bedID, room := range prevAssign {
		if len(room) >= 2 && room[:2] == "NP" {
			prevNPBeds[bedID] = true
		}
	}

	type bedStabilityPair struct {
		bedID uint32
		risk  float64
		wasNP bool
	}
	pairs := make([]bedStabilityPair, 0, len(beds))
	for _, bedID := range beds {
		pairs = append(pairs, bedStabilityPair{
			bedID: bedID,
			risk:  bedRisks[bedID],
			wasNP: prevNPBeds[bedID],
		})
	}

	sort.Slice(pairs, func(i, j int) bool {
		scoreI := pairs[i].risk
		if !pairs[i].wasNP {
			scoreI -= lambda * 0.3
		}
		scoreJ := pairs[j].risk
		if !pairs[j].wasNP {
			scoreJ -= lambda * 0.3
		}
		return scoreI > scoreJ
	})

	result := make(map[uint32]string)
	npCount := 0
	wardCount := 0
	for _, pair := range pairs {
		if npCount < capacity {
			npCount++
			result[pair.bedID] = fmt.Sprintf("NP-%03d", npCount)
		} else {
			wardCount++
			result[pair.bedID] = fmt.Sprintf("WARD-%03d", wardCount)
		}
	}
	return result
}

func (s *SolverAdapter) solveScheduleWithStability(beds []uint32, numNurses int, lambda float64) map[string][]uint32 {
	baseSchedule := s.SolveNurseSchedule(beds, numNurses)

	s.mu.RLock()
	hasPrev := len(s.prevSchedule) > 0
	prevSched := make(map[string][]uint32)
	for k, v := range s.prevSchedule {
		prevSched[k] = append([]uint32{}, v...)
	}
	s.mu.RUnlock()

	if !hasPrev || lambda <= 0 {
		return baseSchedule
	}

	prevBedNurse := make(map[uint32]string)
	for nurse, bedList := range prevSched {
		for _, bedID := range bedList {
			prevBedNurse[bedID] = nurse
		}
	}

	nurseBeds := make(map[string][]uint32)
	for nurse := range baseSchedule {
		nurseBeds[nurse] = make([]uint32, 0)
	}

	unassigned := make([]uint32, 0)
	for _, bedID := range beds {
		prevNurse := prevBedNurse[bedID]
		if prevNurse != "" {
			if _, exists := nurseBeds[prevNurse]; exists {
				nurseBeds[prevNurse] = append(nurseBeds[prevNurse], bedID)
				continue
			}
		}
		unassigned = append(unassigned, bedID)
	}

	s.localSearchNurseBalance(nurseBeds)

	nursesSorted := make([]string, 0, len(nurseBeds))
	for n := range nurseBeds {
		nursesSorted = append(nursesSorted, n)
	}
	sort.Strings(nursesSorted)

	for idx, bedID := range unassigned {
		nurseIdx := idx % numNurses
		if nurseIdx < len(nursesSorted) {
			nurseBeds[nursesSorted[nurseIdx]] = append(nurseBeds[nursesSorted[nurseIdx]], bedID)
		}
	}

	s.localSearchNurseBalance(nurseBeds)
	return nurseBeds
}

func (s *SolverAdapter) GenerateSuggestions(prevAssign, currAssign map[uint32]string, prevSchedule, currSchedule map[string][]uint32) []Suggestion {
	suggestions := make([]Suggestion, 0)

	if len(prevAssign) == 0 || len(currAssign) == 0 {
		return suggestions
	}

	prevRoomMap := make(map[uint32]string)
	for bedID, room := range prevAssign {
		prevRoomMap[bedID] = room
	}

	type sugWithPriority struct {
		sug      Suggestion
		priority int
	}

	var allSugs []sugWithPriority

	for bedID, currRoom := range currAssign {
		prevRoom, exists := prevRoomMap[bedID]
		if exists && prevRoom != currRoom {
			priority := 1
			if len(currRoom) >= 2 && currRoom[:2] == "NP" {
				priority = 3
			}
			allSugs = append(allSugs, sugWithPriority{
				sug: Suggestion{
					Type:   "room_swap",
					BedID:  bedID,
					From:   prevRoom,
					To:     currRoom,
					Reason: fmt.Sprintf("床位%d从%s转移至%s", bedID, prevRoom, currRoom),
				},
				priority: priority,
			})
		}
	}

	prevNurseMap := make(map[uint32]string)
	for nurse, beds := range prevSchedule {
		for _, bedID := range beds {
			prevNurseMap[bedID] = nurse
		}
	}

	currNurseMap := make(map[uint32]string)
	for nurse, beds := range currSchedule {
		for _, bedID := range beds {
			currNurseMap[bedID] = nurse
		}
	}

	for bedID, currNurse := range currNurseMap {
		prevNurse, exists := prevNurseMap[bedID]
		if exists && prevNurse != currNurse {
			allSugs = append(allSugs, sugWithPriority{
				sug: Suggestion{
					Type:   "nurse_reassign",
					BedID:  bedID,
					From:   prevNurse,
					To:     currNurse,
					Reason: fmt.Sprintf("床位%d由护士%s调整至%s", bedID, prevNurse, currNurse),
				},
				priority: 2,
			})
		}
	}

	sort.Slice(allSugs, func(i, j int) bool {
		return allSugs[i].priority > allSugs[j].priority
	})

	for _, sp := range allSugs {
		suggestions = append(suggestions, sp.sug)
	}

	return suggestions
}

func (s *SolverAdapter) FullSolve(req SolveRequest) *SolveResponse {
	bedRisksMap := make(map[uint32]float64)
	beds := make([]uint32, 0, len(req.BedRisks))
	for _, br := range req.BedRisks {
		bedRisksMap[br.BedID] = br.InfectionRisk
		beds = append(beds, br.BedID)
	}

	if len(beds) == 0 {
		for i := uint32(1); i <= 50; i++ {
			beds = append(beds, i)
			if _, exists := bedRisksMap[i]; !exists {
				bedRisksMap[i] = rand.Float64()
			}
		}
	}

	lambda := req.StabilityLambda
	if lambda == 0 {
		lambda = s.GetStabilityLambda()
	}

	npCapacity := int(req.NegativePressureBeds)
	if npCapacity <= 0 {
		npCapacity = 10
	}

	nursesPerShift := int(req.NursesPerShift)
	if nursesPerShift <= 0 {
		nursesPerShift = 3
	}
	totalNurses := nursesPerShift * 3

	assignments := s.solveWithStability(beds, bedRisksMap, npCapacity, lambda)
	schedule := s.solveScheduleWithStability(beds, totalNurses, lambda)
	objective := s.ComputeCost(assignments, schedule, bedRisksMap, npCapacity, req.ObjectiveWeight)

	stabilityCost := s.l1RegularizationCost(assignments, schedule, lambda)
	objective.TotalCost += stabilityCost

	s.mu.RLock()
	prevAssign := make(map[uint32]string)
	for k, v := range s.prevAssignments {
		prevAssign[k] = v
	}
	prevSched := make(map[string][]uint32)
	for k, v := range s.prevSchedule {
		prevSched[k] = append([]uint32{}, v...)
	}
	s.mu.RUnlock()

	assignChangeRate := s.computeAssignmentChangeRate(prevAssign, assignments)
	scheduleChangeRate := s.computeScheduleChangeRate(prevSched, schedule)
	overallChangeRate := (assignChangeRate + scheduleChangeRate) / 2.0

	suggestions := s.GenerateSuggestions(prevAssign, assignments, prevSched, schedule)

	solutionID := fmt.Sprintf("SOL-%s", time.Now().Format("20060102-150405"))

	objectiveMap := map[string]float64{
		"infection_risk":         objective.InfectionRisk,
		"nurse_workload_balance": objective.NurseWorkloadBalance,
		"room_utilization":       objective.RoomUtilization,
		"transport_distance":     objective.TransportDistance,
		"total_cost":             objective.TotalCost,
		"stability_cost":         stabilityCost,
	}

	s.mu.Lock()
	s.prevAssignments = assignments
	s.prevSchedule = schedule
	s.changeRateHistory = append(s.changeRateHistory, overallChangeRate)
	if len(s.changeRateHistory) > 100 {
		s.changeRateHistory = s.changeRateHistory[len(s.changeRateHistory)-100:]
	}
	s.totalSolves++
	s.solutionsCache[solutionID] = &CachedSolution{
		Assignments: assignments,
		Schedule:    schedule,
		Objective:   objectiveMap,
	}
	s.mu.Unlock()

	return &SolveResponse{
		SolutionID:       solutionID,
		Assignments:      assignments,
		Schedule:         schedule,
		Objective:        objectiveMap,
		UnmetNeeds:       make([]string, 0),
		Suggestions:      suggestions,
		Status:           "solved",
		ChangeRate:       overallChangeRate,
		IsGreedyFallback: false,
	}
}

func (s *SolverAdapter) GreedySolve(req SolveRequest) *SolveResponse {
	bedRisksMap := make(map[uint32]float64)
	beds := make([]uint32, 0, len(req.BedRisks))
	for _, br := range req.BedRisks {
		bedRisksMap[br.BedID] = br.InfectionRisk
		beds = append(beds, br.BedID)
	}

	if len(beds) == 0 {
		for i := uint32(1); i <= 50; i++ {
			beds = append(beds, i)
			if _, exists := bedRisksMap[i]; !exists {
				bedRisksMap[i] = rand.Float64()
			}
		}
	}

	npCapacity := int(req.NegativePressureBeds)
	if npCapacity <= 0 {
		npCapacity = 10
	}

	nursesPerShift := int(req.NursesPerShift)
	if nursesPerShift <= 0 {
		nursesPerShift = 3
	}
	totalNurses := nursesPerShift * 3

	assignments := s.SolveNegativePressure(beds, bedRisksMap, npCapacity)

	result := make(map[string][]uint32)
	for i := 1; i <= totalNurses; i++ {
		nurseID := fmt.Sprintf("Nurse-%03d", i)
		result[nurseID] = make([]uint32, 0)
	}
	sortedBeds := make([]uint32, len(beds))
	copy(sortedBeds, beds)
	sort.Slice(sortedBeds, func(i, j int) bool {
		return sortedBeds[i] < sortedBeds[j]
	})
	for idx, bedID := range sortedBeds {
		nurseIdx := (idx % totalNurses) + 1
		nurseID := fmt.Sprintf("Nurse-%03d", nurseIdx)
		result[nurseID] = append(result[nurseID], bedID)
	}
	schedule := result

	objective := s.ComputeCost(assignments, schedule, bedRisksMap, npCapacity, req.ObjectiveWeight)

	s.mu.RLock()
	prevAssign := make(map[uint32]string)
	for k, v := range s.prevAssignments {
		prevAssign[k] = v
	}
	prevSched := make(map[string][]uint32)
	for k, v := range s.prevSchedule {
		prevSched[k] = append([]uint32{}, v...)
	}
	s.mu.RUnlock()

	assignChangeRate := s.computeAssignmentChangeRate(prevAssign, assignments)
	scheduleChangeRate := s.computeScheduleChangeRate(prevSched, schedule)
	overallChangeRate := (assignChangeRate + scheduleChangeRate) / 2.0

	solutionID := fmt.Sprintf("SOL-GREEDY-%s", time.Now().Format("20060102-150405"))

	objectiveMap := map[string]float64{
		"infection_risk":         objective.InfectionRisk,
		"nurse_workload_balance": objective.NurseWorkloadBalance,
		"room_utilization":       objective.RoomUtilization,
		"transport_distance":     objective.TransportDistance,
		"total_cost":             objective.TotalCost,
	}

	return &SolveResponse{
		SolutionID:       solutionID,
		Assignments:      assignments,
		Schedule:         schedule,
		Objective:        objectiveMap,
		UnmetNeeds:       make([]string, 0),
		Suggestions:      make([]Suggestion, 0),
		Status:           "greedy_fallback",
		ChangeRate:       overallChangeRate,
		IsGreedyFallback: true,
	}
}

func (s *SolverAdapter) GetCachedSuggestions(prevID, currID string) []Suggestion {
	s.mu.RLock()
	prev, prevOK := s.solutionsCache[prevID]
	curr, currOK := s.solutionsCache[currID]
	s.mu.RUnlock()

	if !prevOK || !currOK {
		return make([]Suggestion, 0)
	}

	return s.GenerateSuggestions(prev.Assignments, curr.Assignments, prev.Schedule, curr.Schedule)
}
