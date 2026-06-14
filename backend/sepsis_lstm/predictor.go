package sepsis_lstm

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

type SepsisPrediction struct {
	BedID       int
	Probability float64
	SOFAScore   int
	Adapted     bool
	Time        time.Time
}

type PersonalizedModel struct {
	wih      []float64
	whh      []float64
	bi       []float64
	wfh      []float64
	bf       []float64
	wch      []float64
	bc       []float64
	woh      []float64
	bo       []float64
	why      []float64
	by       float64
	adapted  bool
	steps    int
	lastLoss float64
}

type MAMLLSTM struct {
	inputSize  int
	hiddenSize int
	outputSize int
	innerLR    float64
	outerLR    float64
	adaptSteps int
	stopLoss   float64

	wih []float64
	whh []float64
	bi  []float64
	wfh []float64
	bf  []float64
	wch []float64
	bc  []float64
	woh []float64
	bo  []float64
	why []float64
	by  float64

	personalized map[int]*PersonalizedModel
	mux          sync.RWMutex
}

type Predictor struct {
	InChan      chan models.VitalSign
	OutChan     chan SepsisPrediction
	cfg         config.MLConfig
	vitalBuffer map[int]map[string][]float64
	bufferMux   sync.RWMutex
	maml        *MAMLLSTM
	stopChan    chan struct{}
	wg          sync.WaitGroup
}

var Instance *Predictor

func NewPredictor(cfg config.MLConfig, inChan chan models.VitalSign, outChan chan SepsisPrediction) *Predictor {
	p := &Predictor{
		InChan:      inChan,
		OutChan:     outChan,
		cfg:         cfg,
		vitalBuffer: make(map[int]map[string][]float64),
		stopChan:    make(chan struct{}),
	}

	for i := 1; i <= cfg.NumOfBeds; i++ {
		p.vitalBuffer[i] = map[string][]float64{
			"ecg":         make([]float64, 0, cfg.BufferSize),
			"ventilator":  make([]float64, 0, cfg.BufferSize),
			"spo2":        make([]float64, 0, cfg.BufferSize),
			"temperature": make([]float64, 0, cfg.BufferSize),
		}
	}

	p.maml = &MAMLLSTM{
		inputSize:    cfg.LSTM.InputSize,
		hiddenSize:   cfg.LSTM.HiddenSize,
		outputSize:   cfg.LSTM.OutputSize,
		innerLR:      cfg.MAML.InnerLR,
		outerLR:      cfg.MAML.OuterLR,
		adaptSteps:   cfg.MAML.AdaptSteps,
		stopLoss:     cfg.MAML.StopLoss,
		personalized: make(map[int]*PersonalizedModel),
	}
	p.maml.initMetaParameters()

	rand.Seed(time.Now().UnixNano())

	return p
}

func (m *MAMLLSTM) initMetaParameters() {
	scale := math.Sqrt(2.0 / float64(m.inputSize+m.hiddenSize))

	m.wih = make([]float64, m.hiddenSize*m.inputSize)
	m.whh = make([]float64, m.hiddenSize*m.hiddenSize)
	m.bi = make([]float64, m.hiddenSize)
	for i := range m.wih {
		m.wih[i] = (rand.Float64() - 0.5) * 2 * scale
	}
	for i := range m.whh {
		m.whh[i] = (rand.Float64() - 0.5) * 2 * scale
	}

	m.wfh = make([]float64, m.hiddenSize*(m.inputSize+m.hiddenSize))
	m.bf = make([]float64, m.hiddenSize)
	for i := range m.wfh {
		m.wfh[i] = (rand.Float64() - 0.5) * 2 * scale
	}
	for i := range m.bf {
		m.bf[i] = 1.0
	}

	m.wch = make([]float64, m.hiddenSize*(m.inputSize+m.hiddenSize))
	m.bc = make([]float64, m.hiddenSize)
	for i := range m.wch {
		m.wch[i] = (rand.Float64() - 0.5) * 2 * scale
	}

	m.woh = make([]float64, m.hiddenSize*(m.inputSize+m.hiddenSize))
	m.bo = make([]float64, m.hiddenSize)
	for i := range m.woh {
		m.woh[i] = (rand.Float64() - 0.5) * 2 * scale
	}

	m.why = make([]float64, m.outputSize*m.hiddenSize)
	for i := range m.why {
		m.why[i] = (rand.Float64() - 0.5) * 2 * 0.1
	}
	m.by = 0.5
}

func (p *Predictor) Start() {
	p.wg.Add(2)

	go p.bufferLoop()
	go p.predictionLoop()
}

func (p *Predictor) Stop() {
	close(p.stopChan)
	p.wg.Wait()
}

func (p *Predictor) bufferLoop() {
	defer p.wg.Done()
	for {
		select {
		case <-p.stopChan:
			return
		case v := <-p.InChan:
			p.updateBuffer(v)
		}
	}
}

func (p *Predictor) predictionLoop() {
	defer p.wg.Done()
	interval := time.Duration(p.cfg.PredictionInterval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			results := p.RunPredictionAll()
			for _, pred := range results {
				select {
				case p.OutChan <- pred:
				default:
				}
			}
		}
	}
}

func (p *Predictor) updateBuffer(v models.VitalSign) {
	p.bufferMux.Lock()
	defer p.bufferMux.Unlock()

	if _, ok := p.vitalBuffer[v.BedID]; !ok {
		p.vitalBuffer[v.BedID] = map[string][]float64{
			"ecg":         make([]float64, 0, p.cfg.BufferSize),
			"ventilator":  make([]float64, 0, p.cfg.BufferSize),
			"spo2":        make([]float64, 0, p.cfg.BufferSize),
			"temperature": make([]float64, 0, p.cfg.BufferSize),
		}
	}

	buf, ok := p.vitalBuffer[v.BedID][v.SensorType]
	if !ok {
		return
	}
	buf = append(buf, v.Value)
	if len(buf) > p.cfg.BufferSize {
		buf = buf[len(buf)-p.cfg.BufferSize:]
	}
	p.vitalBuffer[v.BedID][v.SensorType] = buf
}

func (p *Predictor) RunPrediction(bedID int) SepsisPrediction {
	prob := p.predictSepsisLSTM(bedID)
	sofa := p.calculateSOFA(bedID)

	adapted := p.maml.IsAdapted(bedID)

	return SepsisPrediction{
		BedID:       bedID,
		Probability: prob,
		SOFAScore:   sofa,
		Adapted:     adapted,
		Time:        time.Now(),
	}
}

func (p *Predictor) RunPredictionAll() map[int]SepsisPrediction {
	p.bufferMux.RLock()
	beds := make([]int, 0, len(p.vitalBuffer))
	for bedID := range p.vitalBuffer {
		beds = append(beds, bedID)
	}
	p.bufferMux.RUnlock()

	results := make(map[int]SepsisPrediction, len(beds))
	for _, bedID := range beds {
		results[bedID] = p.RunPrediction(bedID)
	}
	return results
}

func (p *Predictor) AdaptBed(bedID int) {
	seqLen := p.cfg.MAML.SeqLength
	sequence := p.buildMAMLSequence(bedID, seqLen)
	if len(sequence) < p.cfg.MAML.MinAdaptSeq*p.cfg.LSTM.InputSize {
		return
	}

	sofa := float64(p.calculateSOFA(bedID))
	target := math.Min(sofa/12.0, 1.0)
	target = math.Max(0.05, target)

	p.maml.Personalize(bedID, sequence, target)
}

func (p *Predictor) predictSepsisLSTM(bedID int) float64 {
	p.bufferMux.RLock()
	defer p.bufferMux.RUnlock()

	seqLen := p.cfg.LSTMSequenceLength

	if p.maml.IsAdapted(bedID) {
		sequence := p.buildMAMLSequence(bedID, seqLen)
		if len(sequence) >= 10*p.cfg.LSTM.InputSize {
			mamlProb := p.maml.Predict(bedID, sequence)
			sofa := float64(p.calculateSOFA(bedID))
			sofaInfluence := math.Min(sofa/12.0, 1.0)
			finalProb := mamlProb*p.cfg.LSTM.MAMLWeight + sofaInfluence*p.cfg.LSTM.SOFAWeight
			return math.Max(0, math.Min(1, finalProb))
		}
	}

	features := p.extractLSTMFeatures(bedID, seqLen)

	if len(features) == 0 {
		return p.cfg.LSTM.FallbackBaseRate + rand.Float64()*0.1
	}

	hidden := make([]float64, p.cfg.LSTM.HiddenSize)
	for t := 0; t < len(features); t++ {
		for h := range hidden {
			inputGate := sigmoid(features[t]*0.5 + hidden[h]*0.3)
			forgetGate := sigmoid(features[t]*0.4 + hidden[h]*0.4)
			cellState := forgetGate*hidden[h] + inputGate*tanh(features[t]*0.6)
			outputGate := sigmoid(features[t]*0.3 + hidden[h]*0.5)
			hidden[h] = outputGate * tanh(cellState)
		}
	}

	prob := 0.0
	for _, h := range hidden {
		prob += h * p.cfg.LSTM.LSTMOutputWeight
	}
	prob = sigmoid(prob + p.cfg.LSTM.LSTMOutputBias)

	sofa := float64(p.calculateSOFA(bedID))
	sofaInfluence := math.Min(sofa/12.0, 1.0)
	prob = prob*(1-p.cfg.LSTM.SOFAWeight) + sofaInfluence*p.cfg.LSTM.SOFAWeight

	return math.Max(0, math.Min(1, prob))
}

func (p *Predictor) extractLSTMFeatures(bedID int, seqLen int) []float64 {
	features := make([]float64, 0)
	sensors := []string{"ecg", "ventilator", "spo2", "temperature"}

	for _, s := range sensors {
		buf := p.vitalBuffer[bedID][s]
		if len(buf) < seqLen {
			seqLen = len(buf)
		}
	}

	if seqLen < 10 {
		return features
	}

	start := len(p.vitalBuffer[bedID]["ecg"]) - seqLen
	for t := 0; t < seqLen; t++ {
		f := 0.0
		for _, s := range sensors {
			buf := p.vitalBuffer[bedID][s]
			normVal := p.normalizeValue(s, buf[start+t])
			f += normVal
		}
		features = append(features, f/4.0)
	}

	return features
}

func (p *Predictor) calculateSOFA(bedID int) int {
	p.bufferMux.RLock()
	defer p.bufferMux.RUnlock()

	score := 0

	ecg := p.getLatest(p.vitalBuffer[bedID]["ecg"])
	spo2 := p.getLatest(p.vitalBuffer[bedID]["spo2"])
	temp := p.getLatest(p.vitalBuffer[bedID]["temperature"])
	vent := p.getLatest(p.vitalBuffer[bedID]["ventilator"])

	if spo2 > 0 {
		pfRatio := spo2 * 5
		switch {
		case pfRatio >= 400:
		case pfRatio >= 300:
			score += 1
		case pfRatio >= 200:
			score += 2
		case pfRatio >= 100:
			score += 3
		default:
			score += 4
		}
	}

	if vent > 0 {
		switch {
		case vent < 12 || vent > 30:
			score += 2
		}
	}

	if ecg > 0 {
		mapPressure := ecg * 0.9
		switch {
		case mapPressure >= 70:
		case mapPressure >= 65:
			score += 1
		default:
			score += 2
		}
	}

	if temp > 0 {
		switch {
		case temp > 39 || temp < 36:
			score += 1
		}
	}

	if score > 24 {
		score = 24
	}

	return score
}

func (p *Predictor) getLatest(buf []float64) float64 {
	if len(buf) == 0 {
		return 0
	}
	return buf[len(buf)-1]
}

func (p *Predictor) normalizeValue(sensor string, val float64) float64 {
	norm := p.cfg.Normalization
	switch sensor {
	case "ecg":
		return (val - norm.ECGMean) / norm.ECGStd
	case "ventilator":
		return (val - norm.VentilatorMean) / norm.VentilatorStd
	case "spo2":
		return (val - norm.SpO2Mean) / norm.SpO2Std
	case "temperature":
		return (val - norm.TemperatureMean) / norm.TemperatureStd
	default:
		return 0
	}
}

func (p *Predictor) buildMAMLSequence(bedID int, seqLen int) []float64 {
	sensors := []string{"ecg", "ventilator", "spo2", "temperature"}
	sequence := make([]float64, 0, seqLen*len(sensors))

	minLen := seqLen
	for _, s := range sensors {
		if len(p.vitalBuffer[bedID][s]) < minLen {
			minLen = len(p.vitalBuffer[bedID][s])
		}
	}

	if minLen < p.cfg.MAML.MinAdaptSeq {
		return sequence
	}

	start := len(p.vitalBuffer[bedID]["ecg"]) - minLen
	for t := 0; t < minLen && t < seqLen; t++ {
		for _, s := range sensors {
			buf := p.vitalBuffer[bedID][s]
			val := p.normalizeValue(s, buf[start+t])
			sequence = append(sequence, val)
		}
	}

	return sequence
}

func (m *MAMLLSTM) Personalize(bedID int, sequence []float64, target float64) {
	m.mux.Lock()
	defer m.mux.Unlock()

	pm, exists := m.personalized[bedID]
	if !exists {
		pm = m.cloneMetaParams()
		m.personalized[bedID] = pm
	}

	for step := 0; step < m.adaptSteps; step++ {
		loss := m.innerLoopUpdate(pm, sequence, target)
		pm.lastLoss = loss
		pm.steps++

		if loss < m.stopLoss {
			break
		}
	}

	pm.adapted = true
}

func (m *MAMLLSTM) innerLoopUpdate(pm *PersonalizedModel, sequence []float64, target float64) float64 {
	pred := m.forwardWithParams(sequence, pm)
	loss := mseLoss(pred, target)

	dPred := 2 * (pred - target)

	dWhy := make([]float64, m.outputSize*m.hiddenSize)
	dBy := dPred

	hidden := m.getLastHidden(sequence, pm)

	for i := 0; i < m.hiddenSize; i++ {
		dWhy[i] = dPred * hidden[i]
	}

	dHidden := make([]float64, m.hiddenSize)
	for i := 0; i < m.hiddenSize; i++ {
		dHidden[i] = dPred * pm.why[i]
	}

	for i := 0; i < m.hiddenSize; i++ {
		pm.why[i] -= m.innerLR * dWhy[i]
	}
	pm.by -= m.innerLR * dBy

	for i := 0; i < m.hiddenSize; i++ {
		pm.bi[i] -= m.innerLR * dHidden[i] * 0.1
		pm.bc[i] -= m.innerLR * dHidden[i] * 0.1
		pm.bo[i] -= m.innerLR * dHidden[i] * 0.1
	}

	return loss
}

func (m *MAMLLSTM) forwardWithParams(sequence []float64, pm *PersonalizedModel) float64 {
	hidden := make([]float64, m.hiddenSize)
	cell := make([]float64, m.hiddenSize)

	seqLen := len(sequence) / m.inputSize
	for t := 0; t < seqLen; t++ {
		input := make([]float64, m.inputSize)
		for i := 0; i < m.inputSize; i++ {
			input[i] = sequence[t*m.inputSize+i]
		}

		newHidden := make([]float64, m.hiddenSize)
		newCell := make([]float64, m.hiddenSize)

		for h := 0; h < m.hiddenSize; h++ {
			iGate := 0.0
			fGate := 0.0
			cGate := 0.0
			oGate := 0.0

			for i := 0; i < m.inputSize; i++ {
				iGate += input[i] * pm.wih[h*m.inputSize+i]
			}
			for hh := 0; hh < m.hiddenSize; hh++ {
				iGate += hidden[hh] * pm.whh[h*m.hiddenSize+hh]
			}
			iGate += pm.bi[h]
			iGate = sigmoid(iGate)

			for i := 0; i < m.inputSize+m.hiddenSize; i++ {
				idx := h*(m.inputSize+m.hiddenSize) + i
				if i < m.inputSize {
					fGate += input[i] * pm.wfh[idx]
				} else {
					fGate += hidden[i-m.inputSize] * pm.wfh[idx]
				}
			}
			fGate += pm.bf[h]
			fGate = sigmoid(fGate)

			for i := 0; i < m.inputSize+m.hiddenSize; i++ {
				idx := h*(m.inputSize+m.hiddenSize) + i
				if i < m.inputSize {
					cGate += input[i] * pm.wch[idx]
				} else {
					cGate += hidden[i-m.inputSize] * pm.wch[idx]
				}
			}
			cGate += pm.bc[h]
			cGate = tanh(cGate)

			for i := 0; i < m.inputSize+m.hiddenSize; i++ {
				idx := h*(m.inputSize+m.hiddenSize) + i
				if i < m.inputSize {
					oGate += input[i] * pm.woh[idx]
				} else {
					oGate += hidden[i-m.inputSize] * pm.woh[idx]
				}
			}
			oGate += pm.bo[h]
			oGate = sigmoid(oGate)

			newCell[h] = fGate*cell[h] + iGate*cGate
			newHidden[h] = oGate * tanh(newCell[h])
		}

		hidden = newHidden
		cell = newCell
	}

	output := pm.by
	for i := 0; i < m.hiddenSize; i++ {
		output += hidden[i] * pm.why[i]
	}

	return sigmoid(output)
}

func (m *MAMLLSTM) getLastHidden(sequence []float64, pm *PersonalizedModel) []float64 {
	hidden := make([]float64, m.hiddenSize)
	cell := make([]float64, m.hiddenSize)

	seqLen := len(sequence) / m.inputSize
	for t := 0; t < seqLen; t++ {
		input := make([]float64, m.inputSize)
		for i := 0; i < m.inputSize; i++ {
			input[i] = sequence[t*m.inputSize+i]
		}

		newHidden := make([]float64, m.hiddenSize)
		newCell := make([]float64, m.hiddenSize)

		for h := 0; h < m.hiddenSize; h++ {
			iGate := pm.bi[h]
			for i := 0; i < m.inputSize; i++ {
				iGate += input[i] * pm.wih[h*m.inputSize+i]
			}
			for hh := 0; hh < m.hiddenSize; hh++ {
				iGate += hidden[hh] * pm.whh[h*m.hiddenSize+hh]
			}
			iGate = sigmoid(iGate)

			fGate := pm.bf[h]
			for i := 0; i < m.inputSize+m.hiddenSize; i++ {
				idx := h*(m.inputSize+m.hiddenSize) + i
				if i < m.inputSize {
					fGate += input[i] * pm.wfh[idx]
				} else {
					fGate += hidden[i-m.inputSize] * pm.wfh[idx]
				}
			}
			fGate = sigmoid(fGate)

			cGate := pm.bc[h]
			for i := 0; i < m.inputSize+m.hiddenSize; i++ {
				idx := h*(m.inputSize+m.hiddenSize) + i
				if i < m.inputSize {
					cGate += input[i] * pm.wch[idx]
				} else {
					cGate += hidden[i-m.inputSize] * pm.wch[idx]
				}
			}
			cGate = tanh(cGate)

			oGate := pm.bo[h]
			for i := 0; i < m.inputSize+m.hiddenSize; i++ {
				idx := h*(m.inputSize+m.hiddenSize) + i
				if i < m.inputSize {
					oGate += input[i] * pm.woh[idx]
				} else {
					oGate += hidden[i-m.inputSize] * pm.woh[idx]
				}
			}
			oGate = sigmoid(oGate)

			newCell[h] = fGate*cell[h] + iGate*cGate
			newHidden[h] = oGate * tanh(newCell[h])
		}

		hidden = newHidden
		cell = newCell
	}

	return hidden
}

func (m *MAMLLSTM) Predict(bedID int, sequence []float64) float64 {
	m.mux.RLock()
	pm, exists := m.personalized[bedID]
	m.mux.RUnlock()

	if !exists {
		return m.forwardWithParams(sequence, m.metaToPersonalized())
	}

	return m.forwardWithParams(sequence, pm)
}

func (m *MAMLLSTM) IsAdapted(bedID int) bool {
	m.mux.RLock()
	defer m.mux.RUnlock()
	if pm, ok := m.personalized[bedID]; ok {
		return pm.adapted
	}
	return false
}

func (m *MAMLLSTM) cloneMetaParams() *PersonalizedModel {
	pm := &PersonalizedModel{}

	pm.wih = make([]float64, len(m.wih))
	copy(pm.wih, m.wih)
	pm.whh = make([]float64, len(m.whh))
	copy(pm.whh, m.whh)
	pm.bi = make([]float64, len(m.bi))
	copy(pm.bi, m.bi)

	pm.wfh = make([]float64, len(m.wfh))
	copy(pm.wfh, m.wfh)
	pm.bf = make([]float64, len(m.bf))
	copy(pm.bf, m.bf)

	pm.wch = make([]float64, len(m.wch))
	copy(pm.wch, m.wch)
	pm.bc = make([]float64, len(m.bc))
	copy(pm.bc, m.bc)

	pm.woh = make([]float64, len(m.woh))
	copy(pm.woh, m.woh)
	pm.bo = make([]float64, len(m.bo))
	copy(pm.bo, m.bo)

	pm.why = make([]float64, len(m.why))
	copy(pm.why, m.why)
	pm.by = m.by

	return pm
}

func (m *MAMLLSTM) metaToPersonalized() *PersonalizedModel {
	return m.cloneMetaParams()
}

func mseLoss(pred, target float64) float64 {
	diff := pred - target
	return diff * diff
}

func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}

func tanh(x float64) float64 {
	return math.Tanh(x)
}
