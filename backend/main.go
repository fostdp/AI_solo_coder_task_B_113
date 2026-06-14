package main

import (
	"log"
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

	alertEngine := alert_ws.NewEngine(config.AppConfig.Alert, sepsisAlertChan, infectionAlertChan, alertOutChan)
	alert_ws.EngineInstance = alertEngine
	alertEngine.Start()
	defer alertEngine.Stop()
	log.Println("告警引擎 + WebSocket模块启动完成")

	go startFanOut(vitalsIngestChan, sepsisInChan, infectionInChan)
	log.Println("体征数据扇出通道启动")

	go startSepsisAdaptor(sepsisOutChan, sepsisAlertChan)
	go startInfectionAdaptor(infectionOutChan, infectionAlertChan)
	log.Println("预测结果适配器启动")

	go startAlertPersistWorker(alertOutChan)
	log.Println("告警持久化Worker启动")

	go startRiskBroadcastWorker()
	log.Println("风险状态定时广播启动")

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
	}

	r.GET("/ws", handlers.WebSocketHandler)

	log.Printf("HTTP服务启动，监听端口: %s", config.AppConfig.Server.Port)
	if err := r.Run(":" + config.AppConfig.Server.Port); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}

func startFanOut(in <-chan models.VitalSign, sepsisOut, infectionOut chan<- models.VitalSign) {
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
	}
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
			item["risk"] = risk

			result = append(result, item)
		}
	}

	return result
}
