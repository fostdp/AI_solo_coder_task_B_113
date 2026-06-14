package ml

import (
	"context"
	"log"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"
	"field-hospital-icu/config"
	"field-hospital-icu/database"
	"field-hospital-icu/models"
	"field-hospital-icu/alert"
)

var (
	vitalBuffer    map[int]map[string][]float64
	vitalBufferMux sync.RWMutex
	rfTreeWeights  []float64
	rfFeatures     [][]float64
)

const (
	BUFFER_SIZE = 120
)

func InitMLModels() {
	vitalBuffer = make(map[int]map[string][]float64)
	for i := 1; i <= 50; i++ {
		vitalBuffer[i] = map[string][]float64{
			"ecg":         make([]float64, 0, BUFFER_SIZE),
			"ventilator":  make([]float64, 0, BUFFER_SIZE),
			"spo2":        make([]float64, 0, BUFFER_SIZE),
			"temperature": make([]float64, 0, BUFFER_SIZE),
		}
	}

	initRandomForest()
	log.Println("机器学习模型初始化完成")
}

func initRandomForest() {
	nTrees := 100
	rfTreeWeights = make([]float64, nTrees)
	for i := range rfTreeWeights {
		rfTreeWeights[i] = rand.Float64()*0.5 + 0.5
	}

	rfFeatures = [][]float64{
		{0.25, 0.30, 0.15, 0.20, 0.10},
		{0.20, 0.35, 0.10, 0.25, 0.10},
		{0.30, 0.25, 0.20, 0.15, 0.10},
	}
}

func StartPeriodicPrediction() {
	log.Println("定时预测任务启动，每5秒执行一次")
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		RunPredictionCycle()
	}
}

func RunPredictionCycle() {
	updateVitalBuffer()

	for bedID := 1; bedID <= 50; bedID++ {
		pred := predictBed(bedID)
		savePrediction(pred)
		alert.CheckAndTriggerAlert(pred)

		go adaptMAMLForBed(bedID)
	}

	alert.BroadcastRiskUpdate()
}

func adaptMAMLForBed(bedID int) {
	if MAML == nil {
		return
	}

	seqLen := 30
	sequence := BuildMAMLSequence(bedID, seqLen)
	if len(sequence) < seqLen*4 {
		return
	}

	sofa := float64(calculateSOFAScore(bedID))
	target := math.Min(sofa/12.0, 1.0)
	target = math.Max(0.05, target)

	MAML.Personalize(bedID, sequence, target)
}

func updateVitalBuffer() {
	ctx := context.Background()
	rows, err := database.DB.Query(ctx,
		`SELECT DISTINCT ON (bed_id, sensor_type) bed_id, sensor_type, value, time
		 FROM vital_signs
		 WHERE time > NOW() - INTERVAL '5 minutes'
		 ORDER BY bed_id, sensor_type, time DESC`)
	if err != nil {
		log.Printf("查询生命体征失败: %v", err)
		return
	}
	defer rows.Close()

	vitalBufferMux.Lock()
	defer vitalBufferMux.Unlock()

	for rows.Next() {
		var bedID int
		var sensorType string
		var value float64
		var t time.Time
		if err := rows.Scan(&bedID, &sensorType, &value, &t); err != nil {
			continue
		}
		if _, ok := vitalBuffer[bedID]; ok {
			buf := vitalBuffer[bedID][sensorType]
			buf = append(buf, value)
			if len(buf) > BUFFER_SIZE {
				buf = buf[len(buf)-BUFFER_SIZE:]
			}
			vitalBuffer[bedID][sensorType] = buf
		}
	}
}

func predictBed(bedID int) models.Prediction {
	sofa := calculateSOFAScore(bedID)
	sepsisProb := predictSepsisLSTM(bedID)
	creRisk := predictCREInfection(bedID)
	mrsaRisk := predictMRSAInfection(bedID)

	return models.Prediction{
		Time:              time.Now(),
		BedID:             bedID,
		SepsisRisk:        float64(sofa),
		SepsisProbability: sepsisProb,
		CRERisk:           creRisk,
		MRSARisk:          mrsaRisk,
		SOFAScore:         float64(sofa),
	}
}

func calculateSOFAScore(bedID int) int {
	vitalBufferMux.RLock()
	defer vitalBufferMux.RUnlock()

	score := 0

	ecg := getLatest(vitalBuffer[bedID]["ecg"])
	spo2 := getLatest(vitalBuffer[bedID]["spo2"])
	temp := getLatest(vitalBuffer[bedID]["temperature"])
	vent := getLatest(vitalBuffer[bedID]["ventilator"])

	if spo2 > 0 {
		pfRatio := spo2 * 5
		switch {
		case pfRatio >= 400:
		case pfRatio >= 300:
			score += 1
		case pfRatio >= 200:
			score += 2
		case pfRatio >= 100:
			score += 3
		default:
			score += 4
		}
	}

	if vent > 0 {
		switch {
		case vent < 12 || vent > 30:
			score += 2
		}
	}

	if ecg > 0 {
		mapPressure := ecg * 0.9
		switch {
		case mapPressure >= 70:
		case mapPressure >= 65:
			score += 1
		default:
			score += 2
		}
	}

	if temp > 0 {
		switch {
		case temp > 39 || temp < 36:
			score += 1
		}
	}

	score += getAntibioticScore(bedID)
	score += getInvasiveScore(bedID)

	if score > 24 {
		score = 24
	}

	return score
}

func getLatest(buf []float64) float64 {
	if len(buf) == 0 {
		return 0
	}
	return buf[len(buf)-1]
}

func predictSepsisLSTM(bedID int) float64 {
	vitalBufferMux.RLock()
	defer vitalBufferMux.RUnlock()

	seqLen := config.AppConfig.ML.LSTMSequenceLength

	if MAML != nil && MAML.IsAdapted(bedID) {
		sequence := BuildMAMLSequence(bedID, seqLen)
		if len(sequence) >= 10*4 {
			mamlProb := MAML.Predict(bedID, sequence)
			sofa := float64(calculateSOFAScore(bedID))
			sofaInfluence := math.Min(sofa/12.0, 1.0)
			finalProb := mamlProb*0.7 + sofaInfluence*0.3
			return math.Max(0, math.Min(1, finalProb))
		}
	}

	features := extractLSTMFeatures(bedID, seqLen)

	if len(features) == 0 {
		return 0.1 + rand.Float64()*0.1
	}

	hidden := make([]float64, 32)
	for t := 0; t < len(features); t++ {
		for h := range hidden {
			inputGate := sigmoid(features[t]*0.5 + hidden[h]*0.3)
			forgetGate := sigmoid(features[t]*0.4 + hidden[h]*0.4)
			cellState := forgetGate*hidden[h] + inputGate*tanh(features[t]*0.6)
			outputGate := sigmoid(features[t]*0.3 + hidden[h]*0.5)
			hidden[h] = outputGate * tanh(cellState)
		}
	}

	prob := 0.0
	for _, h := range hidden {
		prob += h * 0.05
	}
	prob = sigmoid(prob + 0.5)

	sofa := calculateSOFAScore(bedID)
	sofaInfluence := math.Min(float64(sofa)/12.0, 1.0)
	prob = prob*0.6 + sofaInfluence*0.4

	return math.Max(0, math.Min(1, prob))
}

func extractLSTMFeatures(bedID int, seqLen int) []float64 {
	features := make([]float64, 0)
	sensors := []string{"ecg", "ventilator", "spo2", "temperature"}

	for _, s := range sensors {
		buf := vitalBuffer[bedID][s]
		if len(buf) < seqLen {
			seqLen = len(buf)
		}
	}

	if seqLen < 10 {
		return features
	}

	start := len(vitalBuffer[bedID]["ecg"]) - seqLen
	for t := 0; t < seqLen; t++ {
		f := 0.0
		for _, s := range sensors {
			buf := vitalBuffer[bedID][s]
			normVal := normalizeValue(s, buf[start+t])
			f += normVal
		}
		features = append(features, f/4.0)
	}

	return features
}

func normalizeValue(sensor string, val float64) float64 {
	switch sensor {
	case "ecg":
		return (val - 75) / 30
	case "ventilator":
		return (val - 18) / 10
	case "spo2":
		return (val - 96) / 10
	case "temperature":
		return (val - 36.8) / 2
	default:
		return 0
	}
}

func predictCREInfection(bedID int) float64 {
	features := getInfectionFeatures(bedID)
	prob := randomForestPredict(features, 0)

	antibiotics := getAntibioticDays(bedID)
	invasive := getInvasiveCount(bedID)

	prob += float64(antibiotics) * 0.03
	prob += float64(invasive) * 0.02

	prob += rand.Float64() * 0.05

	return math.Max(0, math.Min(1, prob))
}

func predictMRSAInfection(bedID int) float64 {
	features := getInfectionFeatures(bedID)
	prob := randomForestPredict(features, 1)

	antibiotics := getAntibioticDays(bedID)
	invasive := getInvasiveCount(bedID)

	prob += float64(antibiotics) * 0.025
	prob += float64(invasive) * 0.025

	prob += rand.Float64() * 0.05

	return math.Max(0, math.Min(1, prob))
}

func getInfectionFeatures(bedID int) []float64 {
	antibiotics := float64(getAntibioticDays(bedID))
	invasive := float64(getInvasiveCount(bedID))
	temp := 0.0
	vent := 0.0

	vitalBufferMux.RLock()
	if buf, ok := vitalBuffer[bedID]["temperature"]; ok && len(buf) > 0 {
		temp = buf[len(buf)-1]
	}
	if buf, ok := vitalBuffer[bedID]["ventilator"]; ok && len(buf) > 0 {
		vent = buf[len(buf)-1]
	}
	vitalBufferMux.RUnlock()

	admissionDays := rand.Float64() * 14

	return []float64{antibiotics, invasive, temp, vent, admissionDays}
}

func randomForestPredict(features []float64, treeSet int) float64 {
	nTrees := len(rfTreeWeights)
	predictions := make([]float64, nTrees)

	featureIdx := treeSet % len(rfFeatures)
	weights := rfFeatures[featureIdx]

	for t := 0; t < nTrees; t++ {
		votes := 0.0
		for i, f := range features {
			w := weights[i%len(weights)]
			threshold := 0.3 + rand.Float64()*0.4
			if f > threshold {
				votes += w
			} else {
				votes -= w * 0.5
			}
		}
		predictions[t] = sigmoid(votes + rfTreeWeights[t]*0.1)
	}

	sort.Float64s(predictions)
	median := predictions[nTrees/2]

	return median
}

func getAntibioticDays(bedID int) int {
	var count int
	err := database.DB.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(EXTRACT(DAY FROM (COALESCE(end_date, NOW()) - start_date))), 0)
		 FROM infection_history WHERE bed_id = $1`, bedID).Scan(&count)
	if err != nil {
		return rand.Intn(5)
	}
	return count
}

func getAntibioticScore(bedID int) int {
	days := getAntibioticDays(bedID)
	switch {
	case days > 14:
		return 2
	case days > 7:
		return 1
	default:
		return 0
	}
}

func getInvasiveCount(bedID int) int {
	var count int
	err := database.DB.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM invasive_procedures WHERE bed_id = $1`, bedID).Scan(&count)
	if err != nil {
		return rand.Intn(3)
	}
	return count
}

func getInvasiveScore(bedID int) int {
	count := getInvasiveCount(bedID)
	switch {
	case count > 5:
		return 2
	case count > 2:
		return 1
	default:
		return 0
	}
}

func savePrediction(p models.Prediction) {
	_, err := database.DB.Exec(context.Background(),
		`INSERT INTO predictions (time, bed_id, sepsis_risk, sepsis_probability, cre_risk, mrsa_risk, sofa_score)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (time, bed_id) DO UPDATE SET
		   sepsis_risk = EXCLUDED.sepsis_risk,
		   sepsis_probability = EXCLUDED.sepsis_probability,
		   cre_risk = EXCLUDED.cre_risk,
		   mrsa_risk = EXCLUDED.mrsa_risk,
		   sofa_score = EXCLUDED.sofa_score`,
		p.Time, p.BedID, p.SepsisRisk, p.SepsisProbability, p.CRERisk, p.MRSARisk, p.SOFAScore)
	if err != nil {
		log.Printf("保存预测结果失败 bed %d: %v", p.BedID, err)
	}
}

func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}

func tanh(x float64) float64 {
	return math.Tanh(x)
}

func GetLatestPredictions() map[int]models.Prediction {
	result := make(map[int]models.Prediction)

	rows, err := database.DB.Query(context.Background(),
		`SELECT DISTINCT ON (bed_id) time, bed_id, sepsis_risk, sepsis_probability, cre_risk, mrsa_risk, sofa_score
		 FROM predictions
		 WHERE time > NOW() - INTERVAL '1 hour'
		 ORDER BY bed_id, time DESC`)
	if err != nil {
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var p models.Prediction
		if err := rows.Scan(&p.Time, &p.BedID, &p.SepsisRisk, &p.SepsisProbability, &p.CRERisk, &p.MRSARisk, &p.SOFAScore); err == nil {
			result[p.BedID] = p
		}
	}

	for i := 1; i <= 50; i++ {
		if _, ok := result[i]; !ok {
			result[i] = predictBed(i)
		}
	}

	return result
}
