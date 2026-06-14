package mqtt

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
	"field-hospital-icu/config"
	"field-hospital-icu/database"
	"field-hospital-icu/models"
	mqttlib "github.com/eclipse/paho.mqtt.golang"
)

const (
	PersistentSession = true
	QoSPersistent     = 1
	KeepAliveSeconds  = 60
)

type PersistentMQTTClient struct {
	client       mqttlib.Client
	subscribed   bool
	messageCount uint64
	reconnectCount uint64
	lastConnectTime time.Time
	offlineBuffer []models.VitalSign
	bufferMux     sync.Mutex
	usingBatch   bool
}

var PersistentClient *PersistentMQTTClient

func NewPersistentMQTTClient() *PersistentMQTTClient {
	return &PersistentMQTTClient{
		offlineBuffer: make([]models.VitalSign, 0, 5000),
		usingBatch:    database.VitalWriter != nil,
	}
}

func InitPersistentClient() {
	PersistentClient = NewPersistentMQTTClient()

	opts := buildPersistentOptions()
	PersistentClient.client = mqttlib.NewClient(opts)

	log.Println("MQTT持久会话客户端初始化完成 (CleanSession=false)")
}

func buildPersistentOptions() *mqttlib.ClientOptions {
	opts := mqttlib.NewClientOptions()
	opts.AddBroker(config.AppConfig.MQTT.Broker)
	opts.SetClientID(config.AppConfig.MQTT.ClientID + "-persistent")

	if config.AppConfig.MQTT.Username != "" {
		opts.SetUsername(config.AppConfig.MQTT.Username)
		opts.SetPassword(config.AppConfig.MQTT.Password)
	}

	opts.SetCleanSession(false)

	opts.SetKeepAlive(KeepAliveSeconds * time.Second)
	opts.SetPingTimeout(10 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(3 * time.Second)
	opts.SetMaxReconnectInterval(1 * time.Minute)

	opts.SetConnectionLostHandler(onPersistentConnectionLost)
	opts.SetOnConnectHandler(onPersistentConnect)
	opts.SetReconnectingHandler(onReconnecting)

	opts.SetDefaultPublishHandler(persistentMessageHandler)
	opts.SetOrderMatters(false)

	opts.SetWriteTimeout(10 * time.Second)
	opts.SetMessageChannelDepth(10000)

	opts.SetResumeSubs(true)

	return opts
}

func (pmc *PersistentMQTTClient) Connect() error {
	token := pmc.client.Connect()
	if token.Wait() && token.Error() != nil {
		log.Printf("MQTT连接失败: %v", token.Error())
		return token.Error()
	}

	pmc.lastConnectTime = time.Now()
	log.Println("MQTT持久会话连接成功")
	return nil
}

func (pmc *PersistentMQTTClient) SubscribeAll() error {
	sensorTypes := []string{"ecg", "ventilator", "spo2", "temperature"}
	totalTopics := 0

	for bedID := 1; bedID <= 50; bedID++ {
		for _, st := range sensorTypes {
			topic := fmt.Sprintf("icu/bed/%d/%s", bedID, st)
			token := pmc.client.Subscribe(topic, byte(QoSPersistent), nil)
			if token.Wait() && token.Error() != nil {
				log.Printf("订阅 %s 失败: %v", topic, token.Error())
			} else {
				totalTopics++
			}
		}
	}

	log.Printf("已订阅 %d 个传感器主题 (持久化QoS=%d)", totalTopics, QoSPersistent)
	pmc.subscribed = true
	return nil
}

func (pmc *PersistentMQTTClient) Publish(topic string, qos byte, payload interface{}) error {
	token := pmc.client.Publish(topic, qos, true, payload)
	return token.Error()
}

func (pmc *PersistentMQTTClient) IsConnected() bool {
	return pmc.client != nil && pmc.client.IsConnected()
}

func (pmc *PersistentMQTTClient) Disconnect(quiesce uint) {
	if pmc.client != nil {
		pmc.client.Disconnect(quiesce)
	}
}

func (pmc *PersistentMQTTClient) Stats() MQTTStats {
	return MQTTStats{
		Connected:      pmc.IsConnected(),
		MessageCount:   atomic.LoadUint64(&pmc.messageCount),
		ReconnectCount: atomic.LoadUint64(&pmc.reconnectCount),
		LastConnect:    pmc.lastConnectTime,
		BufferSize:     len(pmc.offlineBuffer),
	}
}

type MQTTStats struct {
	Connected      bool
	MessageCount   uint64
	ReconnectCount uint64
	LastConnect    time.Time
	BufferSize     int
}

func onPersistentConnect(c mqttlib.Client) {
	log.Println("[MQTT] 持久会话已连接")

	if PersistentClient != nil {
		PersistentClient.lastConnectTime = time.Now()
		atomic.AddUint64(&PersistentClient.reconnectCount, 1)

		if !PersistentClient.subscribed {
			PersistentClient.SubscribeAll()
		} else {
			log.Println("[MQTT] 使用已持久化的订阅，无需重新订阅")
		}

		PersistentClient.flushOfflineBuffer()
	}
}

func onPersistentConnectionLost(c mqttlib.Client, err error) {
	log.Printf("[MQTT] 连接断开: %v (消息不会丢失，Broker已持久化)", err)
}

func onReconnecting(c mqttlib.Client, opts *mqttlib.ClientOptions) {
	log.Println("[MQTT] 正在重连... 保持会话状态")
}

func persistentMessageHandler(c mqttlib.Client, msg mqttlib.Message) {
	if PersistentClient != nil {
		atomic.AddUint64(&PersistentClient.messageCount, 1)
	}

	var m models.MQTTMessage
	if err := json.Unmarshal(msg.Payload(), &m); err != nil {
		log.Printf("[MQTT] 解析消息失败: %v", err)
		return
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

	if database.VitalWriter != nil {
		if !database.VitalWriter.Write(vital) {
			PersistentClient.bufferOffline(vital)
		}
	} else {
		select {
		case VitalChannel <- vital:
		default:
			PersistentClient.bufferOffline(vital)
		}
	}
}

func (pmc *PersistentMQTTClient) bufferOffline(vital models.VitalSign) {
	pmc.bufferMux.Lock()
	defer pmc.bufferMux.Unlock()

	if len(pmc.offlineBuffer) < 5000 {
		pmc.offlineBuffer = append(pmc.offlineBuffer, vital)
	}
}

func (pmc *PersistentMQTTClient) flushOfflineBuffer() {
	pmc.bufferMux.Lock()
	buffer := pmc.offlineBuffer
	pmc.offlineBuffer = make([]models.VitalSign, 0, 5000)
	pmc.bufferMux.Unlock()

	if len(buffer) == 0 {
		return
	}

	log.Printf("[MQTT] 写入 %d 条缓冲的离线数据", len(buffer))

	if database.VitalWriter != nil {
		for _, v := range buffer {
			database.VitalWriter.Write(v)
		}
	} else {
		for _, v := range buffer {
			select {
			case VitalChannel <- v:
			default:
			}
		}
	}
}

func GetMQTTStats() MQTTStats {
	if PersistentClient != nil {
		return PersistentClient.Stats()
	}
	return MQTTStats{}
}
