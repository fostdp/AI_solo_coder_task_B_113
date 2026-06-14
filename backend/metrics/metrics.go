package metrics

import (
	"net/http"
	_ "net/http/pprof"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	VitalSignsReceived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "field_hospital_vital_signs_received_total",
		Help: "Total number of vital signs received from MQTT",
	})

	VitalSignsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "field_hospital_vital_signs_processed_total",
		Help: "Total number of vital signs processed",
	})

	PredictionsMade = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "field_hospital_predictions_total",
			Help: "Total number of predictions made by model type",
		},
		[]string{"model_type"},
	)

	AlertsTriggered = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "field_hospital_alerts_total",
			Help: "Total number of alerts triggered by type and severity",
		},
		[]string{"alert_type", "severity"},
	)

	ActiveWebSocketClients = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "field_hospital_websocket_clients_active",
		Help: "Number of active WebSocket clients",
	})

	PredictionLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "field_hospital_prediction_latency_seconds",
			Help:    "Latency of predictions in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"model_type"},
	)

	MQTTMessagesDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "field_hospital_mqtt_messages_dropped_total",
		Help: "Total number of MQTT messages dropped due to full queues",
	})

	DatabaseBatchSize = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "field_hospital_database_batch_size",
		Help:    "Size of database write batches",
		Buckets: []float64{10, 50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 1000},
	})

	HighRiskPatients = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "field_hospital_high_risk_patients",
			Help: "Number of high risk patients by risk type",
		},
		[]string{"risk_type"},
	)

	SOFAScoreDistribution = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "field_hospital_sofa_score_distribution",
		Help:    "Distribution of SOFA scores across all beds",
		Buckets: []float64{0, 1, 2, 3, 4, 6, 8, 10, 12, 16, 20, 24},
	})
)

var (
	startTime          time.Time
	uptime             atomic.Int64
	requestsInFlight   atomic.Int32
	totalRequests      atomic.Int64
	totalErrors        atomic.Int64
)

func init() {
	startTime = time.Now()

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			uptime.Store(int64(time.Since(startTime).Seconds()))
		}
	}()
}

func SetupMetrics() {
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/debug/pprof/", func(w http.ResponseWriter, r *http.Request) {
			http.DefaultServeMux.ServeHTTP(w, r)
		})
		mux.HandleFunc("/debug/pprof/cmdline", func(w http.ResponseWriter, r *http.Request) {
			http.DefaultServeMux.ServeHTTP(w, r)
		})
		mux.HandleFunc("/debug/pprof/profile", func(w http.ResponseWriter, r *http.Request) {
			http.DefaultServeMux.ServeHTTP(w, r)
		})
		mux.HandleFunc("/debug/pprof/symbol", func(w http.ResponseWriter, r *http.Request) {
			http.DefaultServeMux.ServeHTTP(w, r)
		})
		mux.HandleFunc("/debug/pprof/trace", func(w http.ResponseWriter, r *http.Request) {
			http.DefaultServeMux.ServeHTTP(w, r)
		})

		if err := http.ListenAndServe(":6060", mux); err != nil {
			panic(err)
		}
	}()
}

func IncVitalSignsReceived() {
	VitalSignsReceived.Inc()
}

func IncVitalSignsProcessed() {
	VitalSignsProcessed.Inc()
}

func IncPredictions(modelType string) {
	PredictionsMade.WithLabelValues(modelType).Inc()
}

func IncAlerts(alertType, severity string) {
	AlertsTriggered.WithLabelValues(alertType, severity).Inc()
}

func SetActiveWebSocketClients(n int) {
	ActiveWebSocketClients.Set(float64(n))
}

func ObservePredictionLatency(modelType string, duration time.Duration) {
	PredictionLatency.WithLabelValues(modelType).Observe(duration.Seconds())
}

func IncMQTTMessagesDropped() {
	MQTTMessagesDropped.Inc()
}

func ObserveDatabaseBatchSize(size int) {
	DatabaseBatchSize.Observe(float64(size))
}

func SetHighRiskPatients(riskType string, count int) {
	HighRiskPatients.WithLabelValues(riskType).Set(float64(count))
}

func ObserveSOFAScore(score int) {
	SOFAScoreDistribution.Observe(float64(score))
}
