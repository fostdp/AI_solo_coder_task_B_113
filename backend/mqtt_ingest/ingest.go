package mqtt_ingest

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"

	mqttlib "github.com/eclipse/paho.mqtt.golang"
)

type Ingester struct {
	client        mqttlib.Client
	decodeChan    chan []byte
	VitalChan     chan<- models.VitalSign
	wg            sync.WaitGroup
	stopChan      chan struct{}
	messageCount  uint64
	decodeDropped uint64
}

type IngesterStats struct {
	MessageCount  uint64
	DecodeDropped uint64
	DecodeQueue   int
}

func NewIngester(cfg config.MQTTConfig, vitalChan chan<- models.VitalSign) *Ingester {
	return &Ingester{
		decodeChan: make(chan []byte, cfg.DecodeQueueSize),
		VitalChan:  vitalChan,
		stopChan:   make(chan struct{}),
	}
}

func (i *Ingester) Start(cfg config.MQTTConfig) error {
	opts := mqttlib.NewClientOptions()
	opts.AddBroker(cfg.Broker)
	opts.SetClientID(cfg.ClientID + "-ingest")

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
		opts.SetPassword(cfg.Password)
	}

	opts.SetCleanSession(false)
	opts.SetKeepAlive(time.Duration(cfg.KeepAlive) * time.Second)
	opts.SetPingTimeout(10 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(3 * time.Second)
	opts.SetMaxReconnectInterval(1 * time.Minute)
	opts.SetOrderMatters(false)
	opts.SetMessageChannelDepth(uint(cfg.MessageChannelDepth))
	opts.SetResumeSubs(true)

	opts.SetDefaultPublishHandler(i.rawPayloadHandler)

	opts.SetOnConnectHandler(func(c mqttlib.Client) {
		log.Println("[Ingester] MQTT已连接")
		i.subscribeAll(cfg)
	})

	opts.SetConnectionLostHandler(func(c mqttlib.Client, err error) {
		log.Printf("[Ingester] MQTT连接断开: %v", err)
	})

	opts.SetReconnectingHandler(func(c mqttlib.Client, opts *mqttlib.ClientOptions) {
		log.Println("[Ingester] MQTT正在重连...")
	})

	i.client = mqttlib.NewClient(opts)

	for w := 0; w < cfg.DecodeWorkers; w++ {
		i.wg.Add(1)
		go i.decodeWorker()
	}

	token := i.client.Connect()
	if token.Wait() && token.Error() != nil {
		log.Printf("[Ingester] MQTT连接失败: %v", token.Error())
		return token.Error()
	}

	log.Printf("[Ingester] 已启动 %d 个解码worker", cfg.DecodeWorkers)
	return nil
}

func (i *Ingester) Stop() {
	close(i.stopChan)

	if i.client != nil && i.client.IsConnected() {
		i.client.Disconnect(250)
	}

	i.wg.Wait()
	close(i.decodeChan)
	log.Println("[Ingester] 已停止")
}

func (i *Ingester) Stats() IngesterStats {
	return IngesterStats{
		MessageCount:  atomic.LoadUint64(&i.messageCount),
		DecodeDropped: atomic.LoadUint64(&i.decodeDropped),
		DecodeQueue:   len(i.decodeChan),
	}
}

func (i *Ingester) rawPayloadHandler(c mqttlib.Client, msg mqttlib.Message) {
	atomic.AddUint64(&i.messageCount, 1)

	payload := msg.Payload()
	select {
	case i.decodeChan <- payload:
	default:
		atomic.AddUint64(&i.decodeDropped, 1)
	}
}

func (i *Ingester) decodeWorker() {
	defer i.wg.Done()

	for {
		select {
		case <-i.stopChan:
			return
		case payload, ok := <-i.decodeChan:
			if !ok {
				return
			}

			var m models.MQTTMessage
			if err := json.Unmarshal(payload, &m); err != nil {
				log.Printf("[Ingester] JSON解析失败: %v", err)
				continue
			}

			ts := time.Now()
			if m.Timestamp > 0 {
				ts = time.Unix(m.Timestamp, 0)
			}

			vital := models.VitalSign{
				Time:       ts,
				BedID:      m.BedID,
				SensorType: m.SensorType,
				Value:      m.Value,
				Unit:       m.Unit,
			}

			select {
			case <-i.stopChan:
				return
			case i.VitalChan <- vital:
			}
		}
	}
}

func (i *Ingester) subscribeAll(cfg config.MQTTConfig) {
	sensorTypes := []string{"ecg", "ventilator", "spo2", "temperature"}
	totalTopics := 0

	for bedID := 1; bedID <= 50; bedID++ {
		for _, st := range sensorTypes {
			topic := fmt.Sprintf("icu/bed/%d/%s", bedID, st)
			token := i.client.Subscribe(topic, cfg.QoS, nil)
			if token.Wait() && token.Error() != nil {
				log.Printf("[Ingester] 订阅 %s 失败: %v", topic, token.Error())
			} else {
				totalTopics++
			}
		}
	}

	log.Printf("[Ingester] 已订阅 %d 个传感器主题", totalTopics)
}
