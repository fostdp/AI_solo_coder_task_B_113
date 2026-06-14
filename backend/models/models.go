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

type VentilatorParam struct {
	BedID                uint32    `db:"bed_id" json:"bed_id"`
	PeakPressure         float64   `db:"peak_pressure" json:"peak_pressure"`
	TidalVolume          float64   `db:"tidal_volume" json:"tidal_volume"`
	OralSecretionGrade   float64   `db:"oral_secretion_grade" json:"oral_secretion_grade"`
	VentilatorHours      int       `db:"ventilator_hours" json:"ventilator_hours"`
	Time                 time.Time `db:"time" json:"time"`
	PredictedWeight      float64   `db:"predicted_weight" json:"predicted_weight"`
	OralSecretion        float64   `db:"oral_secretion" json:"oral_secretion"`
	PriorInfection       float64   `db:"prior_infection" json:"prior_infection"`
}

type VapRiskRecord struct {
	ID                  uint32            `db:"id" json:"id"`
	BedID               uint32            `db:"bed_id" json:"bed_id"`
	RiskProbability     float64           `db:"risk_probability" json:"risk_probability"`
	HazardsRatio        float64           `db:"hazards_ratio" json:"hazards_ratio"`
	PredictedOnsetHours float64           `db:"predicted_onset_hours" json:"predicted_onset_hours"`
	FeatureWeights      map[string]float64 `db:"feature_weights" json:"feature_weights"`
	Time                time.Time         `db:"time" json:"time"`
	RiskProb            float64           `db:"-" json:"risk_prob"`
	PredictedOnset      float64           `db:"-" json:"predicted_onset"`
	Timestamp           time.Time         `db:"-" json:"timestamp"`
	Features            map[string]float64 `db:"features" json:"features"`
}

type CultureResult struct {
	ID                    uint32    `db:"id" json:"id"`
	BedID                 uint32    `db:"bed_id" json:"bed_id"`
	BacteriaName          string    `db:"bacteria_name" json:"bacteria_name"`
	ResistanceGenes       string    `db:"resistance_genes" json:"resistance_genes"`
	AntibioticSensitivity string    `db:"antibiotic_sensitivity" json:"antibiotic_sensitivity"`
	Time                  time.Time `db:"time" json:"time"`
	Result                string    `db:"result" json:"result"`
	CollectedAt           time.Time `db:"collected_at" json:"collected_at"`
	ReportedAt            time.Time `db:"reported_at" json:"reported_at"`
}

type ResistancePrediction struct {
	ID             uint32     `db:"id" json:"id"`
	BedID          uint32     `db:"bed_id" json:"bed_id"`
	BacteriaName   string     `db:"bacteria_name" json:"bacteria_name"`
	GeneSpreadProb float64    `db:"gene_spread_prob" json:"gene_spread_prob"`
	SpreadPath     []uint32   `db:"spread_path" json:"spread_path"`
	EdgeWeights    []float64  `db:"edge_weights" json:"edge_weights"`
	Time           time.Time  `db:"time" json:"time"`
	SourceBed      uint32     `db:"source_bed" json:"source_bed"`
	SpreadProb     float64    `db:"spread_prob" json:"spread_prob"`
	Path           []uint32   `db:"path" json:"path"`
	PredictedAt    time.Time  `db:"predicted_at" json:"predicted_at"`
	IsFallback     bool       `db:"is_fallback" json:"is_fallback"`
}

type ScheduleRequest struct {
	TimeWindow      string          `db:"time_window" json:"time_window"`
	AvailableNurses []string        `db:"available_nurses" json:"available_nurses"`
	IsolationNeeds  map[uint32]bool `db:"isolation_needs" json:"isolation_needs"`
}

type OptimizerDecision struct {
	BedID         uint32 `db:"bed_id" json:"bed_id"`
	AssignedRoom  string `db:"assigned_room" json:"assigned_room"`
	AssignedNurse string `db:"assigned_nurse" json:"assigned_nurse"`
}

type OptimizerObjective struct {
	InfectionRisk         float64 `db:"infection_risk" json:"infection_risk"`
	NurseWorkloadBalance  float64 `db:"nurse_workload_balance" json:"nurse_workload_balance"`
	RoomUtilization       float64 `db:"room_utilization" json:"room_utilization"`
	TransportDistance     float64 `db:"transport_distance" json:"transport_distance"`
	TotalCost             float64 `db:"total_cost" json:"total_cost"`
}

type OptimizerSolution struct {
	ID                   uint32              `db:"id" json:"id"`
	SolveTime            int                 `db:"solve_time_ms" json:"solve_time_ms"`
	NegativePressureAssign map[uint32]string `db:"negative_pressure_assign" json:"negative_pressure_assign"`
	NurseSchedule        map[string][]uint32 `db:"nurse_schedule" json:"nurse_schedule"`
	UnmetNeeds           []string            `db:"unmet_needs" json:"unmet_needs"`
	Cost                 float64             `db:"cost" json:"cost"`
	Status               string              `db:"status" json:"status"`
	Time                 time.Time           `db:"time" json:"time"`
	SolutionID           string              `db:"solution_id" json:"solution_id"`
	Timestamp            time.Time           `db:"timestamp" json:"timestamp"`
	Assignments          map[uint32]string   `db:"assignments" json:"-"`
	Schedule             map[string][]uint32 `db:"schedule" json:"-"`
	Objective            map[string]float64  `db:"objective" json:"objective"`
	Decisions            []map[string]interface{} `db:"decisions" json:"decisions"`
}

type TransportRequest struct {
	ID            uint32 `db:"id" json:"id"`
	FromBed       uint32 `db:"from_bed" json:"from_bed"`
	ToBed         uint32 `db:"to_bed" json:"to_bed"`
	DistanceMeters int    `db:"distance_meters" json:"distance_meters"`
	Priority      int    `db:"priority" json:"priority"`
	Urgent        bool   `db:"urgent" json:"urgent"`
	RequestID     uint32 `db:"request_id" json:"request_id"`
	BedID         uint32 `db:"bed_id" json:"bed_id"`
	Distance      float64 `db:"distance" json:"distance"`
	HourOfDay     int    `db:"hour_of_day" json:"hour_of_day"`
	FromBedID     uint32 `db:"from_bed_id" json:"from_bed_id"`
	ToBedID       uint32 `db:"to_bed_id" json:"to_bed_id"`
	PatientAge    int    `db:"patient_age" json:"patient_age"`
}

type TransportRiskResult struct {
	ID               uint32            `db:"id" json:"id"`
	RequestID        uint32            `db:"request_id" json:"request_id"`
	RiskScore        int               `db:"risk_score" json:"risk_score"`
	RiskLevel        string            `db:"risk_level" json:"risk_level"`
	AdverseEventProb float64           `db:"adverse_event_prob" json:"adverse_event_prob"`
	FeatureContrib   map[string]float64 `db:"feature_contrib" json:"feature_contrib"`
	Recommendations  []string          `db:"recommendations" json:"recommendations"`
	Time             time.Time         `db:"time" json:"time"`
	Timestamp        time.Time         `db:"timestamp" json:"timestamp"`
	BedID            uint32            `db:"bed_id" json:"bed_id"`
}
