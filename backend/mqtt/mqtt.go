package mqtt

import (
	"encoding/json"
	"fmt"
	"log"
	"time"
	"field-hospital-icu/config"
	"field-hospital-icu/database"
	"field-hospital-icu/models"
	mqttlib "github.com/eclipse/paho.mqtt.golang"
)

var client mqttlib.Client
var VitalChannel chan models.VitalSign

func InitMQTT() {
	VitalChannel = make(chan models.VitalSign, 10000)

	InitPersistentClient()
	PersistentClient.Connect()

	opts := mqttlib.NewClientOptions()
	opts.AddBroker(config.AppConfig.MQTT.Broker)
	opts.SetClientID(config.AppConfig.MQTT.ClientID)
	if config.AppConfig.MQTT.Username != "" {
		opts.SetUsername(config.AppConfig.MQTT.Username)
		opts.SetPassword(config.AppConfig.MQTT.Password)
	}
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetOnConnectHandler(onConnect)
	opts.SetConnectionLostHandler(onConnectionLost)
	opts.SetDefaultPublishHandler(messageHandler)

	client = mqttlib.NewClient(opts)

	token := client.Connect()
	token.Wait()
	if token.Error() != nil {
		log.Printf("MQTT连接警告: %v", token.Error())
	}

	go seedInitialData()
}

func onConnect(c mqttlib.Client) {
	log.Println("MQTT已连接")

	sensorTypes := []string{"ecg", "ventilator", "spo2", "temperature"}
	for bedID := 1; bedID <= 50; bedID++ {
		for _, st := range sensorTypes {
			topic := fmt.Sprintf("icu/bed/%d/%s", bedID, st)
			token := c.Subscribe(topic, config.AppConfig.MQTT.QoS, nil)
			token.Wait()
			if token.Error() != nil {
				log.Printf("订阅 %s 失败: %v", topic, token.Error())
			}
		}
	}
	log.Println("已订阅200个传感器主题")
}

func onConnectionLost(c mqttlib.Client, err error) {
	log.Printf("MQTT连接断开: %v", err)
}

func messageHandler(c mqttlib.Client, msg mqttlib.Message) {
	var m models.MQTTMessage
	if err := json.Unmarshal(msg.Payload(), &m); err != nil {
		log.Printf("解析MQTT消息失败: %v", err)
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
		database.VitalWriter.Write(vital)
	} else {
		select {
		case VitalChannel <- vital:
		default:
		}
	}
}

func seedInitialData() {
	sensorConfigs := []struct {
		Type  string
		Base  float64
		Range float64
		Unit  string
	}{
		{"ecg", 75, 15, "bpm"},
		{"ventilator", 18, 4, "rpm"},
		{"spo2", 96, 3, "%"},
		{"temperature", 36.8, 0.8, "°C"},
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	count := 0
	for range ticker.C {
		now := time.Now()
		for bedID := 1; bedID <= 50; bedID++ {
			for _, sc := range sensorConfigs {
				anomalyMult := 1.0
				if count%60 == 0 && bedID == count%50+1 {
					anomalyMult = 1.3
				}
				noise := (float64(count%10) - 5) / 10
				value := sc.Base + noise*sc.Range
				if sc.Type == "temperature" {
					value = sc.Base + noise*sc.Range*0.1
				}
				value *= anomalyMult

				vital := models.VitalSign{
					Time:       now,
					BedID:      bedID,
					SensorType: sc.Type,
					Value:      value,
					Unit:       sc.Unit,
				}
				VitalChannel <- vital
			}
		}
		count++
	}
}

func CloseMQTT() {
	if client != nil && client.IsConnected() {
		client.Disconnect(250)
	}
}
