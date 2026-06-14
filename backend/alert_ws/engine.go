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
	AlertOut      chan models.Alert
	cfg           config.AlertConfig
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

	e.BroadcastMessage("alert", alert)

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
