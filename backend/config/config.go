package config

import (
	"log"
	"github.com/spf13/viper"
)

type Config struct {
	Server      ServerConfig
	Database    DatabaseConfig
	MQTT        MQTTConfig
	Alert       AlertConfig
	ML          MLConfig
	CoxVap      CoxVapConfig
	GNN         GNNConfig
	Optimizer   OptimizerConfig
	Transport   TransportConfig
	BatchWriter BatchWriterConfig
}

type ServerConfig struct {
	Port string
}

type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
}

type MQTTConfig struct {
	Broker             string
	ClientID           string
	Username           string
	Password           string
	QoS                byte
	CleanSession       bool
	KeepAlive          int
	MessageChannelDepth int
	DecodeWorkers      int
	DecodeQueueSize    int
}

type AlertConfig struct {
	SofaThreshold        float64
	InfectionThreshold   float64
	SMSGateway           string
	DeduplicationWindow  int
}

type MLConfig struct {
	LSTMSequenceLength  int
	ModelUpdateInterval int
	NumOfBeds           int
	BufferSize          int
	PredictionInterval  int
	LSTM                LSTMConfig
	MAML                MAMLConfig
	RandomForest        RandomForestConfig
	Normalization       NormalizationConfig
}

type LSTMConfig struct {
	InputSize         int
	HiddenSize        int
	OutputSize        int
	MAMLWeight        float64
	SOFAWeight        float64
	FallbackBaseRate  float64
	LSTMOutputWeight  float64
	LSTMOutputBias    float64
}

type MAMLConfig struct {
	InnerLR       float64
	OuterLR       float64
	AdaptSteps    int
	StopLoss      float64
	SeqLength     int
	MinAdaptSeq   int
}

type RandomForestConfig struct {
	NumTrees               int
	TreeWeightMin          float64
	TreeWeightMax          float64
	FeatureWeightSets      [][]float64
	CRERiskAntibioticCoef  float64
	CRERiskInvasiveCoef    float64
	CRERiskNoise           float64
	MRSARiskAntibioticCoef float64
	MRSARiskInvasiveCoef   float64
	MRSARiskNoise          float64
}

type NormalizationConfig struct {
	ECGMean          float64
	ECGStd           float64
	VentilatorMean   float64
	VentilatorStd    float64
	SpO2Mean         float64
	SpO2Std          float64
	TemperatureMean  float64
	TemperatureStd   float64
}

type CoxVapConfig struct {
	Enabled           bool
	UpdateIntervalSec int
	ModelWeights      []float64
	RiskThreshold     float64
}

type GNNConfig struct {
	Enabled           bool
	PythonServiceURL  string
	UpdateIntervalSec int
	MaxNodes          int
}

type OptimizerConfig struct {
	Enabled             bool
	SolveIntervalSec    int
	NegativePressureBeds int
	NursesPerShift      int
	ObjectiveWeight     map[string]float64
}

type TransportConfig struct {
	Enabled        bool
	ForestTrees    int
	ScoreThreshold int
}

type BatchWriterConfig struct {
	BatchSize      int
	FlushIntervalMs int
	QueueSize      int
}

var AppConfig Config

func LoadConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	viper.SetDefault("server.port", "8080")
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", "5432")
	viper.SetDefault("database.user", "postgres")
	viper.SetDefault("database.password", "postgres")
	viper.SetDefault("database.dbname", "field_hospital")
	viper.SetDefault("database.sslmode", "disable")

	viper.SetDefault("mqtt.broker", "tcp://localhost:1883")
	viper.SetDefault("mqtt.clientid", "field-hospital-backend")
	viper.SetDefault("mqtt.qos", 1)
	viper.SetDefault("mqtt.cleansession", false)
	viper.SetDefault("mqtt.keepalive", 60)
	viper.SetDefault("mqtt.messagechanneldepth", 10000)
	viper.SetDefault("mqtt.decodeworkers", 4)
	viper.SetDefault("mqtt.decodequeuesize", 20000)

	viper.SetDefault("alert.sofathreshold", 2.0)
	viper.SetDefault("alert.infectionthreshold", 0.7)
	viper.SetDefault("alert.smsgateway", "http://localhost:9090/sms")
	viper.SetDefault("alert.deduplicationwindow", 5)

	viper.SetDefault("ml.lstmsequencelength", 60)
	viper.SetDefault("ml.modelupdateinterval", 300)
	viper.SetDefault("ml.numofbeds", 50)
	viper.SetDefault("ml.buffersize", 120)
	viper.SetDefault("ml.predictioninterval", 5)

	viper.SetDefault("ml.lstm.inputsize", 4)
	viper.SetDefault("ml.lstm.hiddensize", 32)
	viper.SetDefault("ml.lstm.outputsize", 1)
	viper.SetDefault("ml.lstm.mamlweight", 0.7)
	viper.SetDefault("ml.lstm.sofaweight", 0.3)
	viper.SetDefault("ml.lstm.fallbackbaserate", 0.15)
	viper.SetDefault("ml.lstm.lstmoutputweight", 0.05)
	viper.SetDefault("ml.lstm.lstmoutputbias", 0.5)

	viper.SetDefault("ml.maml.innerlr", 0.01)
	viper.SetDefault("ml.maml.outerlr", 0.001)
	viper.SetDefault("ml.maml.adaptsteps", 5)
	viper.SetDefault("ml.maml.stoploss", 0.01)
	viper.SetDefault("ml.maml.seqlength", 30)
	viper.SetDefault("ml.maml.minadaptseq", 10)

	viper.SetDefault("ml.randomforest.numtrees", 100)
	viper.SetDefault("ml.randomforest.treeweightmin", 0.5)
	viper.SetDefault("ml.randomforest.treeweightmax", 1.0)
	viper.SetDefault("ml.randomforest.creriskantibioticcoef", 0.03)
	viper.SetDefault("ml.randomforest.creriskinvasivecoef", 0.02)
	viper.SetDefault("ml.randomforest.crerisknoise", 0.05)
	viper.SetDefault("ml.randomforest.mrsariskantibioticcoef", 0.025)
	viper.SetDefault("ml.randomforest.mrsariskinvasivecoef", 0.025)
	viper.SetDefault("ml.randomforest.mrsarisknoise", 0.05)

	viper.SetDefault("ml.normalization.ecgmean", 75)
	viper.SetDefault("ml.normalization.ecgstd", 30)
	viper.SetDefault("ml.normalization.ventilatormean", 18)
	viper.SetDefault("ml.normalization.ventilatorstd", 10)
	viper.SetDefault("ml.normalization.spo2mean", 96)
	viper.SetDefault("ml.normalization.spo2std", 10)
	viper.SetDefault("ml.normalization.temperaturemean", 36.8)
	viper.SetDefault("ml.normalization.temperaturestd", 2)

	viper.SetDefault("batchwriter.batchsize", 500)
	viper.SetDefault("batchwriter.flushintervalms", 100)
	viper.SetDefault("batchwriter.queuesize", 50000)

	viper.SetDefault("coxvap.enabled", false)
	viper.SetDefault("coxvap.updateintervalsec", 21600)
	viper.SetDefault("coxvap.riskthreshold", 0.5)

	viper.SetDefault("gnn.enabled", false)
	viper.SetDefault("gnn.pythonserviceurl", "http://gnn-service:8000")
	viper.SetDefault("gnn.updateintervalsec", 3600)
	viper.SetDefault("gnn.maxnodes", 100)

	viper.SetDefault("optimizer.enabled", false)
	viper.SetDefault("optimizer.solveintervalsec", 7200)
	viper.SetDefault("optimizer.negativepressurebeds", 10)
	viper.SetDefault("optimizer.nursespershift", 8)

	viper.SetDefault("transport.enabled", false)
	viper.SetDefault("transport.foresttrees", 150)
	viper.SetDefault("transport.scorethreshold", 60)

	if err := viper.ReadInConfig(); err != nil {
		log.Printf("Warning: Config file not found, using defaults: %v", err)
	}

	if err := viper.Unmarshal(&AppConfig); err != nil {
		log.Fatalf("Unable to decode config: %v", err)
	}
}
