package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type CultureResult struct {
	BedID        int       `json:"bed_id"`
	BacteriaType string    `json:"bacteria_type"`
	Resistance   []string  `json:"resistance"`
	CollectedAt  time.Time `json:"collected_at"`
}

type TransportRequest struct {
	FromBed     int     `json:"from_bed"`
	ToBed       string  `json:"to_bed"`
	Distance    float64 `json:"distance"`
	Priority    string  `json:"priority"`
	IsEmergency bool    `json:"is_emergency"`
}

type SensorMessage struct {
	BedID      int     `json:"bed_id"`
	SensorType string  `json:"sensor_type"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	Timestamp  int64   `json:"timestamp"`
}

type SensorConfig struct {
	Type          string
	Name          string
	BaseValue     float64
	MinNormal     float64
	MaxNormal     float64
	MinRange      float64
	MaxRange      float64
	Unit          string
	AnomalyChance float64
}

type SepsisMode string

const (
	SepsisModeNormal SepsisMode = "normal"
	SepsisModeLow    SepsisMode = "low"
	SepsisModeMedium SepsisMode = "medium"
	SepsisModeHigh   SepsisMode = "high"
)

type BedSepsisState struct {
	Active        bool
	StartTime     time.Time
	Duration      time.Duration
	Progress      float64
	HeartRateMult float64
	RespMult      float64
	SpO2Mult      float64
	TempMult      float64
}

func main() {
	broker := flag.String("broker", "tcp://localhost:1883", "MQTT broker地址")
	clientID := flag.String("clientid", "sensor-simulator", "MQTT客户端ID")
	interval := flag.Int("interval", 1000, "上报间隔(毫秒)")
	numBeds := flag.Int("beds", 50, "床位数量")
	sepsisModeStr := flag.String("sepsis_mode", "normal", "脓毒症事件模式: normal|low|medium|high")
	sepsisDuration := flag.Int("sepsis_duration", 300, "脓毒症事件持续秒数")
	flag.Parse()

	sepsisMode := SepsisMode(*sepsisModeStr)
	var sepsisTriggerChance float64
	switch sepsisMode {
	case SepsisModeLow:
		sepsisTriggerChance = 0.001
	case SepsisModeMedium:
		sepsisTriggerChance = 0.005
	case SepsisModeHigh:
		sepsisTriggerChance = 0.02
	default:
		sepsisTriggerChance = 0
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	backendURL := "http://backend:8080"
	if os.Getenv("BACKEND_URL") != "" {
		backendURL = os.Getenv("BACKEND_URL")
	}
	lastCultureCheck := make(map[int]time.Time)
	lastTransportCheck := time.Now()
	cultureCount := 0
	transportCount := 0

	log.Println("=== 战地医院ICU传感器模拟器启动 ===")
	log.Printf("Broker: %s", *broker)
	log.Printf("Backend API: %s", backendURL)
	log.Printf("床位: %d, 传感器: %d", *numBeds, *numBeds*len(sensors))
	log.Printf("上报间隔: %dms", *interval)
	log.Printf("脓毒症模式: %s (触发概率: %.4f/事件持续: %ds)", sepsisMode, sepsisTriggerChance, *sepsisDuration)
	log.Println("高级仿真已启用: ventilator_advanced(peak_pressure/tidal_volume/oral_secretion) + 细菌培养 + 转运请求")

	sensors := []SensorConfig{
		{
			Type:          "ecg",
			Name:          "心电监护",
			BaseValue:     75,
			MinNormal:     60,
			MaxNormal:     100,
			MinRange:      40,
			MaxRange:      180,
			Unit:          "bpm",
			AnomalyChance: 0.05,
		},
		{
			Type:          "ventilator",
			Name:          "呼吸机频率",
			BaseValue:     18,
			MinNormal:     12,
			MaxNormal:     24,
			MinRange:      6,
			MaxRange:      40,
			Unit:          "rpm",
			AnomalyChance: 0.03,
		},
		{
			Type:          "spo2",
			Name:          "血氧饱和度",
			BaseValue:     96,
			MinNormal:     94,
			MaxNormal:     100,
			MinRange:      70,
			MaxRange:      100,
			Unit:          "%",
			AnomalyChance: 0.04,
		},
		{
			Type:          "temperature",
			Name:          "体温",
			BaseValue:     36.8,
			MinNormal:     36.5,
			MaxNormal:     37.5,
			MinRange:      35,
			MaxRange:      41,
			Unit:          "°C",
			AnomalyChance: 0.02,
		},
		{
			Type:          "peak_pressure",
			Name:          "气道峰压",
			BaseValue:     25,
			MinNormal:     15,
			MaxNormal:     30,
			MinRange:      10,
			MaxRange:      60,
			Unit:          "cmH2O",
			AnomalyChance: 0.04,
		},
		{
			Type:          "tidal_volume",
			Name:          "潮气量",
			BaseValue:     450,
			MinNormal:     300,
			MaxNormal:     600,
			MinRange:      150,
			MaxRange:      900,
			Unit:          "mL",
			AnomalyChance: 0.03,
		},
		{
			Type:          "oral_secretion",
			Name:          "口腔分泌物量",
			BaseValue:     2,
			MinNormal:     1,
			MaxNormal:     3,
			MinRange:      0,
			MaxRange:      10,
			Unit:          "score",
			AnomalyChance: 0.06,
		},
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(*broker)
	opts.SetClientID(*clientID + "-persistent")
	opts.SetCleanSession(false)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(3 * time.Second)
	opts.SetMaxReconnectInterval(1 * time.Minute)
	opts.SetKeepAlive(60 * time.Second)
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Println("MQTT已连接 (持久会话模式)")
	})
	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		log.Printf("MQTT连接断开: %v (消息不会丢失，Broker已持久化)", err)
	})

	client := mqtt.NewClient(opts)
	token := client.Connect()
	token.Wait()
	if token.Error() != nil {
		log.Fatalf("MQTT连接失败: %v", token.Error())
	}

	bedStates := make(map[int]map[string]float64)
	sepsisStates := make(map[int]*BedSepsisState)
	for i := 1; i <= *numBeds; i++ {
		bedStates[i] = make(map[string]float64)
		for _, s := range sensors {
			bedStates[i][s.Type] = s.BaseValue
		}
		sepsisStates[i] = &BedSepsisState{Active: false}
	}

	activeSepsisCount := 0
	sepsisEventCount := 0

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(time.Duration(*interval) * time.Millisecond)
	defer ticker.Stop()

	count := 0
	for {
		select {
		case <-sigChan:
			log.Println("收到停止信号，模拟器退出")
			client.Disconnect(250)
			return
		case t := <-ticker.C:
			count++
			totalSent := 0

			for bedID := 1; bedID <= *numBeds; bedID++ {
				ss := sepsisStates[bedID]

				if sepsisTriggerChance > 0 && !ss.Active && rand.Float64() < sepsisTriggerChance {
					ss.Active = true
					ss.StartTime = t
					ss.Duration = time.Duration(*sepsisDuration) * time.Second
					ss.Progress = 0
					ss.HeartRateMult = 1.0 + rand.Float64()*0.6
					ss.RespMult = 1.0 + rand.Float64()*0.8
					ss.SpO2Mult = 0.9 - rand.Float64()*0.15
					ss.TempMult = 1.0 + rand.Float64()*0.08
					sepsisEventCount++
					activeSepsisCount++
					log.Printf("[脓毒症事件#%d] 床位%d 进入脓毒症状态 (持续%ds, 心率x%.2f 呼吸x%.2f 血氧x%.2f 体温x%.2f",
						sepsisEventCount, bedID, *sepsisDuration,
						ss.HeartRateMult, ss.RespMult, ss.SpO2Mult, ss.TempMult)
				}

				intensity := 0.0
				if ss.Active {
					elapsed := t.Sub(ss.StartTime)
					ss.Progress = math.Min(1.0, float64(elapsed)/float64(ss.Duration))
					intensity = math.Sin(ss.Progress * math.Pi)

					if elapsed > ss.Duration {
						ss.Active = false
						activeSepsisCount--
						log.Printf("[脓毒症结束] 床位%d 脓毒症事件结束，共持续%.0fs", bedID, elapsed.Seconds())
					}
				}

				for _, s := range sensors {
					currentVal := bedStates[bedID][s.Type]
					var newVal float64

					isAnomaly := rand.Float64() < s.AnomalyChance

					if isAnomaly {
						anomalyDir := 1.0
						if rand.Float64() < 0.5 {
							anomalyDir = -1.0
						}
						newVal = currentVal + anomalyDir*(s.MaxRange-s.MinRange)*0.15
						if !ss.Active {
							log.Printf("[异常] 床位%d %s: %.2f -> %.2f", bedID, s.Name, currentVal, newVal)
						}
					} else {
						drift := (rand.Float64() - 0.5) * (s.MaxNormal - s.MinNormal) * 0.05
						newVal = currentVal + drift

						targetDiff := s.BaseValue - newVal
						newVal += targetDiff * 0.02

						wave := math.Sin(float64(count)/10.0+float64(bedID)) * (s.MaxNormal - s.MinNormal) * 0.02
						newVal += wave
					}

					if ss.Active && intensity > 0 {
						switch s.Type {
						case "ecg":
							sepsisEffect := (s.BaseValue * (ss.HeartRateMult - 1.0)) * intensity
							newVal += sepsisEffect
						case "ventilator":
							sepsisEffect := (s.BaseValue * (ss.RespMult - 1.0)) * intensity
							newVal += sepsisEffect
						case "spo2":
							sepsisEffect := (s.BaseValue * (ss.SpO2Mult - 1.0)) * intensity
							newVal += sepsisEffect
						case "temperature":
							sepsisEffect := (s.BaseValue * (ss.TempMult - 1.0)) * intensity
							newVal += sepsisEffect
						}
					}

					newVal = math.Max(s.MinRange, math.Min(s.MaxRange, newVal))
					bedStates[bedID][s.Type] = newVal

					msg := SensorMessage{
						BedID:      bedID,
						SensorType: s.Type,
						Value:      roundTo(newVal, 2),
						Unit:       s.Unit,
						Timestamp:  t.Unix(),
					}

					payload, _ := json.Marshal(msg)
					topic := fmt.Sprintf("icu/bed/%d/%s", bedID, s.Type)

					token := client.Publish(topic, 1, false, payload)
					token.WaitTimeout(100 * time.Millisecond)
					totalSent++
				}

				lastCheck, ok := lastCultureCheck[bedID]
				if !ok || t.Sub(lastCheck) >= 3600*time.Second {
					lastCultureCheck[bedID] = t
					if rand.Float64() < 0.01 {
						cultureCount++
						go submitCultureResult(httpClient, backendURL, bedID, t, cultureCount)
					}
				}
			}

			if t.Sub(lastTransportCheck) >= 600*time.Second {
				lastTransportCheck = t
				if rand.Float64() < 0.05 {
					transportCount++
					fromBed := rand.Intn(*numBeds) + 1
					go submitTransportRequest(httpClient, backendURL, fromBed, *numBeds, t, transportCount)
				}
			}

			if count%10 == 0 {
				log.Printf("已发送 %d 条消息 (总 %d, 活跃脓毒症: %d/%d, 培养报告: %d, 转运请求: %d)",
					totalSent, count**numBeds*len(sensors), activeSepsisCount, *numBeds, cultureCount, transportCount)
			}
		}
	}
}

func submitCultureResult(client *http.Client, baseURL string, bedID int, t time.Time, seq int) {
	bacteriaTypes := []string{"Klebsiella_pneumoniae", "E_coli", "Pseudomonas_aeruginosa", "Staphylococcus_aureus", "Acinetobacter_baumannii"}
	resistanceMap := map[string][]string{
		"Klebsiella_pneumoniae":  {"CRE", "ESBL", "Ciprofloxacin"},
		"E_coli":                 {"ESBL", "AmpC", "Cotrimoxazole"},
		"Pseudomonas_aeruginosa": {"Carbapenem", "Ceftazidime", "Amikacin"},
		"Staphylococcus_aureus":  {"MRSA", "Vancomycin", "Daptomycin"},
		"Acinetobacter_baumannii": {"Carbapenem", "Colistin", "Sulbactam"},
	}

	bType := bacteriaTypes[rand.Intn(len(bacteriaTypes))]
	allResist := resistanceMap[bType]
	n := rand.Intn(len(allResist)) + 1
	resistance := make([]string, n)
	perm := rand.Perm(len(allResist))
	for i := 0; i < n; i++ {
		resistance[i] = allResist[perm[i]]
	}

	result := CultureResult{
		BedID:        bedID,
		BacteriaType: bType,
		Resistance:   resistance,
		CollectedAt:  t,
	}
	payload, _ := json.Marshal(result)

	url := fmt.Sprintf("%s/api/beds/%d/culture", baseURL, bedID)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("[细菌培养#%d] 创建请求失败 床位%d: %v", seq, bedID, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[细菌培养#%d] 提交失败 床位%d (%s): %v", seq, bedID, bType, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[细菌培养#%d] 床位%d: %s 阳性, 耐药: %v (HTTP %d)", seq, bedID, bType, resistance, resp.StatusCode)
	} else {
		log.Printf("[细菌培养#%d] 提交异常 床位%d (HTTP %d)", seq, bedID, resp.StatusCode)
	}
}

func submitTransportRequest(client *http.Client, baseURL string, fromBed int, numBeds int, t time.Time, seq int) {
	priorities := []string{"low", "medium", "high"}
	toOptions := []string{
		fmt.Sprintf("ICU-%03d", rand.Intn(numBeds)+1),
		"手术室",
		"CT室",
		"MRI室",
		"DSA介入室",
		"普通病房",
		"肾内科透析室",
	}

	reqBody := TransportRequest{
		FromBed:     fromBed,
		ToBed:       toOptions[rand.Intn(len(toOptions))],
		Distance:    50 + rand.Float64()*500,
		Priority:    priorities[rand.Intn(len(priorities))],
		IsEmergency: rand.Float64() < 0.15,
	}
	payload, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/api/transport/evaluate", baseURL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("[转运#%d] 创建请求失败 床位%d: %v", seq, fromBed, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[转运#%d] 提交失败 床位%d->%s: %v", seq, fromBed, reqBody.ToBed, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[转运#%d] 评估完成 ICU-%03d -> %s, 距离%.0fm, 优先级:%s, 紧急:%v (HTTP %d)",
			seq, fromBed, reqBody.ToBed, reqBody.Distance, reqBody.Priority, reqBody.IsEmergency, resp.StatusCode)
	} else {
		log.Printf("[转运#%d] 评估异常 床位%d (HTTP %d)", seq, fromBed, resp.StatusCode)
	}
}

func roundTo(val float64, precision int) float64 {
	mult := math.Pow10(precision)
	return math.Round(val*mult) / mult
}
