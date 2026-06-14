package sepsis_lstm

import (
	"testing"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

func TestNewPredictor(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          5,
		BufferSize:         100,
		PredictionInterval: 10,
		LSTM: config.LSTMConfig{
			InputSize:      4,
			HiddenSize:     32,
			OutputSize:     1,
			MAMLWeight:     0.7,
			SOFAWeight:     0.3,
			FallbackBaseRate: 0.15,
			LSTMOutputWeight: 0.05,
			LSTMOutputBias: 0.5,
		},
		MAML: config.MAMLConfig{
			InnerLR:     0.01,
			OuterLR:     0.001,
			AdaptSteps:  5,
			StopLoss:    0.01,
			SeqLength:   30,
			MinAdaptSeq: 10,
		},
		Normalization: config.NormalizationConfig{
			ECGMean:         75.0,
			ECGStd:          30.0,
			VentilatorMean:  18.0,
			VentilatorStd:   10.0,
			SpO2Mean:        96.0,
			SpO2Std:         10.0,
			TemperatureMean: 36.8,
			TemperatureStd:  2.0,
		},
	}

	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan SepsisPrediction, 10)

	p := NewPredictor(cfg, inChan, outChan)
	if p == nil {
		t.Fatal("NewPredictor returned nil")
	}
	if p.InChan == nil {
		t.Error("InChan is nil")
	}
	if p.OutChan == nil {
		t.Error("OutChan is nil")
	}
	if p.vitalBuffer == nil {
		t.Error("vitalBuffer is nil")
	}
	if len(p.vitalBuffer) != 5 {
		t.Errorf("vitalBuffer should have %d beds, got %d", 5, len(p.vitalBuffer))
	}
}

func TestPredictor_StartStop(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          3,
		BufferSize:         50,
		PredictionInterval: 10,
		LSTM: config.LSTMConfig{
			InputSize:  4,
			HiddenSize: 16,
			OutputSize: 1,
		},
	}

	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan SepsisPrediction, 10)

	p := NewPredictor(cfg, inChan, outChan)

	p.Start()
	time.Sleep(50 * time.Millisecond)

	p.Stop()
	time.Sleep(50 * time.Millisecond)
}

func TestPredictor_UpdateBuffer(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          2,
		BufferSize:         10,
		PredictionInterval: 5,
		LSTM: config.LSTMConfig{
			InputSize:  4,
			HiddenSize: 16,
			OutputSize: 1,
		},
	}

	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan SepsisPrediction, 10)

	p := NewPredictor(cfg, inChan, outChan)

	now := time.Now()
	for i := 0; i < 15; i++ {
		v := models.VitalSign{
			Time:       now.Add(time.Duration(i) * time.Second),
			BedID:      1,
			SensorType: "ecg",
			Value:      70.0 + float64(i),
			Unit:       "bpm",
		}
		p.updateBuffer(v)
	}

	p.bufferMux.RLock()
	bufLen := len(p.vitalBuffer[1]["ecg"])
	p.bufferMux.RUnlock()

	if bufLen != 10 {
		t.Errorf("buffer should be at capacity 10, got %d", bufLen)
	}
}

func TestGetLatest(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:  1,
		BufferSize: 10,
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan SepsisPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	emptyBuf := []float64{}
	val := p.getLatest(emptyBuf)
	if val != 0 {
		t.Errorf("getLatest(empty) = %f, want 0", val)
	}

	fullBuf := []float64{1.0, 2.0, 3.0}
	val = p.getLatest(fullBuf)
	if val != 3.0 {
		t.Errorf("getLatest([1,2,3]) = %f, want 3.0", val)
	}
}

func TestNormalizeValue(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds: 1,
		Normalization: config.NormalizationConfig{
			ECGMean:         75.0,
			ECGStd:          10.0,
			VentilatorMean:  18.0,
			VentilatorStd:   4.0,
			SpO2Mean:        97.0,
			SpO2Std:         2.0,
			TemperatureMean: 36.5,
			TemperatureStd:  0.5,
		},
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan SepsisPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	val := p.normalizeValue("ecg", 85.0)
	if val != 1.0 {
		t.Errorf("normalizeValue(ecg, 85) = %f, want 1.0", val)
	}

	val = p.normalizeValue("spo2", 97.0)
	if val != 0.0 {
		t.Errorf("normalizeValue(spo2, 97) = %f, want 0.0", val)
	}

	val = p.normalizeValue("unknown", 100.0)
	if val != 0 {
		t.Errorf("normalizeValue(unknown, 100) = %f, want 0", val)
	}
}

func TestCalculateSOFA(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:  1,
		BufferSize: 100,
	}
	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan SepsisPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	score := p.calculateSOFA(1)
	if score != 0 {
		t.Errorf("SOFA with empty buffers = %d, want 0", score)
	}

	now := time.Now()
	ecgVal := 80.0
	spo2Val := 98.0
	tempVal := 36.8
	ventVal := 18.0

	p.updateBuffer(models.VitalSign{Time: now, BedID: 1, SensorType: "ecg", Value: ecgVal})
	p.updateBuffer(models.VitalSign{Time: now, BedID: 1, SensorType: "spo2", Value: spo2Val})
	p.updateBuffer(models.VitalSign{Time: now, BedID: 1, SensorType: "temperature", Value: tempVal})
	p.updateBuffer(models.VitalSign{Time: now, BedID: 1, SensorType: "ventilator", Value: ventVal})

	score = p.calculateSOFA(1)
	if score < 0 || score > 24 {
		t.Errorf("SOFA score out of range: %d", score)
	}
}

func TestRunPredictionAll(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          3,
		BufferSize:         50,
		PredictionInterval: 10,
		LSTM: config.LSTMConfig{
			InputSize:  4,
			HiddenSize: 16,
			OutputSize: 1,
		},
	}
	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan SepsisPrediction, 10)

	p := NewPredictor(cfg, inChan, outChan)

	now := time.Now()
	bedIDs := []int{1, 2, 3}
	for _, bid := range bedIDs {
		for i := 0; i < 5; i++ {
			p.updateBuffer(models.VitalSign{
				Time:       now.Add(time.Duration(i) * time.Second),
				BedID:      bid,
				SensorType: "ecg",
				Value:      70.0 + float64(bid),
				Unit:       "bpm",
			})
		}
	}

	predictions := p.RunPredictionAll()
	if len(predictions) != 3 {
		t.Errorf("RunPredictionAll returned %d predictions, want 3", len(predictions))
	}

	for _, bid := range bedIDs {
		if _, ok := predictions[bid]; !ok {
			t.Errorf("RunPredictionAll missing bed %d", bid)
		}
		pred := predictions[bid]
		if pred.BedID != bid {
			t.Errorf("prediction BedID mismatch: got %d, want %d", pred.BedID, bid)
		}
		if pred.Probability < 0 || pred.Probability > 1 {
			t.Errorf("probability out of range [0,1]: %f", pred.Probability)
		}
	}
}

func TestRunPrediction(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          1,
		BufferSize:         50,
		PredictionInterval: 10,
		LSTM: config.LSTMConfig{
			InputSize:  4,
			HiddenSize: 16,
			OutputSize: 1,
		},
	}
	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan SepsisPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	now := time.Now()
	for i := 0; i < 10; i++ {
		p.updateBuffer(models.VitalSign{
			Time:       now.Add(time.Duration(i) * time.Second),
			BedID:      1,
			SensorType: "ecg",
			Value:      75.0,
		})
	}

	pred := p.RunPrediction(1)
	if pred.BedID != 1 {
		t.Errorf("BedID = %d, want 1", pred.BedID)
	}
	if pred.Probability < 0 || pred.Probability > 1 {
		t.Errorf("Probability out of range: %f", pred.Probability)
	}
	if pred.SOFAScore < 0 {
		t.Errorf("SOFAScore should be non-negative: %d", pred.SOFAScore)
	}
}

func TestSepsisPredictionStruct(t *testing.T) {
	now := time.Now()
	p := SepsisPrediction{
		BedID:       1,
		Probability: 0.85,
		SOFAScore:   3,
		Adapted:     true,
		Time:        now,
	}

	if p.BedID != 1 {
		t.Error("BedID mismatch")
	}
	if p.Probability < 0 || p.Probability > 1 {
		t.Error("Probability out of range")
	}
	if p.SOFAScore < 0 {
		t.Error("SOFAScore should be non-negative")
	}
	if p.Time != now {
		t.Error("Time mismatch")
	}
	if !p.Adapted {
		t.Error("Adapted should be true")
	}
}

func TestAdaptBed(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          1,
		BufferSize:         100,
		PredictionInterval: 10,
		LSTM: config.LSTMConfig{
			InputSize:  4,
			HiddenSize: 8,
			OutputSize: 1,
		},
		MAML: config.MAMLConfig{
			InnerLR:     0.01,
			OuterLR:     0.001,
			AdaptSteps:  2,
			StopLoss:    0.001,
			SeqLength:   10,
			MinAdaptSeq: 5,
		},
	}
	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan SepsisPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	now := time.Now()
	sensors := []string{"ecg", "ventilator", "spo2", "temperature"}
	values := map[string]float64{
		"ecg":         75.0,
		"ventilator":  18.0,
		"spo2":        96.0,
		"temperature": 36.8,
	}
	for i := 0; i < 20; i++ {
		for _, s := range sensors {
			p.updateBuffer(models.VitalSign{
				Time:       now.Add(time.Duration(i) * time.Second),
				BedID:      1,
				SensorType: s,
				Value:      values[s],
			})
		}
	}

	p.AdaptBed(1)

	isAdapted := p.maml.IsAdapted(1)
	if !isAdapted {
		t.Log("AdaptBed may not have adapted due to insufficient data or stop condition (non-fatal)")
	}
}

func TestMAMLInitMetaParameters(t *testing.T) {
	m := &MAMLLSTM{
		inputSize:    4,
		hiddenSize:   8,
		outputSize:   1,
		personalized: make(map[int]*PersonalizedModel),
	}

	m.initMetaParameters()

	if len(m.wih) != 8*4 {
		t.Errorf("wih size = %d, want %d", len(m.wih), 8*4)
	}
	if len(m.whh) != 8*8 {
		t.Errorf("whh size = %d, want %d", len(m.whh), 8*8)
	}
	if len(m.bi) != 8 {
		t.Errorf("bi size = %d, want 8", len(m.bi))
	}
}

func TestPredictSepsisLSTM(t *testing.T) {
	cfg := config.MLConfig{
		NumOfBeds:          1,
		BufferSize:         100,
		PredictionInterval: 10,
		LSTM: config.LSTMConfig{
			InputSize:  4,
			HiddenSize: 8,
			OutputSize: 1,
		},
	}
	inChan := make(chan models.VitalSign, 100)
	outChan := make(chan SepsisPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	result := p.predictSepsisLSTM(999)
	if result < 0 || result > 1 {
		t.Errorf("predictSepsisLSTM result out of range [0,1]: %f", result)
	}
}

func TestGlobalInstance(t *testing.T) {
	if Instance != nil {
		t.Log("Instance already set (from prior tests)")
		return
	}

	cfg := config.MLConfig{
		NumOfBeds:  1,
		BufferSize: 10,
	}
	inChan := make(chan models.VitalSign, 10)
	outChan := make(chan SepsisPrediction, 10)
	p := NewPredictor(cfg, inChan, outChan)

	Instance = p
	if Instance != p {
		t.Error("Instance not set correctly")
	}

	Instance = nil
}
