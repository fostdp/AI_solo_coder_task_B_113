package database

import (
	"context"
	"fmt"
	"log"
	"field-hospital-icu/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

var DB *pgxpool.Pool

func InitDB() {
	cfg := config.AppConfig.Database
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, cfg.SSLMode)

	var err error
	DB, err = pgxpool.New(context.Background(), connStr)
	if err != nil {
		log.Fatalf("无法连接数据库: %v", err)
	}

	if err = DB.Ping(context.Background()); err != nil {
		log.Fatalf("数据库Ping失败: %v", err)
	}
}

func CloseDB() {
	if DB != nil {
		DB.Close()
	}
}

func InitSchema() {
	schemaSQL := `
	CREATE EXTENSION IF NOT EXISTS timescaledb;

	CREATE TABLE IF NOT EXISTS beds (
		id SERIAL PRIMARY KEY,
		bed_code VARCHAR(20) UNIQUE NOT NULL,
		patient_name VARCHAR(100),
		patient_age INT,
		patient_gender VARCHAR(10),
		status VARCHAR(20) DEFAULT 'empty',
		admission_time TIMESTAMPTZ,
		location_x FLOAT,
		location_y FLOAT,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS vital_signs (
		time TIMESTAMPTZ NOT NULL,
		bed_id INT NOT NULL REFERENCES beds(id),
		sensor_type VARCHAR(30) NOT NULL,
		value FLOAT NOT NULL,
		unit VARCHAR(20)
	);

	SELECT create_hypertable('vital_signs', 'time', if_not_exists => TRUE);
	CREATE INDEX IF NOT EXISTS idx_vital_signs_bed_time ON vital_signs (bed_id, time DESC);
	CREATE INDEX IF NOT EXISTS idx_vital_signs_sensor ON vital_signs (sensor_type, time DESC);

	CREATE TABLE IF NOT EXISTS medical_records (
		id SERIAL PRIMARY KEY,
		bed_id INT NOT NULL REFERENCES beds(id),
		record_type VARCHAR(30) NOT NULL,
		record_data JSONB,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS infection_history (
		id SERIAL PRIMARY KEY,
		bed_id INT NOT NULL REFERENCES beds(id),
		antibiotic_type VARCHAR(100),
		dosage FLOAT,
		start_date TIMESTAMPTZ,
		end_date TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS invasive_procedures (
		id SERIAL PRIMARY KEY,
		bed_id INT NOT NULL REFERENCES beds(id),
		procedure_type VARCHAR(100),
		procedure_time TIMESTAMPTZ DEFAULT NOW(),
		notes TEXT
	);

	CREATE TABLE IF NOT EXISTS predictions (
		time TIMESTAMPTZ NOT NULL,
		bed_id INT NOT NULL REFERENCES beds(id),
		sepsis_risk FLOAT,
		sepsis_probability FLOAT,
		cre_risk FLOAT,
		mrsa_risk FLOAT,
		sofa_score FLOAT,
		PRIMARY KEY (time, bed_id)
	);

	SELECT create_hypertable('predictions', 'time', if_not_exists => TRUE);

	CREATE TABLE IF NOT EXISTS alerts (
		id SERIAL PRIMARY KEY,
		bed_id INT NOT NULL REFERENCES beds(id),
		alert_type VARCHAR(50) NOT NULL,
		severity VARCHAR(20) NOT NULL,
		message TEXT,
		trigger_value FLOAT,
		threshold FLOAT,
		acknowledged BOOLEAN DEFAULT FALSE,
		acknowledged_by VARCHAR(50),
		acknowledged_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_alerts_active ON alerts (acknowledged, created_at DESC);
	`

	_, err := DB.Exec(context.Background(), schemaSQL)
	if err != nil {
		log.Fatalf("Schema初始化失败: %v", err)
	}
}

func SeedBedData() {
	var count int
	err := DB.QueryRow(context.Background(), "SELECT COUNT(*) FROM beds").Scan(&count)
	if err != nil {
		log.Printf("查询床位数量失败: %v", err)
		return
	}

	if count >= 50 {
		log.Printf("已有 %d 条床位数据，跳过初始化", count)
		return
	}

	tx, err := DB.Begin(context.Background())
	if err != nil {
		log.Printf("开启事务失败: %v", err)
		return
	}
	defer tx.Rollback(context.Background())

	for i := 1; i <= 50; i++ {
		row := (i - 1) / 10
		col := (i - 1) % 10
		bedCode := fmt.Sprintf("ICU-%03d", i)

		_, err := tx.Exec(context.Background(),
			`INSERT INTO beds (bed_code, patient_name, patient_age, patient_gender, status, location_x, location_y, admission_time)
			 VALUES ($1, $2, $3, $4, 'occupied', $5, $6, NOW())
			 ON CONFLICT (bed_code) DO NOTHING`,
			bedCode,
			fmt.Sprintf("患者%d", i),
			30+i%40,
			[]string{"男", "女"}[i%2],
			float64(col)*100+50,
			float64(row)*100+50,
		)
		if err != nil {
			log.Printf("插入床位 %d 失败: %v", i, err)
		}
	}

	if err := tx.Commit(context.Background()); err != nil {
		log.Printf("提交事务失败: %v", err)
		return
	}

	log.Println("50张床位数据初始化完成")
}
