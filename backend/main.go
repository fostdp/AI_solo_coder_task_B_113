package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/database"
	"field-hospital-icu/mqtt_ingest"
	"field-hospital-icu/sepsis_lstm"
	"field-hospital-icu/infection_rf"
	"field-hospital-icu/alert_ws"
	"field-hospital-icu/handlers"
	"field-hospital-icu/metrics"
	"field-hospital-icu/models"
	"field-hospital-icu/cox_vap"
	"field-hospital-icu/gnn_resistance"
	"field-hospital-icu/optimizer"
	"field-hospital-icu/transport_rf"

	"github.com/gin-gonic/gin"
	"github.com/gin-contrib/cors"
)

func main() {
	log.Println("=== 战地医院移动ICU生命体征监测系统启动 ===")

	metrics.SetupMetrics()
	log.Println("Prometheus metrics 端点启动: :6060/metrics")
	log.Println("pprof 调试端点启动: :6060/debug/pprof/")

	config.LoadConfig()
	log.Println("配置加载完成")

	database.InitDB()
	defer database.CloseDB()
	log.Println("数据库连接成功")

	database.InitSchema()
	log.Println("数据库Schema初始化完成")

	database.SeedBedData()
	log.Println("床位数据初始化完成")

	bedList := loadBedList()
	log.Printf("已加载 %d 条床位信息", len(bedList))

	database.InitBatchWriterFromConfig(config.AppConfig.BatchWriter)
	defer database.VitalWriter.Stop()
	log.Println("批量写入器启动完成")

	vitalsIngestChan := make(chan models.VitalSign, 10000)
	sepsisInChan := make(chan models.VitalSign, 5000)
	infectionInChan := make(chan models.VitalSign, 5000)
	sepsisOutChan := make(chan sepsis_lstm.SepsisPrediction, 100)
	infectionOutChan := make(chan infection_rf.InfectionPrediction, 100)
	sepsisAlertChan := make(chan alert_ws.SepsisMsg, 100)
	infectionAlertChan := make(chan alert_ws.InfectionMsg, 100)
	alertOutChan := make(chan models.Alert, 100)

	var ventilatorIn chan models.VentilatorParam
	var vapOut chan models.VapRiskRecord
	if config.AppConfig.CoxVap.Enabled {
		ventilatorIn = make(chan models.VentilatorParam, 1000)
		vapOut = make(chan models.VapRiskRecord, 50)
	}

	var transportIn chan models.TransportRequest
	var transportOut chan models.TransportRiskResult
	if config.AppConfig.Transport.Enabled {
		transportIn = make(chan models.TransportRequest, 100)
		transportOut = make(chan models.TransportRiskResult, 100)
	}

	ingester := mqtt_ingest.NewIngester(config.AppConfig.MQTT, vitalsIngestChan)
	if err := ingester.Start(config.AppConfig.MQTT); err != nil {
		log.Printf("MQTT摄入器启动警告: %v (将使用内置模拟器数据)", err)
	}
	defer ingester.Stop()
	log.Println("MQTT摄入模块启动完成（持久会话 + 解码Worker）")

	sepsisPredictor := sepsis_lstm.NewPredictor(config.AppConfig.ML, sepsisInChan, sepsisOutChan)
	sepsis_lstm.Instance = sepsisPredictor
	sepsisPredictor.Start()
	defer sepsisPredictor.Stop()
	log.Println("脓毒症LSTM推理模块启动完成（MAML元学习）")

	infectionPredictor := infection_rf.NewPredictor(config.AppConfig.ML, infectionInChan, infectionOutChan)
	infection_rf.Instance = infectionPredictor
	infectionPredictor.Start()
	defer infectionPredictor.Stop()
	log.Println("感染风险随机森林模块启动完成")

	if config.AppConfig.CoxVap.Enabled {
		coxPredictor := cox_vap.NewCoxPredictor(config.AppConfig.CoxVap, ventilatorIn, vapOut)
		cox_vap.Instance = coxPredictor
		coxPredictor.Start()
		log.Println("呼吸机相关性肺炎Cox风险预测模块启动完成")
	}

	if config.AppConfig.GNN.Enabled {
		gnnPredictor := gnn_resistance.NewGNNSpreadPredictor(config.AppConfig.GNN)
		if len(bedList) > 0 {
			gnnPredictor.BuildAdjacencyMatrix(bedList)
		}
		gnnPredictor.Start()
		log.Println("耐药菌传播GNN图神经网络模块启动完成")
	}

	if config.AppConfig.Optimizer.Enabled {
		bedOptimizer := optimizer.NewBedOptimizer(config.AppConfig.Optimizer)
		bedOptimizer.SetInfectionRiskProvider(infection_rf.Instance)
		bedOptimizer.Start()
		log.Println("床位资源优化分配求解器模块启动完成")
	}

	if config.AppConfig.Transport.Enabled {
		transportScorer := transport_rf.NewTransportScorer(config.AppConfig.Transport, transportIn, transportOut)
		transportScorer.SetProviders(sepsis_lstm.Instance, infection_rf.Instance)
		transportScorer.Start()
		log.Println("患者转运风险随机森林评分模块启动完成")
	}

	var vapAlertIn chan models.VapRiskRecord
	var transportAlertIn chan models.TransportRiskResult
	if config.AppConfig.CoxVap.Enabled {
		vapAlertIn = make(chan models.VapRiskRecord, 50)
	}
	if config.AppConfig.Transport.Enabled {
		transportAlertIn = make(chan models.TransportRiskResult, 50)
	}

	alertEngine := alert_ws.NewEngineExtended(config.AppConfig, sepsisAlertChan, infectionAlertChan, vapAlertIn, transportAlertIn, alertOutChan)
	alert_ws.EngineInstance = alertEngine
	alertEngine.StartExtended()
	defer alertEngine.Stop()
	log.Println("告警引擎 + WebSocket模块启动完成（扩展模式：VAP+转运告警）")

	go startFanOut(vitalsIngestChan, sepsisInChan, infectionInChan, ventilatorIn)
	log.Println("体征数据扇出通道启动")

	go startSepsisAdaptor(sepsisOutChan, sepsisAlertChan)
	go startInfectionAdaptor(infectionOutChan, infectionAlertChan)
	if config.AppConfig.CoxVap.Enabled {
		go startVapAdaptor(vapOut, alertOutChan, vapAlertIn)
		log.Println("VAP风险结果适配器启动")
	}
	if config.AppConfig.Transport.Enabled {
		go startTransportAdaptor(transportOut, transportAlertIn)
		log.Println("转运风险结果适配器启动")
	}
	log.Println("预测结果适配器启动")

	go startAlertPersistWorker(alertOutChan)
	log.Println("告警持久化Worker启动")

	go startRiskBroadcastWorker()
	log.Println("风险状态定时广播启动")

	shutdown := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigChan:
			log.Println("收到关闭信号，开始优雅关闭...")
			close(shutdown)
		}
	}()

	go func() {
		<-shutdown
		if config.AppConfig.GNN.Enabled && gnn_resistance.GetInstance() != nil {
			gnn_resistance.GetInstance().Stop()
			log.Println("GNN耐药菌模块已关闭")
		}
		if config.AppConfig.Optimizer.Enabled && optimizer.GetInstance() != nil {
			optimizer.GetInstance().Stop()
			log.Println("床位优化器模块已关闭")
		}
		if config.AppConfig.Transport.Enabled && transport_rf.GetInstance() != nil {
			transport_rf.GetInstance().Stop()
			log.Println("转运风险评分模块已关闭")
		}
		if config.AppConfig.CoxVap.Enabled && cox_vap.GetInstance() != nil {
			cox_vap.GetInstance().Stop()
			log.Println("Cox VAP预测模块已关闭")
		}
	}()

	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.Static("/css", "./frontend/css")
	r.Static("/js", "./frontend/js")
	r.StaticFile("/", "./frontend/index.html")

	api := r.Group("/api")
	{
		api.GET("/beds", handlers.GetBeds)
		api.GET("/beds/:id", handlers.GetBedByID)
		api.GET("/beds/:id/vitals", handlers.GetBedVitals)
		api.GET("/beds/:id/vitals/recent", handlers.GetRecentVitals)
		api.GET("/alerts", handlers.GetAlerts)
		api.GET("/alerts/active", handlers.GetActiveAlerts)
		api.GET("/infection/risk", handlers.GetInfectionRiskMap)
		api.GET("/statistics", handlers.GetStatistics)
		api.POST("/beds/:id/antibiotics", handlers.RecordAntibiotic)
		api.POST("/beds/:id/invasive", handlers.RecordInvasiveProcedure)
		api.POST("/alerts/:id/acknowledge", handlers.AcknowledgeAlert)

		api.GET("/vap", handlers.GetAllVapRisks)
		api.GET("/beds/:id/vap", handlers.GetBedVapRisk)

		api.GET("/resistance/predictions", handlers.GetAllResistancePredictions)
		api.GET("/resistance/heatmap", handlers.GetResistanceHeatmap)
		api.POST("/beds/:id/culture", handlers.SubmitCultureResult)

		api.GET("/optimizer/solution", handlers.GetLatestOptimizerSolution)
		api.GET("/optimizer/suggestions", handlers.GetOptimizerSuggestions)
		api.POST("/optimizer/solve", handlers.TriggerOptimizerSolve)

		api.GET("/transport/results", handlers.GetAllTransportResults)
		api.GET("/transport/results/:id", handlers.GetTransportResult)
		api.POST("/transport/evaluate", handlers.EvaluateTransport)
	}

	r.GET("/ws", handlers.WebSocketHandler)

	log.Printf("HTTP服务启动，监听端口: %s", config.AppConfig.Server.Port)
	if err := r.Run(":" + config.AppConfig.Server.Port); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}

func loadBedList() []models.Bed {
	rows, err := database.DB.Query(nil,
		`SELECT id, bed_code, COALESCE(patient_name, ''), COALESCE(patient_age, 0), COALESCE(patient_gender, ''),
		        COALESCE(status, ''), admission_time, location_x, location_y, created_at
		 FROM beds ORDER BY id`)
	if err != nil {
		log.Printf("加载床位列表失败: %v", err)
		return nil
	}
	defer rows.Close()

	beds := make([]models.Bed, 0)
	for rows.Next() {
		var b models.Bed
		if err := rows.Scan(&b.ID, &b.BedCode, &b.PatientName, &b.PatientAge,
			&b.PatientGender, &b.Status, &b.AdmissionTime, &b.LocationX, &b.LocationY, &b.CreatedAt); err == nil {
			beds = append(beds, b)
		}
	}
	return beds
}

func startFanOut(in <-chan models.VitalSign, sepsisOut, infectionOut chan<- models.VitalSign, ventilatorOut chan<- models.VentilatorParam) {
	for v := range in {
		if database.VitalWriter != nil {
			database.VitalWriter.Write(v)
		}

		select {
		case sepsisOut <- v:
		default:
		}

		select {
		case infectionOut <- v:
		default:
		}

		if ventilatorOut != nil && v.SensorType == "ventilator" {
			ventHours := estimateVentilatorHours(v.BedID)
			param := models.VentilatorParam{
				BedID:              uint32(v.BedID),
				PeakPressure:       v.Value + (rand.Float64()-0.5)*4.0,
				TidalVolume:        v.Value * 0.45,
				OralSecretionGrade: float64(rand.Intn(4)),
				VentilatorHours:    ventHours,
				Time:               v.Time,
			}
			select {
			case ventilatorOut <- param:
			default:
			}
		}
	}
}

func estimateVentilatorHours(bedID int) int {
	rows, err := database.DB.Query(nil,
		`SELECT admission_time FROM beds WHERE id = $1`, bedID)
	if err != nil {
		return rand.Intn(72)
	}
	defer rows.Close()

	for rows.Next() {
		var at *time.Time
		if err := rows.Scan(&at); err == nil && at != nil {
			return int(time.Since(*at).Hours())
		}
	}
	return rand.Intn(24)
}

func startSepsisAdaptor(in <-chan sepsis_lstm.SepsisPrediction, out chan<- alert_ws.SepsisMsg) {
	for p := range in {
		msg := alert_ws.SepsisMsg{
			BedID:       p.BedID,
			Probability: p.Probability,
			SOFAScore:   p.SOFAScore,
			Time:        p.Time,
		}
		select {
		case out <- msg:
		default:
		}
	}
}

func startInfectionAdaptor(in <-chan infection_rf.InfectionPrediction, out chan<- alert_ws.InfectionMsg) {
	for p := range in {
		msg := alert_ws.InfectionMsg{
			BedID:    p.BedID,
			CRERisk:  p.CRERisk,
			MRSARisk: p.MRSARisk,
			Time:     p.Time,
		}
		select {
		case out <- msg:
		default:
		}
	}
}

func startVapAdaptor(in <-chan models.VapRiskRecord, out chan<- models.Alert, vapAlert chan<- models.VapRiskRecord) {
	for p := range in {
		if p.RiskProbability > config.AppConfig.CoxVap.RiskThreshold {
			severity := "warning"
			if p.RiskProbability >= 0.8 {
				severity = "critical"
			} else if p.RiskProbability >= 0.65 {
				severity = "high"
			}
			alert := models.Alert{
				BedID:        int(p.BedID),
				AlertType:    "vap",
				Severity:     severity,
				Message:      fmt.Sprintf("床位%d VAP风险%.1f%%，预计%.1fh后发作", int(p.BedID), p.RiskProbability*100, p.PredictedOnsetHours),
				TriggerValue: p.RiskProbability,
				Threshold:    config.AppConfig.CoxVap.RiskThreshold,
				CreatedAt:    time.Now(),
			}
			select {
			case out <- alert:
			default:
			}
			if vapAlert != nil {
				select {
				case vapAlert <- p:
				default:
				}
			}
		}
	}
}

func startTransportAdaptor(in <-chan models.TransportRiskResult, transportAlert chan<- models.TransportRiskResult) {
	for r := range in {
		if transportAlert != nil {
			select {
			case transportAlert <- r:
			default:
			}
		}
		if alert_ws.EngineInstance != nil {
			alert_ws.EngineInstance.BroadcastOptimizationSuggestion([]map[string]interface{}{
				{
					"type":          "transport_risk_alert",
					"request_id":    r.RequestID,
					"bed_id":        r.BedID,
					"risk_score":    r.RiskScore,
					"risk_level":    r.RiskLevel,
					"recommendations": r.Recommendations,
				},
			})
		}
	}
}

func startAlertPersistWorker(in <-chan models.Alert) {
	for alert := range in {
		_, err := database.DB.Exec(nil,
			`INSERT INTO alerts (bed_id, alert_type, severity, message, trigger_value, threshold)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			alert.BedID, alert.AlertType, alert.Severity, alert.Message,
			alert.TriggerValue, alert.Threshold)
		if err != nil {
			log.Printf("保存告警失败 bed %d: %v", alert.BedID, err)
		}
	}
}

func startRiskBroadcastWorker() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if alert_ws.EngineInstance == nil {
			continue
		}

		riskData := buildRiskUpdateData()
		alert_ws.EngineInstance.BroadcastRiskUpdate(riskData)
	}
}

func buildRiskUpdateData() []map[string]interface{} {
	result := make([]map[string]interface{}, 0)

	rows, err := database.DB.Query(nil,
		`SELECT id, bed_code, patient_name, patient_age, patient_gender, status, admission_time, location_x, location_y, created_at
		 FROM beds ORDER BY id`)
	if err != nil {
		return result
	}
	defer rows.Close()

	beds := make([]models.Bed, 0)
	for rows.Next() {
		var b models.Bed
		if err := rows.Scan(&b.ID, &b.BedCode, &b.PatientName, &b.PatientAge,
			&b.PatientGender, &b.Status, &b.AdmissionTime, &b.LocationX, &b.LocationY, &b.CreatedAt); err == nil {
			beds = append(beds, b)
		}
	}

	vitals := make(map[int]map[string]float64)
	if sepsis_lstm.Instance != nil {
	}

	var vapRecords map[uint32]*models.VapRiskRecord
	if cox_vap.GetInstance() != nil {
		vapRecords = cox_vap.GetInstance().GetAllLatest()
	}

	var resistancePreds map[uint32]*models.ResistancePrediction
	if gnn_resistance.GetInstance() != nil {
		resistancePreds = gnn_resistance.GetInstance().GetAllPredictions()
	}

	var optimizerSuggestions []map[string]interface{}
	if optimizer.GetInstance() != nil {
		optimizerSuggestions = optimizer.GetInstance().GetSuggestions()
	}

	var transportResults map[uint32]*models.TransportRiskResult
	if transport_rf.GetInstance() != nil {
		transportResults = transport_rf.GetInstance().GetAllResults()
	}

	transportStats := make(map[string]int)
	if transportResults != nil {
		transportStats["total"] = len(transportResults)
		criticalCount := 0
		highCount := 0
		mediumCount := 0
		lowCount := 0
		for _, r := range transportResults {
			switch r.RiskLevel {
			case "critical":
				criticalCount++
			case "high":
				highCount++
			case "medium":
				mediumCount++
			case "low":
				lowCount++
			}
		}
		transportStats["critical"] = criticalCount
		transportStats["high"] = highCount
		transportStats["medium"] = mediumCount
		transportStats["low"] = lowCount
	}

	if infection_rf.Instance != nil {
		infectionPreds := infection_rf.Instance.PredictAll()
		sepsisPreds := sepsis_lstm.Instance.RunPredictionAll()

		for _, bed := range beds {
			item := map[string]interface{}{
				"bed": bed,
			}

			if v, ok := vitals[bed.ID]; ok {
				item["vitals"] = v
			} else {
				item["vitals"] = map[string]float64{}
			}

			risk := map[string]interface{}{}
			if sp, ok := sepsisPreds[bed.ID]; ok {
				risk["sepsis_probability"] = sp.Probability
				risk["sofa_score"] = sp.SOFAScore
			} else {
				risk["sepsis_probability"] = 0.0
				risk["sofa_score"] = 0
			}
			if ip, ok := infectionPreds[bed.ID]; ok {
				risk["cre_risk"] = ip.CRERisk
				risk["mrsa_risk"] = ip.MRSARisk
			} else {
				risk["cre_risk"] = 0.0
				risk["mrsa_risk"] = 0.0
			}

			bedUID := uint32(bed.ID)
			if vapRecords != nil {
				if vr, ok := vapRecords[bedUID]; ok {
					risk["vap_risk"] = vr.RiskProbability
				} else {
					risk["vap_risk"] = 0.0
				}
			} else {
				risk["vap_risk"] = 0.0
			}

			if resistancePreds != nil {
				if rp, ok := resistancePreds[bedUID]; ok {
					risk["resistance_risk"] = rp.SpreadProb
				} else {
					risk["resistance_risk"] = 0.0
				}
			} else {
				risk["resistance_risk"] = 0.0
			}

			item["risk"] = risk

			if optimizerSuggestions != nil {
				item["optimizer_suggestion"] = optimizerSuggestions
			} else {
				item["optimizer_suggestion"] = []map[string]interface{}{}
			}

			item["transport_stats"] = transportStats

			result = append(result, item)
		}
	}

	return result
}
