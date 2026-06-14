package optimizer

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

type InfectionRiskProvider interface {
	GetInfectionRisk(bedID uint32) float64
}

type BedOptimizer struct {
	cfg                  config.OptimizerConfig
	infectionRiskProvider InfectionRiskProvider
	bedOccupancy         map[uint32]bool
	latestSolution       *models.OptimizerSolution
	latestSuggestion     []map[string]interface{}
	stabilityLambda      float64
	changeRateHistory    []float64
	prevAssignments      map[uint32]string
	prevSchedule         map[string][]uint32
	mu                   sync.RWMutex
	stopChan             chan struct{}
	wg                   sync.WaitGroup
}

var Instance *BedOptimizer

func NewBedOptimizer(cfg config.OptimizerConfig) *BedOptimizer {
	opt := &BedOptimizer{
		cfg:              cfg,
		bedOccupancy:     make(map[uint32]bool),
		latestSuggestion: make([]map[string]interface{}, 0),
		stopChan:         make(chan struct{}),
		stabilityLambda:  0.5,
		changeRateHistory: make([]float64, 0, 100),
		prevAssignments:   make(map[uint32]string),
		prevSchedule:      make(map[string][]uint32),
	}
	Instance = opt
	return opt
}

func (b *BedOptimizer) SetInfectionRiskProvider(p InfectionRiskProvider) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.infectionRiskProvider = p
}

func (b *BedOptimizer) Start() {
	b.wg.Add(1)
	go b.Run()
}

func (b *BedOptimizer) Stop() {
	close(b.stopChan)
	b.wg.Wait()
}

func (b *BedOptimizer) Run() {
	defer b.wg.Done()

	interval := b.cfg.SolveIntervalSec
	if interval <= 0 {
		interval = 60
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	b.SolveAndBroadcast()

	for {
		select {
		case <-b.stopChan:
			return
		case <-ticker.C:
			b.SolveAndBroadcast()
		}
	}
}

func (b *BedOptimizer) SolveAndBroadcast() {
	b.mu.Lock()
	defer b.mu.Unlock()

	var beds []uint32
	for bedID, occupied := range b.bedOccupancy {
		if occupied {
			beds = append(beds, bedID)
		}
	}
	if len(beds) == 0 {
		beds = make([]uint32, 0)
		for i := uint32(1); i <= 50; i++ {
			beds = append(beds, i)
		}
	}

	bedRisks := make(map[uint32]float64)
	for _, bedID := range beds {
		if b.infectionRiskProvider != nil {
			bedRisks[bedID] = b.infectionRiskProvider.GetInfectionRisk(bedID)
		} else {
			bedRisks[bedID] = rand.Float64()
		}
	}

	npCapacity := b.cfg.NegativePressureBeds
	if npCapacity <= 0 {
		npCapacity = 10
	}
	assignments := b.solveWithStability(beds, bedRisks, npCapacity)

	nursesPerShift := b.cfg.NursesPerShift
	if nursesPerShift <= 0 {
		nursesPerShift = 3
	}
	totalNurses := nursesPerShift * 3
	schedule := b.solveScheduleWithStability(beds, totalNurses)

	objective := b.ComputeCost(assignments, schedule, bedRisks)

	stabilityCost := b.l1RegularizationCost(assignments, schedule)
	objective.TotalCost += stabilityCost

	assignChangeRate := b.computeAssignmentChangeRate(b.prevAssignments, assignments)
	scheduleChangeRate := b.computeScheduleChangeRate(b.prevSchedule, schedule)
	overallChangeRate := (assignChangeRate + scheduleChangeRate) / 2.0

	b.changeRateHistory = append(b.changeRateHistory, overallChangeRate)
	if len(b.changeRateHistory) > 100 {
		b.changeRateHistory = b.changeRateHistory[len(b.changeRateHistory)-100:]
	}

	decisions := make([]map[string]interface{}, 0, len(beds))
	for _, bedID := range beds {
		nurse := b.findNurseForBed(bedID, schedule)
		decisions = append(decisions, map[string]interface{}{
			"bed_id":         bedID,
			"assigned_room":  assignments[bedID],
			"assigned_nurse": nurse,
		})
	}

	solutionID := fmt.Sprintf("SOL-%s", time.Now().Format("20060102-150405"))
	now := time.Now()
	objectiveMap := map[string]float64{
		"infection_risk":         objective.InfectionRisk,
		"nurse_workload_balance": objective.NurseWorkloadBalance,
		"room_utilization":       objective.RoomUtilization,
		"transport_distance":     objective.TransportDistance,
		"total_cost":             objective.TotalCost,
	}
	newSolution := &models.OptimizerSolution{
		ID:                     0,
		SolutionID:             solutionID,
		Time:                   now,
		Timestamp:              now,
		NegativePressureAssign: assignments,
		Assignments:            assignments,
		NurseSchedule:          schedule,
		Schedule:               schedule,
		Objective:              objectiveMap,
		Decisions:              decisions,
		Cost:                   objective.TotalCost,
		UnmetNeeds:             make([]string, 0),
		Status:                 "solved",
		SolveTime:              0,
	}

	prevSolution := b.latestSolution
	b.latestSolution = newSolution
	b.latestSuggestion = b.GenerateSuggestions(prevSolution, newSolution)

	b.prevAssignments = assignments
	b.prevSchedule = schedule

	log.Printf("[OPTIMIZER] 解已生成: %s, 总成本=%.4f, 床位=%d, NP病房=%d, 护士=%d",
		solutionID, objective.TotalCost, len(beds), npCapacity, totalNurses)
}

func (b *BedOptimizer) findNurseForBed(bedID uint32, schedule map[string][]uint32) string {
	for nurse, bedList := range schedule {
		for _, bid := range bedList {
			if bid == bedID {
				return nurse
			}
		}
	}
	return ""
}

func (b *BedOptimizer) SolveNegativePressure(beds []uint32, bedRisks map[uint32]float64, capacity int) map[uint32]string {
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
	for idx, pair := range pairs {
		if npCount < capacity {
			npCount++
			result[pair.bedID] = fmt.Sprintf("NP-%03d", npCount)
		} else {
			wardCount++
			result[pair.bedID] = fmt.Sprintf("WARD-%03d", wardCount)
		}
		_ = idx
	}

	return result
}

func (b *BedOptimizer) SolveNurseSchedule(beds []uint32, numNurses int) map[string][]uint32 {
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

	b.localSearchNurseBalance(result)

	return result
}

func (b *BedOptimizer) localSearchNurseBalance(schedule map[string][]uint32) {
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

func (b *BedOptimizer) ComputeCost(assignments map[uint32]string, schedule map[string][]uint32, bedRisks map[uint32]float64) models.OptimizerObjective {
	var obj models.OptimizerObjective

	highRiskThreshold := 0.7
	npPenalty := 0.0
	totalNPBeds := 0
	usedNPBeds := 0

	for bedID, room := range assignments {
		if len(room) >= 2 && room[:2] == "NP" {
			usedNPBeds++
		}
		totalNPBeds++
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

	npCapacity := b.cfg.NegativePressureBeds
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
		totalDistance += float64(bedID%10) * rand.Float64() * 5.0
	}
	obj.TransportDistance = totalDistance

	weights := b.cfg.ObjectiveWeight
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

func (b *BedOptimizer) computeAssignmentChangeRate(
	prev, curr map[uint32]string,
) float64 {
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

func (b *BedOptimizer) computeScheduleChangeRate(
	prev, curr map[string][]uint32,
) float64 {
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

func (b *BedOptimizer) l1RegularizationCost(
	assignments map[uint32]string,
	schedule map[string][]uint32,
) float64 {
	b.mu.RLock()
	hasPrev := len(b.prevAssignments) > 0 && len(b.prevSchedule) > 0
	b.mu.RUnlock()

	if !hasPrev {
		return 0
	}

	assignPenalty := 0.0
	for bedID, currRoom := range assignments {
		b.mu.RLock()
		prevRoom := b.prevAssignments[bedID]
		b.mu.RUnlock()
		if prevRoom != "" && prevRoom != currRoom {
			assignPenalty += 1.0
		}
	}

	prevBedNurse := make(map[uint32]string)
	b.mu.RLock()
	for nurse, beds := range b.prevSchedule {
		for _, bedID := range beds {
			prevBedNurse[bedID] = nurse
		}
	}
	b.mu.RUnlock()

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

	return b.stabilityLambda * (assignPenalty + nursePenalty)
}

func (b *BedOptimizer) solveWithStability(
	beds []uint32,
	bedRisks map[uint32]float64,
	capacity int,
) map[uint32]string {
	baseResult := b.SolveNegativePressure(beds, bedRisks, capacity)

	b.mu.RLock()
	hasPrev := len(b.prevAssignments) > 0
	b.mu.RUnlock()

	if !hasPrev || b.stabilityLambda <= 0 {
		return baseResult
	}

	b.mu.RLock()
	prevNPBeds := make(map[uint32]bool)
	for bedID, room := range b.prevAssignments {
		if len(room) >= 2 && room[:2] == "NP" {
			prevNPBeds[bedID] = true
		}
	}
	b.mu.RUnlock()

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
			scoreI -= b.stabilityLambda * 0.3
		}
		scoreJ := pairs[j].risk
		if !pairs[j].wasNP {
			scoreJ -= b.stabilityLambda * 0.3
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

func (b *BedOptimizer) solveScheduleWithStability(
	beds []uint32,
	numNurses int,
) map[string][]uint32 {
	baseSchedule := b.SolveNurseSchedule(beds, numNurses)

	b.mu.RLock()
	hasPrev := len(b.prevSchedule) > 0
	b.mu.RUnlock()

	if !hasPrev || b.stabilityLambda <= 0 {
		return baseSchedule
	}

	b.mu.RLock()
	prevBedNurse := make(map[uint32]string)
	for nurse, bedList := range b.prevSchedule {
		for _, bedID := range bedList {
			prevBedNurse[bedID] = nurse
		}
	}
	b.mu.RUnlock()

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

	b.localSearchNurseBalance(nurseBeds)

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

	b.localSearchNurseBalance(nurseBeds)
	return nurseBeds
}

func (b *BedOptimizer) GenerateSuggestions(prev, curr *models.OptimizerSolution) []map[string]interface{} {
	suggestions := make([]map[string]interface{}, 0)

	if prev == nil || curr == nil {
		return suggestions
	}

	highRiskThreshold := 0.7

	prevRoomMap := make(map[uint32]string)
	for bedID, room := range prev.Assignments {
		prevRoomMap[bedID] = room
	}

	for bedID, currRoom := range curr.Assignments {
		prevRoom, exists := prevRoomMap[bedID]
		if exists && prevRoom != currRoom {
			priority := 1
			if len(currRoom) >= 2 && currRoom[:2] == "NP" {
				priority = 3
			}
			suggestions = append(suggestions, map[string]interface{}{
				"type":     "room_swap",
				"from_bed": bedID,
				"to_room":  currRoom,
				"from_room": prevRoom,
				"reason":   fmt.Sprintf("床位%d从%s转移至%s", bedID, prevRoom, currRoom),
				"priority": priority,
			})
		}
		_ = highRiskThreshold
	}

	prevNurseMap := make(map[uint32]string)
	for nurse, beds := range prev.Schedule {
		for _, bedID := range beds {
			prevNurseMap[bedID] = nurse
		}
	}

	currNurseMap := make(map[uint32]string)
	for nurse, beds := range curr.Schedule {
		for _, bedID := range beds {
			currNurseMap[bedID] = nurse
		}
	}

	for bedID, currNurse := range currNurseMap {
		prevNurse, exists := prevNurseMap[bedID]
		if exists && prevNurse != currNurse {
			suggestions = append(suggestions, map[string]interface{}{
				"type":          "nurse_reassign",
				"from_bed":      bedID,
				"from_nurse":    prevNurse,
				"to_nurse":      currNurse,
				"reason":        fmt.Sprintf("床位%d由护士%s调整至%s", bedID, prevNurse, currNurse),
				"priority":      2,
			})
		}
	}

	sort.Slice(suggestions, func(i, j int) bool {
		pi, _ := suggestions[i]["priority"].(int)
		pj, _ := suggestions[j]["priority"].(int)
		return pi > pj
	})

	return suggestions
}

func (b *BedOptimizer) GetLatestSolution() *models.OptimizerSolution {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.latestSolution
}

func (b *BedOptimizer) GetSuggestions() []map[string]interface{} {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]map[string]interface{}, len(b.latestSuggestion))
	copy(result, b.latestSuggestion)
	return result
}

func (b *BedOptimizer) GetLatestChangeRate() float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.changeRateHistory) == 0 {
		return 0.3
	}
	return b.changeRateHistory[len(b.changeRateHistory)-1]
}

func (b *BedOptimizer) GetAverageChangeRate() float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.changeRateHistory) == 0 {
		return 0.3
	}
	sum := 0.0
	for _, r := range b.changeRateHistory {
		sum += r
	}
	return sum / float64(len(b.changeRateHistory))
}

func (b *BedOptimizer) SetStabilityLambda(lambda float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if lambda < 0 {
		lambda = 0
	}
	if lambda > 2.0 {
		lambda = 2.0
	}
	b.stabilityLambda = lambda
}

func GetInstance() *BedOptimizer {
	return Instance
}
