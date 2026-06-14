package ml

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

type MAMLLSTM struct {
	inputSize    int
	hiddenSize   int
	outputSize   int
	innerLR      float64
	outerLR      float64
	adaptSteps   int

	wih  []float64
	whh  []float64
	bi   []float64
	wfh  []float64
	bf   []float64
	wch  []float64
	bc   []float64
	woh  []float64
	bo   []float64
	why  []float64
	by   float64

	personalized map[int]*PersonalizedModel
	mux          sync.RWMutex
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

var MAML *MAMLLSTM

func InitMAML() {
	MAML = &MAMLLSTM{
		inputSize:    4,
		hiddenSize:   32,
		outputSize:   1,
		innerLR:      0.01,
		outerLR:      0.001,
		adaptSteps:   5,
		personalized: make(map[int]*PersonalizedModel),
	}

	MAML.initMetaParameters()
}

func (m *MAMLLSTM) initMetaParameters() {
	scale := math.Sqrt(2.0 / float64(m.inputSize+m.hiddenSize))

	m.wih = make([]float64, m.hiddenSize*m.inputSize)
	m.whh = make([]float64, m.hiddenSize*m.hiddenSize)
	m.bi = make([]float64, m.hiddenSize)
	for i := range m.wih {
		m.wih[i] = (randFloat() - 0.5) * 2 * scale
	}
	for i := range m.whh {
		m.whh[i] = (randFloat() - 0.5) * 2 * scale
	}

	m.wfh = make([]float64, m.hiddenSize*(m.inputSize+m.hiddenSize))
	m.bf = make([]float64, m.hiddenSize)
	for i := range m.wfh {
		m.wfh[i] = (randFloat() - 0.5) * 2 * scale
	}
	for i := range m.bf {
		m.bf[i] = 1.0
	}

	m.wch = make([]float64, m.hiddenSize*(m.inputSize+m.hiddenSize))
	m.bc = make([]float64, m.hiddenSize)
	for i := range m.wch {
		m.wch[i] = (randFloat() - 0.5) * 2 * scale
	}

	m.woh = make([]float64, m.hiddenSize*(m.inputSize+m.hiddenSize))
	m.bo = make([]float64, m.hiddenSize)
	for i := range m.woh {
		m.woh[i] = (randFloat() - 0.5) * 2 * scale
	}

	m.why = make([]float64, m.outputSize*m.hiddenSize)
	for i := range m.why {
		m.why[i] = (randFloat() - 0.5) * 2 * 0.1
	}
	m.by = 0.5
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

		if loss < 0.01 {
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

func (m *MAMLLSTM) AdaptSteps(bedID int) int {
	m.mux.RLock()
	defer m.mux.RUnlock()
	if pm, ok := m.personalized[bedID]; ok {
		return pm.steps
	}
	return 0
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

func (m *MAMLLSTM) GetAdaptationCount() int {
	m.mux.RLock()
	defer m.mux.RUnlock()
	count := 0
	for _, pm := range m.personalized {
		if pm.adapted {
			count++
		}
	}
	return count
}

func mseLoss(pred, target float64) float64 {
	diff := pred - target
	return diff * diff
}

func randFloat() float64 {
	return rand.Float64()
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func BuildMAMLSequence(bedID int, seqLen int) []float64 {
	vitalBufferMux.RLock()
	defer vitalBufferMux.RUnlock()

	sensors := []string{"ecg", "ventilator", "spo2", "temperature"}
	sequence := make([]float64, 0, seqLen*4)

	minLen := seqLen
	for _, s := range sensors {
		if len(vitalBuffer[bedID][s]) < minLen {
			minLen = len(vitalBuffer[bedID][s])
		}
	}

	if minLen < 10 {
		return sequence
	}

	start := len(vitalBuffer[bedID]["ecg"]) - minLen
	for t := 0; t < minLen && t < seqLen; t++ {
		for _, s := range sensors {
			buf := vitalBuffer[bedID][s]
			val := normalizeValue(s, buf[start+t])
			sequence = append(sequence, val)
		}
	}

	return sequence
}
