package models

import "time"

type Bed struct {
	ID           int        `json:"id"`
	BedCode      string     `json:"bed_code"`
	PatientName  string     `json:"patient_name"`
	PatientAge   int        `json:"patient_age"`
	PatientGender string    `json:"patient_gender"`
	Status       string     `json:"status"`
	AdmissionTime *time.Time `json:"admission_time"`
	LocationX    float64    `json:"location_x"`
	LocationY    float64    `json:"location_y"`
	CreatedAt    time.Time  `json:"created_at"`
}

type VitalSign struct {
	Time       time.Time `json:"time"`
	BedID      int       `json:"bed_id"`
	SensorType string    `json:"sensor_type"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`
}

type MQTTMessage struct {
	BedID      int     `json:"bed_id"`
	SensorType string  `json:"sensor_type"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	Timestamp  int64   `json:"timestamp"`
}

type Prediction struct {
	Time              time.Time `json:"time"`
	BedID             int       `json:"bed_id"`
	SepsisRisk        float64   `json:"sepsis_risk"`
	SepsisProbability float64   `json:"sepsis_probability"`
	CRERisk           float64   `json:"cre_risk"`
	MRSARisk          float64   `json:"mrsa_risk"`
	SOFAScore         float64   `json:"sofa_score"`
}

type Alert struct {
	ID             int        `json:"id"`
	BedID          int        `json:"bed_id"`
	AlertType      string     `json:"alert_type"`
	Severity       string     `json:"severity"`
	Message        string     `json:"message"`
	TriggerValue   float64    `json:"trigger_value"`
	Threshold      float64    `json:"threshold"`
	Acknowledged   bool       `json:"acknowledged"`
	AcknowledgedBy string     `json:"acknowledged_by"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

type WSMessage struct {
	Type    string      `json:"type"`
	Data    interface{} `json:"data"`
	Time    time.Time   `json:"time"`
}

type InfectionRiskPoint struct {
	BedID    int     `json:"bed_id"`
	BedCode  string  `json:"bed_code"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	CRERisk  float64 `json:"cre_risk"`
	MRSARisk float64 `json:"mrsa_risk"`
	MaxRisk  float64 `json:"max_risk"`
}

type AntibioticRecord struct {
	BedID          int       `json:"bed_id"`
	AntibioticType string    `json:"antibiotic_type"`
	Dosage         float64   `json:"dosage"`
	StartDate      time.Time `json:"start_date"`
	EndDate        time.Time `json:"end_date"`
}

type InvasiveProcedure struct {
	BedID         int       `json:"bed_id"`
	ProcedureType string    `json:"procedure_type"`
	ProcedureTime time.Time `json:"procedure_time"`
	Notes         string    `json:"notes"`
}

type Statistics struct {
	TotalBeds          int     `json:"total_beds"`
	OccupiedBeds       int     `json:"occupied_beds"`
	ActiveAlerts       int     `json:"active_alerts"`
	HighRiskSepsis     int     `json:"high_risk_sepsis"`
	HighRiskInfection  int     `json:"high_risk_infection"`
	AvgSOFAScore       float64 `json:"avg_sofa_score"`
	LastUpdate         time.Time `json:"last_update"`
}

type BedVitalsSnapshot struct {
	BedID   int                `json:"bed_id"`
	BedCode string             `json:"bed_code"`
	Vitals  map[string]float64 `json:"vitals"`
	Risk    Prediction         `json:"risk"`
}
