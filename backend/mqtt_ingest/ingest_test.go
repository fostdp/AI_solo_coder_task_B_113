package mqtt_ingest

import (
	"encoding/json"
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

func TestNewIngester(t *testing.T) {
	cfg := config.MQTTConfig{
		Broker:             "tcp://localhost:1883",
		ClientID:           "test-client",
		QoS:                1,
		CleanSession:       false,
		KeepAlive:          60,
		MessageChannelDepth: 1000,
		DecodeWorkers:      2,
		DecodeQueueSize:    100,
	}

	vitalChan := make(chan models.VitalSign, 100)
	ingester := NewIngester(cfg, vitalChan)

	if ingester == nil {
		t.Fatal("NewIngester returned nil")
	}
	if ingester.VitalChan == nil {
		t.Error("VitalChan is nil")
	}
	if ingester.decodeChan == nil {
		t.Error("decodeChan is nil")
	}
	if cap(ingester.decodeChan) != 100 {
		t.Errorf("decodeChan capacity = %d, want 100", cap(ingester.decodeChan))
	}
	if ingester.stopChan == nil {
		t.Error("stopChan is nil")
	}
}

func TestIngesterStats(t *testing.T) {
	cfg := config.MQTTConfig{
		DecodeQueueSize: 50,
	}

	vitalChan := make(chan models.VitalSign, 10)
	ingester := NewIngester(cfg, vitalChan)

	stats := ingester.Stats()
	if stats.MessageCount != 0 {
		t.Errorf("initial MessageCount = %d, want 0", stats.MessageCount)
	}
	if stats.DecodeDropped != 0 {
		t.Errorf("initial DecodeDropped = %d, want 0", stats.DecodeDropped)
	}
	if stats.DecodeQueue != 0 {
		t.Errorf("initial DecodeQueue = %d, want 0", stats.DecodeQueue)
	}
}

func TestDecodeWorker_ValidJSON(t *testing.T) {
	cfg := config.MQTTConfig{
		DecodeWorkers:   1,
		DecodeQueueSize: 10,
	}

	vitalChan := make(chan models.VitalSign, 10)
	ingester := NewIngester(cfg, vitalChan)

	ingester.wg.Add(1)
	go ingester.decodeWorker()

	msg := models.MQTTMessage{
		BedID:      5,
		SensorType: "ecg",
		Value:      72.5,
		Unit:       "bpm",
		Timestamp:  time.Now().Unix(),
	}
	payload, _ := json.Marshal(msg)

	ingester.decodeChan <- payload

	select {
	case vital := <-vitalChan:
		if vital.BedID != 5 {
			t.Errorf("BedID = %d, want 5", vital.BedID)
		}
		if vital.SensorType != "ecg" {
			t.Errorf("SensorType = %s, want ecg", vital.SensorType)
		}
		if vital.Value != 72.5 {
			t.Errorf("Value = %f, want 72.5", vital.Value)
		}
		if vital.Unit != "bpm" {
			t.Errorf("Unit = %s, want bpm", vital.Unit)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timed out waiting for decoded vital sign")
	}

	close(ingester.stopChan)
	ingester.wg.Wait()
}

func TestDecodeWorker_InvalidJSON(t *testing.T) {
	cfg := config.MQTTConfig{
		DecodeWorkers:   1,
		DecodeQueueSize: 10,
	}

	vitalChan := make(chan models.VitalSign, 10)
	ingester := NewIngester(cfg, vitalChan)

	ingester.wg.Add(1)
	go ingester.decodeWorker()

	ingester.decodeChan <- []byte("not valid json{{{")

	time.Sleep(100 * time.Millisecond)

	select {
	case <-vitalChan:
		t.Error("should not have received vital sign from invalid JSON")
	default:
	}

	close(ingester.stopChan)
	ingester.wg.Wait()
}

func TestDecodeWorker_MultipleMessages(t *testing.T) {
	cfg := config.MQTTConfig{
		DecodeWorkers:   2,
		DecodeQueueSize: 20,
	}

	vitalChan := make(chan models.VitalSign, 20)
	ingester := NewIngester(cfg, vitalChan)

	for w := 0; w < cfg.DecodeWorkers; w++ {
		ingester.wg.Add(1)
		go ingester.decodeWorker()
	}

	numMessages := 10
	for i := 0; i < numMessages; i++ {
		msg := models.MQTTMessage{
			BedID:      i + 1,
			SensorType: "spo2",
			Value:      95.0 + float64(i),
			Unit:       "%",
			Timestamp:  time.Now().Unix(),
		}
		payload, _ := json.Marshal(msg)
		ingester.decodeChan <- payload
	}

	received := 0
	timeout := time.After(1 * time.Second)
loop:
	for received < numMessages {
		select {
		case <-vitalChan:
			received++
		case <-timeout:
			break loop
		}
	}

	if received != numMessages {
		t.Errorf("received %d messages, want %d", received, numMessages)
	}

	close(ingester.stopChan)
	close(ingester.decodeChan)
	ingester.wg.Wait()
}

func TestDecodeWorker_ZeroTimestamp(t *testing.T) {
	cfg := config.MQTTConfig{
		DecodeWorkers:   1,
		DecodeQueueSize: 10,
	}

	vitalChan := make(chan models.VitalSign, 10)
	ingester := NewIngester(cfg, vitalChan)

	ingester.wg.Add(1)
	go ingester.decodeWorker()

	msg := models.MQTTMessage{
		BedID:      1,
		SensorType: "temperature",
		Value:      36.8,
		Unit:       "C",
		Timestamp:  0,
	}
	payload, _ := json.Marshal(msg)

	before := time.Now()
	ingester.decodeChan <- payload

	select {
	case vital := <-vitalChan:
		if vital.Time.Before(before) {
			t.Errorf("vital time %v is before test start %v", vital.Time, before)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timed out")
	}

	close(ingester.stopChan)
	ingester.wg.Wait()
}

func TestIngesterStatsStruct(t *testing.T) {
	stats := IngesterStats{
		MessageCount:  100,
		DecodeDropped: 5,
		DecodeQueue:   10,
	}

	if stats.MessageCount != 100 {
		t.Error("MessageCount mismatch")
	}
	if stats.DecodeDropped != 5 {
		t.Error("DecodeDropped mismatch")
	}
	if stats.DecodeQueue != 10 {
		t.Error("DecodeQueue mismatch")
	}
}

func TestIngesterStruct(t *testing.T) {
	cfg := config.MQTTConfig{
		DecodeQueueSize: 100,
	}

	vitalChan := make(chan models.VitalSign, 50)
	ingester := NewIngester(cfg, vitalChan)

	if ingester.VitalChan != (chan<- models.VitalSign)(vitalChan) {
		t.Error("VitalChan not properly set")
	}

	if ingester.messageCount != 0 {
		t.Error("messageCount should start at 0")
	}
	if ingester.decodeDropped != 0 {
		t.Error("decodeDropped should start at 0")
	}
}

func TestStopWithoutStart(t *testing.T) {
	cfg := config.MQTTConfig{
		DecodeQueueSize: 10,
		DecodeWorkers:   1,
	}

	vitalChan := make(chan models.VitalSign, 10)
	ingester := NewIngester(cfg, vitalChan)

	ingester.wg.Add(1)
	go ingester.decodeWorker()

	time.Sleep(50 * time.Millisecond)

	ingester.Stop()

	if ingester.client != nil && ingester.client.IsConnected() {
		t.Error("client should not be connected after stop")
	}
}

func TestDecodeWorker_ChannelClose(t *testing.T) {
	cfg := config.MQTTConfig{
		DecodeWorkers:   1,
		DecodeQueueSize: 10,
	}

	vitalChan := make(chan models.VitalSign, 10)
	ingester := NewIngester(cfg, vitalChan)

	done := make(chan struct{})
	ingester.wg.Add(1)
	go func() {
		defer close(done)
		ingester.decodeWorker()
	}()

	close(ingester.decodeChan)

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("decodeWorker did not exit after channel close")
	}
}

func TestDecodeQueueCapacity(t *testing.T) {
	cfg := config.MQTTConfig{
		DecodeQueueSize: 100,
		DecodeWorkers:   0,
	}

	vitalChan := make(chan models.VitalSign, 10)
	ingester := NewIngester(cfg, vitalChan)

	if cap(ingester.decodeChan) != 100 {
		t.Errorf("decodeChan capacity = %d, want 100", cap(ingester.decodeChan))
	}
}

func TestVitalChanDirection(t *testing.T) {
	cfg := config.MQTTConfig{
		DecodeQueueSize: 10,
	}

	vitalChan := make(chan models.VitalSign, 10)
	ingester := NewIngester(cfg, vitalChan)

	if ingester.VitalChan == nil {
		t.Fatal("VitalChan is nil")
	}

	testVital := models.VitalSign{
		BedID:      1,
		SensorType: "ecg",
		Value:      72.0,
		Unit:       "bpm",
		Time:       time.Now(),
	}

	cfg2 := config.MQTTConfig{DecodeQueueSize: 10}
	ingester2 := NewIngester(cfg2, vitalChan)
	ingester2.wg.Add(1)
	go ingester2.decodeWorker()

	msg := models.MQTTMessage{
		BedID:      testVital.BedID,
		SensorType: testVital.SensorType,
		Value:      testVital.Value,
		Unit:       testVital.Unit,
		Timestamp:  testVital.Time.Unix(),
	}
	payload, _ := json.Marshal(msg)
	ingester2.decodeChan <- payload

	select {
	case received := <-vitalChan:
		if received.BedID != testVital.BedID {
			t.Errorf("BedID = %d, want %d", received.BedID, testVital.BedID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timed out waiting for vital sign")
	}

	close(ingester2.stopChan)
	ingester2.wg.Wait()
}
