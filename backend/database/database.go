package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/gnn_resistance"
	"field-hospital-icu/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

var DB *pgxpool.Pool

func InitDB() {
	cfg := config.AppConfig.Database
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, cfg.SSLMode)

	var err error
	DB, err = pgxpool.New(context.Background(), connStr)
	if err != nil {
		log.Fatalf("无法连接数据库: %v", err)
	}

	if err = DB.Ping(context.Background()); err != nil {
		log.Fatalf("数据库Ping失败: %v", err)
	}
}

func CloseDB() {
	if DB != nil {
		DB.Close()
	}
}

func InitSchema() {
	schemaSQL := `
	CREATE EXTENSION IF NOT EXISTS timescaledb;

	CREATE TABLE IF NOT EXISTS beds (
		id SERIAL PRIMARY KEY,
		bed_code VARCHAR(20) UNIQUE NOT NULL,
		patient_name VARCHAR(100),
		patient_age INT,
		patient_gender VARCHAR(10),
		status VARCHAR(20) DEFAULT 'empty',
		admission_time TIMESTAMPTZ,
		location_x FLOAT,
		location_y FLOAT,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS vital_signs (
		time TIMESTAMPTZ NOT NULL,
		bed_id INT NOT NULL REFERENCES beds(id),
		sensor_type VARCHAR(30) NOT NULL,
		value FLOAT NOT NULL,
		unit VARCHAR(20)
	);

	SELECT create_hypertable('vital_signs', 'time', if_not_exists => TRUE);
	CREATE INDEX IF NOT EXISTS idx_vital_signs_bed_time ON vital_signs (bed_id, time DESC);
	CREATE INDEX IF NOT EXISTS idx_vital_signs_sensor ON vital_signs (sensor_type, time DESC);

	CREATE TABLE IF NOT EXISTS medical_records (
		id SERIAL PRIMARY KEY,
		bed_id INT NOT NULL REFERENCES beds(id),
		record_type VARCHAR(30) NOT NULL,
		record_data JSONB,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS infection_history (
		id SERIAL PRIMARY KEY,
		bed_id INT NOT NULL REFERENCES beds(id),
		antibiotic_type VARCHAR(100),
		dosage FLOAT,
		start_date TIMESTAMPTZ,
		end_date TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS invasive_procedures (
		id SERIAL PRIMARY KEY,
		bed_id INT NOT NULL REFERENCES beds(id),
		procedure_type VARCHAR(100),
		procedure_time TIMESTAMPTZ DEFAULT NOW(),
		notes TEXT
	);

	CREATE TABLE IF NOT EXISTS predictions (
		time TIMESTAMPTZ NOT NULL,
		bed_id INT NOT NULL REFERENCES beds(id),
		sepsis_risk FLOAT,
		sepsis_probability FLOAT,
		cre_risk FLOAT,
		mrsa_risk FLOAT,
		sofa_score FLOAT,
		PRIMARY KEY (time, bed_id)
	);

	SELECT create_hypertable('predictions', 'time', if_not_exists => TRUE);

	CREATE TABLE IF NOT EXISTS alerts (
		id SERIAL PRIMARY KEY,
		bed_id INT NOT NULL REFERENCES beds(id),
		alert_type VARCHAR(50) NOT NULL,
		severity VARCHAR(20) NOT NULL,
		message TEXT,
		trigger_value FLOAT,
		threshold FLOAT,
		acknowledged BOOLEAN DEFAULT FALSE,
		acknowledged_by VARCHAR(50),
		acknowledged_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_alerts_active ON alerts (acknowledged, created_at DESC);
	`

	_, err := DB.Exec(context.Background(), schemaSQL)
	if err != nil {
		log.Fatalf("Schema初始化失败: %v", err)
	}
}

func SeedBedData() {
	var count int
	err := DB.QueryRow(context.Background(), "SELECT COUNT(*) FROM beds").Scan(&count)
	if err != nil {
		log.Printf("查询床位数量失败: %v", err)
		return
	}

	if count >= 50 {
		log.Printf("已有 %d 条床位数据，跳过初始化", count)
		return
	}

	tx, err := DB.Begin(context.Background())
	if err != nil {
		log.Printf("开启事务失败: %v", err)
		return
	}
	defer tx.Rollback(context.Background())

	for i := 1; i <= 50; i++ {
		row := (i - 1) / 10
		col := (i - 1) % 10
		bedCode := fmt.Sprintf("ICU-%03d", i)

		_, err := tx.Exec(context.Background(),
			`INSERT INTO beds (bed_code, patient_name, patient_age, patient_gender, status, location_x, location_y, admission_time)
			 VALUES ($1, $2, $3, $4, 'occupied', $5, $6, NOW())
			 ON CONFLICT (bed_code) DO NOTHING`,
			bedCode,
			fmt.Sprintf("患者%d", i),
			30+i%40,
			[]string{"男", "女"}[i%2],
			float64(col)*100+50,
			float64(row)*100+50,
		)
		if err != nil {
			log.Printf("插入床位 %d 失败: %v", i, err)
		}
	}

	if err := tx.Commit(context.Background()); err != nil {
		log.Printf("提交事务失败: %v", err)
		return
	}

	log.Println("50张床位数据初始化完成")
}

func InitSeedData() {
	SeedBedData()

	rand.Seed(time.Now().UnixNano())

	ctx := context.Background()

	log.Println("开始插入 ventilator_params 随机初始值")
	for bedID := 1; bedID <= 50; bedID++ {
		numRecords := rand.Intn(4)
		for j := 0; j < numRecords; j++ {
			peakPressure := 15.0 + rand.Float64()*25.0
			tidalVolume := 300.0 + rand.Float64()*500.0
			oralSecretionGrade := rand.Float64() * 3.0
			ventilatorHours := rand.Intn(720)
			predictedWeight := 50.0 + rand.Float64()*40.0
			oralSecretion := rand.Float64() * 3.0
			priorInfection := rand.Float64()
			recordTime := time.Now().Add(-time.Duration(rand.Intn(72)) * time.Hour)

			sql := `INSERT INTO ventilator_params (bed_id, time, peak_pressure, tidal_volume, oral_secretion_grade, ventilator_hours, predicted_weight, oral_secretion, prior_infection)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
					ON CONFLICT (time, bed_id) DO NOTHING`
			_, err := DB.Exec(ctx, sql, bedID, recordTime, peakPressure, tidalVolume, oralSecretionGrade, ventilatorHours, predictedWeight, oralSecretion, priorInfection)
			if err != nil {
				log.Fatalf("插入 ventilator_params 失败 (bed_id=%d): %v", bedID, err)
			}
		}
	}
	log.Println("ventilator_params 随机初始值插入完成")

	log.Println("开始插入 culture_results 培养结果")
	bacteriaNames := []string{
		"Klebsiella pneumoniae",
		"Pseudomonas aeruginosa",
		"E. coli",
		"MRSA",
		"Acinetobacter baumannii",
	}
	numCultureBeds := 3 + rand.Intn(3)
	cultureBedSet := make(map[int]bool)
	for len(cultureBedSet) < numCultureBeds {
		cultureBedSet[1+rand.Intn(50)] = true
	}

	resistanceGenesList := []string{"KPC", "NDM", "VIM", "IMP", "OXA-48", "mecA", "vanA", "CTX-M"}
	antibioticList := []string{"meropenem", "imipenem", "ceftazidime", "ciprofloxacin", "amikacin", "vancomycin", "piperacillin-tazobactam"}

	positiveBedIDs := make([]uint32, 0)
	for bedID := range cultureBedSet {
		bacteriaName := bacteriaNames[rand.Intn(len(bacteriaNames))]
		numGenes := 1 + rand.Intn(3)
		genesSet := make(map[string]bool)
		for len(genesSet) < numGenes {
			genesSet[resistanceGenesList[rand.Intn(len(resistanceGenesList))]] = true
		}
		resistanceGenes := ""
		for g := range genesSet {
			if resistanceGenes != "" {
				resistanceGenes += ","
			}
			resistanceGenes += g
		}

		abxSensitivity := make(map[string]string)
		for _, abx := range antibioticList {
			result := "sensitive"
			if rand.Float64() < 0.4 {
				result = "resistant"
			} else if rand.Float64() < 0.6 {
				result = "intermediate"
			}
			abxSensitivity[abx] = result
		}
		abxJSON, _ := json.Marshal(abxSensitivity)

		cultureTime := time.Now().Add(-time.Duration(rand.Intn(168)) * time.Hour)
		collectedAt := cultureTime.Add(-24 * time.Hour)
		reportedAt := cultureTime
		resultVal := "positive"

		sql := `INSERT INTO culture_results (bed_id, bacteria_name, resistance_genes, antibiotic_sensitivity, time, result, collected_at, reported_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`
		var crID int
		err := DB.QueryRow(ctx, sql, bedID, bacteriaName, resistanceGenes, string(abxJSON), cultureTime, resultVal, collectedAt, reportedAt).Scan(&crID)
		if err != nil {
			log.Fatalf("插入 culture_results 失败 (bed_id=%d): %v", bedID, err)
		}
		positiveBedIDs = append(positiveBedIDs, uint32(bedID))

		if gnnInstance := gnn_resistance.GetInstance(); gnnInstance != nil {
			cr := models.CultureResult{
				ID:                    uint32(crID),
				BedID:                 uint32(bedID),
				BacteriaName:          bacteriaName,
				ResistanceGenes:       resistanceGenes,
				AntibioticSensitivity: string(abxJSON),
				Time:                  cultureTime,
				Result:                resultVal,
				CollectedAt:           collectedAt,
				ReportedAt:            reportedAt,
			}
			gnnInstance.UpdateCultureResult(cr)
			log.Printf("GNN: 已更新床位 %d 的培养结果", bedID)
		}
	}
	log.Printf("culture_results 插入完成，共 %d 个阳性床位", len(positiveBedIDs))

	log.Println("开始插入 optimizer_solutions 历史记录")
	for i := 0; i < 2; i++ {
		solveTimeMs := 0
		npAssign, _ := json.Marshal(map[string]interface{}{})
		nurseSched, _ := json.Marshal(map[string]interface{}{})
		objectiveJSON, _ := json.Marshal(map[string]float64{
			"infection_risk":         0.0,
			"nurse_workload_balance": 0.0,
			"room_utilization":       0.0,
			"transport_distance":     0.0,
			"total_cost":             0.0,
		})
		decisionsJSON, _ := json.Marshal([]map[string]interface{}{})
		unmetNeeds := []string{}
		cost := 0.0
		status := "initial"
		solTime := time.Now().Add(-time.Duration(2-i) * time.Hour)
		solutionID := fmt.Sprintf("SOL-INIT-%d", i)

		sql := `INSERT INTO optimizer_solutions (solve_time_ms, negative_pressure_assign, nurse_schedule, unmet_needs, cost, status, time, solution_id, timestamp, assignments, schedule, objective, decisions)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`
		_, err := DB.Exec(ctx, sql, solveTimeMs, string(npAssign), string(nurseSched), unmetNeeds, cost, status, solTime, solutionID, solTime, string(npAssign), string(nurseSched), string(objectiveJSON), string(decisionsJSON))
		if err != nil {
			log.Fatalf("插入 optimizer_solutions 失败: %v", err)
		}
	}
	log.Println("optimizer_solutions 历史记录插入完成")

	log.Println("开始插入 transport_requests + transport_risk_results 历史记录")
	riskLevels := []string{"low", "medium", "high", "critical"}
	recommendationsPool := []string{
		"建议使用负压救护车",
		"增加医护人员陪护",
		"提前准备急救设备",
		"优化转运路线",
		"中途增加监测点",
		"通知接收科室做好准备",
	}

	for i := 0; i < 3; i++ {
		fromBed := 1 + rand.Intn(50)
		toBed := 1 + rand.Intn(50)
		for toBed == fromBed {
			toBed = 1 + rand.Intn(50)
		}
		distanceMeters := 50 + rand.Intn(450)
		priority := rand.Intn(5)
		urgent := rand.Float64() < 0.3
		reqTime := time.Now().Add(-time.Duration(3-i) * time.Hour)
		distance := float64(distanceMeters)
		hourOfDay := reqTime.Hour()
		patientAge := 30 + rand.Intn(50)
		bedID := uint32(fromBed)

		sql := `INSERT INTO transport_requests (from_bed, to_bed, distance_meters, priority, urgent, time, request_id, bed_id, distance, hour_of_day, from_bed_id, to_bed_id, patient_age)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`
		var reqID int
		err := DB.QueryRow(ctx, sql, fromBed, toBed, distanceMeters, priority, urgent, reqTime, uint32(i+1), bedID, distance, hourOfDay, uint32(fromBed), uint32(toBed), patientAge).Scan(&reqID)
		if err != nil {
			log.Fatalf("插入 transport_requests 失败: %v", err)
		}

		riskScore := rand.Intn(100)
		riskLevelIdx := 0
		if riskScore >= 80 {
			riskLevelIdx = 3
		} else if riskScore >= 60 {
			riskLevelIdx = 2
		} else if riskScore >= 30 {
			riskLevelIdx = 1
		}
		riskLevel := riskLevels[riskLevelIdx]
		adverseEventProb := float64(riskScore) / 100.0

		featureContrib := map[string]float64{
			"distance":        0.1 + rand.Float64()*0.3,
			"priority":        0.1 + rand.Float64()*0.25,
			"patient_condition": 0.1 + rand.Float64()*0.35,
			"equipment_need":  0.05 + rand.Float64()*0.2,
		}
		fcJSON, _ := json.Marshal(featureContrib)

		numRecs := 1 + rand.Intn(3)
		recSet := make(map[string]bool)
		for len(recSet) < numRecs {
			recSet[recommendationsPool[rand.Intn(len(recommendationsPool))]] = true
		}
		recommendations := make([]string, 0, len(recSet))
		for r := range recSet {
			recommendations = append(recommendations, r)
		}

		resultTime := reqTime.Add(5 * time.Minute)

		sql2 := `INSERT INTO transport_risk_results (request_id, risk_score, risk_level, adverse_event_prob, feature_contrib, recommendations, time, timestamp, bed_id)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
		_, err = DB.Exec(ctx, sql2, reqID, riskScore, riskLevel, adverseEventProb, string(fcJSON), recommendations, resultTime, resultTime, bedID)
		if err != nil {
			log.Fatalf("插入 transport_risk_results 失败 (request_id=%d): %v", reqID, err)
		}
	}
	log.Println("transport_requests + transport_risk_results 历史记录插入完成")

	log.Println("InitSeedData 全部完成")
}
