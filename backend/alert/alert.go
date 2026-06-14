package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
	"field-hospital-icu/config"
	"field-hospital-icu/database"
	"field-hospital-icu/models"
	"github.com/gorilla/websocket"
)

var (
	clients      map[*websocket.Conn]bool
	clientsMux   sync.RWMutex
	lastAlertBed map[int]time.Time
	alertMux     sync.Mutex
)

type WSClient struct {
	Conn  *websocket.Conn
	Send  chan []byte
}

func InitAlertSystem() {
	clients = make(map[*websocket.Conn]bool)
	lastAlertBed = make(map[int]time.Time)
}

func RegisterClient(conn *websocket.Conn) {
	clientsMux.Lock()
	defer clientsMux.Unlock()
	clients[conn] = true
	log.Printf("WebSocket客户端已连接，当前连接数: %d", len(clients))
}

func UnregisterClient(conn *websocket.Conn) {
	clientsMux.Lock()
	defer clientsMux.Unlock()
	delete(clients, conn)
	log.Printf("WebSocket客户端已断开，当前连接数: %d", len(clients))
}

func BroadcastMessage(msgType string, data interface{}) {
	msg := models.WSMessage{
		Type: msgType,
		Data: data,
		Time: time.Now(),
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("序列化广播消息失败: %v", err)
		return
	}

	clientsMux.RLock()
	defer clientsMux.RUnlock()

	for client := range clients {
		err := client.WriteMessage(websocket.TextMessage, payload)
		if err != nil {
			log.Printf("WebSocket写入失败: %v", err)
			client.Close()
			delete(clients, client)
		}
	}
}

func CheckAndTriggerAlert(pred models.Prediction) {
	sofaThreshold := config.AppConfig.Alert.SofaThreshold
	infectionThreshold := config.AppConfig.Alert.InfectionThreshold

	if pred.SOFAScore >= sofaThreshold {
		triggerAlert(pred, "sepsis", pred.SOFAScore, sofaThreshold,
			fmt.Sprintf("脓毒症预警: SOFA评分 %.1f ≥ 阈值 %.1f", pred.SOFAScore, sofaThreshold))
	}

	if pred.CRERisk > infectionThreshold {
		triggerAlert(pred, "cre_infection", pred.CRERisk, infectionThreshold,
			fmt.Sprintf("CRE感染高风险: 风险值 %.3f > 阈值 %.1f", pred.CRERisk, infectionThreshold))
	}

	if pred.MRSARisk > infectionThreshold {
		triggerAlert(pred, "mrsa_infection", pred.MRSARisk, infectionThreshold,
			fmt.Sprintf("MRSA感染高风险: 风险值 %.3f > 阈值 %.1f", pred.MRSARisk, infectionThreshold))
	}
}

func triggerAlert(pred models.Prediction, alertType string, triggerVal float64, threshold float64, message string) {
	alertMux.Lock()
	key := pred.BedID*10 + hashAlertType(alertType)
	if last, ok := lastAlertBed[key]; ok && time.Since(last) < 5*time.Minute {
		alertMux.Unlock()
		return
	}
	lastAlertBed[key] = time.Now()
	alertMux.Unlock()

	severity := "warning"
	if triggerVal >= threshold*1.5 {
		severity = "critical"
	} else if triggerVal >= threshold*1.2 {
		severity = "high"
	}

	var id int
	err := database.DB.QueryRow(context.Background(),
		`INSERT INTO alerts (bed_id, alert_type, severity, message, trigger_value, threshold)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		pred.BedID, alertType, severity, message, triggerVal, threshold).Scan(&id)
	if err != nil {
		log.Printf("保存告警失败: %v", err)
		return
	}

	alert := models.Alert{
		ID:           id,
		BedID:        pred.BedID,
		AlertType:    alertType,
		Severity:     severity,
		Message:      message,
		TriggerValue: triggerVal,
		Threshold:    threshold,
		CreatedAt:    time.Now(),
	}

	BroadcastMessage("alert", alert)
	sendSMSNotification(alert)

	log.Printf("[ALERT] 床位 %d: %s - %s", pred.BedID, severity, message)
}

func hashAlertType(t string) int {
	switch t {
	case "sepsis":
		return 1
	case "cre_infection":
		return 2
	case "mrsa_infection":
		return 3
	default:
		return 0
	}
}

func sendSMSNotification(alert models.Alert) {
	smsContent := fmt.Sprintf("[战地医院ICU告警] 床位ICU-%03d %s: %s",
		alert.BedID, alert.Severity, alert.Message)
	log.Printf("[SMS模拟] %s -> %s", smsContent, config.AppConfig.Alert.SMSGateway)
}

func BroadcastRiskUpdate() {
	beds := make([]models.Bed, 0)
	rows, err := database.DB.Query(context.Background(),
		`SELECT id, bed_code, patient_name, patient_age, patient_gender, status, admission_time, location_x, location_y, created_at
		 FROM beds ORDER BY id`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var b models.Bed
		if err := rows.Scan(&b.ID, &b.BedCode, &b.PatientName, &b.PatientAge,
			&b.PatientGender, &b.Status, &b.AdmissionTime, &b.LocationX, &b.LocationY, &b.CreatedAt); err == nil {
			beds = append(beds, b)
		}
	}

	vitals := make(map[int]map[string]float64)
	vitalRows, err := database.DB.Query(context.Background(),
		`SELECT DISTINCT ON (bed_id, sensor_type) bed_id, sensor_type, value
		 FROM vital_signs
		 WHERE time > NOW() - INTERVAL '1 minute'
		 ORDER BY bed_id, sensor_type, time DESC`)
	if err == nil {
		defer vitalRows.Close()
		for vitalRows.Next() {
			var bedID int
			var sensor string
			var val float64
			if err := vitalRows.Scan(&bedID, &sensor, &val); err == nil {
				if vitals[bedID] == nil {
					vitals[bedID] = make(map[string]float64)
				}
				vitals[bedID][sensor] = val
			}
		}
	}

	predictions := make(map[int]models.Prediction)
	predRows, err := database.DB.Query(context.Background(),
		`SELECT DISTINCT ON (bed_id) time, bed_id, sepsis_risk, sepsis_probability, cre_risk, mrsa_risk, sofa_score
		 FROM predictions
		 WHERE time > NOW() - INTERVAL '10 minutes'
		 ORDER BY bed_id, time DESC`)
	if err == nil {
		defer predRows.Close()
		for predRows.Next() {
			var p models.Prediction
			if err := predRows.Scan(&p.Time, &p.BedID, &p.SepsisRisk, &p.SepsisProbability,
				&p.CRERisk, &p.MRSARisk, &p.SOFAScore); err == nil {
				predictions[p.BedID] = p
			}
		}
	}

	result := make([]map[string]interface{}, 0)
	for _, b := range beds {
		item := map[string]interface{}{
			"bed":    b,
			"vitals": vitals[b.ID],
			"risk":   predictions[b.ID],
		}
		result = append(result, item)
	}

	BroadcastMessage("vitals_update", result)
}
