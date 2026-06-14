package alert_ws

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"

	"github.com/gorilla/websocket"
)

const defaultVapThreshold = 0.5

type SepsisMsg struct {
	BedID       int
	Probability float64
	SOFAScore   int
	Time        time.Time
}

type InfectionMsg struct {
	BedID    int
	CRERisk  float64
	MRSARisk float64
	Time     time.Time
}

type Engine struct {
	SepsisIn      chan SepsisMsg
	InfectionIn   chan InfectionMsg
	VapIn         chan models.VapRiskRecord
	TransportIn   chan models.TransportRiskResult
	AlertOut      chan models.Alert
	cfg           config.AlertConfig
	VapThreshold  float64
	clients       map[*websocket.Conn]bool
	clientsMux    sync.RWMutex
	lastAlertTime map[string]time.Time
	alertMux      sync.Mutex
	stopChan      chan struct{}
	wg            sync.WaitGroup
}

var EngineInstance *Engine

func NewEngine(cfg config.AlertConfig, sepsisIn chan SepsisMsg, infectionIn chan InfectionMsg, alertOut chan models.Alert) *Engine {
	return &Engine{
		SepsisIn:      sepsisIn,
		InfectionIn:   infectionIn,
		AlertOut:      alertOut,
		cfg:           cfg,
		VapThreshold:  defaultVapThreshold,
		clients:       make(map[*websocket.Conn]bool),
		lastAlertTime: make(map[string]time.Time),
		stopChan:      make(chan struct{}),
	}
}

func NewEngineExtended(cfg config.Config, sepsisIn chan SepsisMsg, infectionIn chan InfectionMsg, vapIn chan models.VapRiskRecord, transportIn chan models.TransportRiskResult, alertOut chan models.Alert) *Engine {
	vapThresh := defaultVapThreshold
	if cfg.CoxVap.RiskThreshold > 0 {
		vapThresh = cfg.CoxVap.RiskThreshold
	}
	return &Engine{
		SepsisIn:      sepsisIn,
		InfectionIn:   infectionIn,
		VapIn:         vapIn,
		TransportIn:   transportIn,
		AlertOut:      alertOut,
		cfg:           cfg.Alert,
		VapThreshold:  vapThresh,
		clients:       make(map[*websocket.Conn]bool),
		lastAlertTime: make(map[string]time.Time),
		stopChan:      make(chan struct{}),
	}
}

func (e *Engine) Start() {
	e.wg.Add(2)
	go e.handleSepsis()
	go e.handleInfection()
}

func (e *Engine) StartExtended() {
	count := 2
	if e.VapIn != nil {
		count++
	}
	if e.TransportIn != nil {
		count++
	}
	e.wg.Add(count)
	go e.handleSepsis()
	go e.handleInfection()
	if e.VapIn != nil {
		go e.handleVapChannel(e.VapIn)
	}
	if e.TransportIn != nil {
		go e.handleTransportChannel(e.TransportIn)
	}
}

func (e *Engine) Stop() {
	close(e.stopChan)
	e.wg.Wait()
}

func (e *Engine) handleSepsis() {
	defer e.wg.Done()
	for {
		select {
		case <-e.stopChan:
			return
		case msg := <-e.SepsisIn:
			e.checkAndTrigger(msg.BedID, "sepsis", float64(msg.SOFAScore), e.cfg.SofaThreshold,
				fmt.Sprintf("脓毒症预警: SOFA评分 %d ≥ 阈值 %.1f", msg.SOFAScore, e.cfg.SofaThreshold))
		}
	}
}

func (e *Engine) handleInfection() {
	defer e.wg.Done()
	for {
		select {
		case <-e.stopChan:
			return
		case msg := <-e.InfectionIn:
			if msg.CRERisk > e.cfg.InfectionThreshold {
				e.checkAndTrigger(msg.BedID, "cre_infection", msg.CRERisk, e.cfg.InfectionThreshold,
					fmt.Sprintf("CRE感染高风险: 风险值 %.3f > 阈值 %.1f", msg.CRERisk, e.cfg.InfectionThreshold))
			}
			if msg.MRSARisk > e.cfg.InfectionThreshold {
				e.checkAndTrigger(msg.BedID, "mrsa_infection", msg.MRSARisk, e.cfg.InfectionThreshold,
					fmt.Sprintf("MRSA感染高风险: 风险值 %.3f > 阈值 %.1f", msg.MRSARisk, e.cfg.InfectionThreshold))
			}
		}
	}
}

func (e *Engine) handleVapChannel(vapIn <-chan models.VapRiskRecord) {
	defer e.wg.Done()
	for {
		select {
		case <-e.stopChan:
			return
		case vap := <-vapIn:
			if vap.RiskProbability > e.VapThreshold {
				e.checkAndTriggerVap(vap)
			}
		}
	}
}

func (e *Engine) handleOptimizerChannel(suggestions []map[string]interface{}) {
	e.BroadcastOptimizationSuggestion(suggestions)
}

func (e *Engine) BroadcastOptimizationSuggestion(suggestions []map[string]interface{}) {
	e.BroadcastMessage("optimizer_suggestion", suggestions)
}

func (e *Engine) handleTransportChannel(transIn <-chan models.TransportRiskResult) {
	defer e.wg.Done()
	threshold := 60
	for {
		select {
		case <-e.stopChan:
			return
		case trans := <-transIn:
			if trans.RiskScore >= threshold {
				e.checkAndTrigger(int(trans.RequestID), "transport_high_risk", float64(trans.RiskScore), float64(threshold),
					fmt.Sprintf("转运高风险: 请求ID %d 风险评分 %d ≥ 阈值 %d，建议: %v", trans.RequestID, trans.RiskScore, threshold, trans.Recommendations))
			}
		}
	}
}

func (e *Engine) checkAndTrigger(bedID int, alertType string, triggerVal, threshold float64, message string) {
	key := e.hashAlertTypeKey(bedID, alertType)

	e.alertMux.Lock()
	if last, ok := e.lastAlertTime[key]; ok && time.Since(last) < time.Duration(e.cfg.DeduplicationWindow)*time.Minute {
		e.alertMux.Unlock()
		return
	}
	e.lastAlertTime[key] = time.Now()
	e.alertMux.Unlock()

	severity := e.severityOf(triggerVal, threshold)

	alert := models.Alert{
		BedID:        bedID,
		AlertType:    alertType,
		Severity:     severity,
		Message:      message,
		TriggerValue: triggerVal,
		Threshold:    threshold,
		CreatedAt:    time.Now(),
	}

	if e.AlertOut != nil {
		e.AlertOut <- alert
	}

	switch alertType {
	case "vap_risk":
		e.BroadcastMessage("vap_risk_alert", alert)
	case "cre_infection", "mrsa_infection":
		e.BroadcastMessage("resistance_alert", alert)
	case "transport_high_risk":
		e.BroadcastMessage("transport_risk_alert", alert)
	default:
		e.BroadcastMessage("alert", alert)
	}

	log.Printf("[ALERT] 床位 %d: %s - %s", bedID, severity, message)
}

func (e *Engine) checkAndTriggerVap(vap models.VapRiskRecord) {
	alertType := "vap_risk"
	bedID := int(vap.BedID)
	triggerVal := vap.RiskProbability
	threshold := e.VapThreshold

	message := fmt.Sprintf("床位%d VAP风险%.1f%%，预计%.1fh后发作",
		bedID, vap.RiskProbability*100, vap.PredictedOnsetHours)

	key := e.hashAlertTypeKey(bedID, alertType)

	e.alertMux.Lock()
	if last, ok := e.lastAlertTime[key]; ok && time.Since(last) < time.Duration(e.cfg.DeduplicationWindow)*time.Minute {
		e.alertMux.Unlock()
		return
	}
	e.lastAlertTime[key] = time.Now()
	e.alertMux.Unlock()

	severity := e.severityOfVap(vap.HazardsRatio)

	alert := models.Alert{
		BedID:        bedID,
		AlertType:    alertType,
		Severity:     severity,
		Message:      message,
		TriggerValue: triggerVal,
		Threshold:    threshold,
		CreatedAt:    time.Now(),
	}

	if e.AlertOut != nil {
		e.AlertOut <- alert
	}

	e.BroadcastMessage("vap_risk_alert", alert)

	log.Printf("[ALERT] 床位 %d: %s - %s", bedID, severity, message)
}

func (e *Engine) severityOf(triggerVal, threshold float64) string {
	if triggerVal >= threshold*1.5 {
		return "critical"
	} else if triggerVal >= threshold*1.2 {
		return "high"
	}
	return "warning"
}

func (e *Engine) severityOfVap(hr float64) string {
	if hr > 2 {
		return "critical"
	} else if hr > 1.5 {
		return "high"
	}
	return "warning"
}

func (e *Engine) hashAlertTypeKey(bedID int, alertType string) string {
	return fmt.Sprintf("%d:%s", bedID, alertType)
}

func (e *Engine) RegisterClient(conn *websocket.Conn) {
	e.clientsMux.Lock()
	defer e.clientsMux.Unlock()
	e.clients[conn] = true
	log.Printf("WebSocket客户端已连接，当前连接数: %d", len(e.clients))
}

func (e *Engine) UnregisterClient(conn *websocket.Conn) {
	e.clientsMux.Lock()
	defer e.clientsMux.Unlock()
	delete(e.clients, conn)
	log.Printf("WebSocket客户端已断开，当前连接数: %d", len(e.clients))
}

func (e *Engine) BroadcastMessage(msgType string, data interface{}) {
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

	var toDelete []*websocket.Conn

	e.clientsMux.RLock()
	for client := range e.clients {
		err := client.WriteMessage(websocket.TextMessage, payload)
		if err != nil {
			log.Printf("WebSocket写入失败: %v", err)
			toDelete = append(toDelete, client)
		}
	}
	e.clientsMux.RUnlock()

	if len(toDelete) > 0 {
		e.clientsMux.Lock()
		for _, client := range toDelete {
			client.Close()
			delete(e.clients, client)
		}
		e.clientsMux.Unlock()
	}
}

func (e *Engine) BroadcastRiskUpdate(riskData interface{}) {
	e.BroadcastMessage("vitals_update", riskData)
}
