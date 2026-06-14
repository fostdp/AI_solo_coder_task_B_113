-- =============================================
-- 战地医院移动ICU系统 - TimescaleDB 初始化脚本
-- =============================================

-- 创建数据库（请先手动创建数据库后再执行此脚本）
-- CREATE DATABASE field_hospital;

-- 启用TimescaleDB扩展
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- =============================================
-- 床位表
-- =============================================
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

CREATE INDEX IF NOT EXISTS idx_beds_status ON beds(status);
CREATE INDEX IF NOT EXISTS idx_beds_code ON beds(bed_code);

-- =============================================
-- 生命体征时序表（核心传感器数据表）
-- =============================================
CREATE TABLE IF NOT EXISTS vital_signs (
    time TIMESTAMPTZ NOT NULL,
    bed_id INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    sensor_type VARCHAR(30) NOT NULL,
    value FLOAT NOT NULL,
    unit VARCHAR(20)
);

-- 创建超表（时序表）
SELECT create_hypertable('vital_signs', 'time', if_not_exists => TRUE);

-- 索引
CREATE INDEX IF NOT EXISTS idx_vital_signs_bed_time ON vital_signs (bed_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_vital_signs_sensor_time ON vital_signs (sensor_type, time DESC);
CREATE INDEX IF NOT EXISTS idx_vital_signs_bed_sensor ON vital_signs (bed_id, sensor_type, time DESC);

-- 自动保留策略（可选，保留最近30天数据）
-- SELECT add_retention_policy('vital_signs', INTERVAL '30 days');

-- 连续聚合：每5分钟聚合
CREATE MATERIALIZED VIEW IF NOT EXISTS vital_signs_5min
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('5 minutes', time) AS bucket,
    bed_id,
    sensor_type,
    AVG(value) AS avg_value,
    MIN(value) AS min_value,
    MAX(value) AS max_value,
    COUNT(*) AS sample_count
FROM vital_signs
GROUP BY bucket, bed_id, sensor_type
WITH NO DATA;

-- 刷新策略
SELECT add_continuous_aggregate_policy('vital_signs_5min',
    start_offset => INTERVAL '1 hour',
    end_offset => INTERVAL '5 minutes',
    schedule_interval => INTERVAL '5 minutes');

-- =============================================
-- 医疗记录表
-- =============================================
CREATE TABLE IF NOT EXISTS medical_records (
    id SERIAL PRIMARY KEY,
    bed_id INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    record_type VARCHAR(30) NOT NULL,
    record_data JSONB,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_medical_records_bed ON medical_records(bed_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_medical_records_type ON medical_records(record_type);

-- =============================================
-- 抗生素使用历史表
-- =============================================
CREATE TABLE IF NOT EXISTS infection_history (
    id SERIAL PRIMARY KEY,
    bed_id INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    antibiotic_type VARCHAR(100),
    dosage FLOAT,
    start_date TIMESTAMPTZ,
    end_date TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_infection_history_bed ON infection_history(bed_id, created_at DESC);

-- =============================================
-- 侵入性操作记录表
-- =============================================
CREATE TABLE IF NOT EXISTS invasive_procedures (
    id SERIAL PRIMARY KEY,
    bed_id INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    procedure_type VARCHAR(100),
    procedure_time TIMESTAMPTZ DEFAULT NOW(),
    notes TEXT
);

CREATE INDEX IF NOT EXISTS idx_invasive_procedures_bed ON invasive_procedures(bed_id, procedure_time DESC);

-- =============================================
-- 预测结果时序表（ML模型输出）
-- =============================================
CREATE TABLE IF NOT EXISTS predictions (
    time TIMESTAMPTZ NOT NULL,
    bed_id INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    sepsis_risk FLOAT,
    sepsis_probability FLOAT,
    cre_risk FLOAT,
    mrsa_risk FLOAT,
    sofa_score FLOAT,
    PRIMARY KEY (time, bed_id)
);

SELECT create_hypertable('predictions', 'time', if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_predictions_bed_time ON predictions (bed_id, time DESC);

-- =============================================
-- 告警表
-- =============================================
CREATE TABLE IF NOT EXISTS alerts (
    id SERIAL PRIMARY KEY,
    bed_id INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
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
CREATE INDEX IF NOT EXISTS idx_alerts_bed ON alerts (bed_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_severity ON alerts (severity, created_at DESC);

-- =============================================
-- 初始化50张床位数据
-- =============================================
DO $$
DECLARE
    row_idx INT;
    col_idx INT;
    i INT;
    v_bed_code VARCHAR(20);
    v_patient_name VARCHAR(100);
    v_gender VARCHAR(10);
    v_age INT;
BEGIN
    FOR i IN 1..50 LOOP
        row_idx := (i - 1) / 10;
        col_idx := (i - 1) % 10;
        v_bed_code := FORMAT('ICU-%03d', i);
        v_patient_name := FORMAT('患者%d', i);
        v_gender := CASE WHEN i % 2 = 0 THEN '女' ELSE '男' END;
        v_age := 30 + (i % 40);

        INSERT INTO beds (bed_code, patient_name, patient_age, patient_gender, status,
                          location_x, location_y, admission_time)
        VALUES (v_bed_code, v_patient_name, v_age, v_gender, 'occupied',
                col_idx::float * 100 + 50, row_idx::float * 100 + 50, NOW())
        ON CONFLICT (bed_code) DO NOTHING;
    END LOOP;
END $$;

-- =============================================
-- 初始化一些抗生素和侵入性操作记录（模拟数据）
-- =============================================
INSERT INTO infection_history (bed_id, antibiotic_type, dosage, start_date, end_date)
SELECT
    id,
    (ARRAY['美罗培南', '万古霉素', '头孢他啶', '哌拉西林他唑巴坦', '阿米卡星'])[1 + (id % 5)],
    1.0 + (id % 3) * 0.5,
    NOW() - (id % 7 || ' days')::INTERVAL,
    CASE WHEN id % 3 = 0 THEN NULL ELSE NOW() - (id % 2 || ' days')::INTERVAL END
FROM beds
WHERE id % 3 = 0
ON CONFLICT DO NOTHING;

INSERT INTO invasive_procedures (bed_id, procedure_type, notes)
SELECT
    id,
    (ARRAY['气管插管', '中心静脉置管', '动脉置管', '导尿管', '胸腔闭式引流'])[1 + (id % 5)],
    '常规操作'
FROM beds
WHERE id % 2 = 0
ON CONFLICT DO NOTHING;

-- =============================================
-- 视图：最新生命体征汇总
-- =============================================
CREATE OR REPLACE VIEW latest_vitals AS
SELECT DISTINCT ON (b.id, vs.sensor_type)
    b.id AS bed_id,
    b.bed_code,
    vs.sensor_type,
    vs.value,
    vs.unit,
    vs.time
FROM beds b
JOIN vital_signs vs ON vs.bed_id = b.id
WHERE vs.time > NOW() - INTERVAL '5 minutes'
ORDER BY b.id, vs.sensor_type, vs.time DESC;

-- =============================================
-- 视图：最新预测汇总
-- =============================================
CREATE OR REPLACE VIEW latest_predictions AS
SELECT DISTINCT ON (b.id)
    b.id AS bed_id,
    b.bed_code,
    p.sofa_score,
    p.sepsis_probability,
    p.cre_risk,
    p.mrsa_risk,
    p.time AS prediction_time
FROM beds b
LEFT JOIN predictions p ON p.bed_id = b.id
WHERE p.time IS NULL OR p.time > NOW() - INTERVAL '10 minutes'
ORDER BY b.id, p.time DESC;

-- =============================================
-- 视图：活动告警统计
-- =============================================
CREATE OR REPLACE VIEW active_alerts_summary AS
SELECT
    bed_id,
    COUNT(*) AS alert_count,
    COUNT(*) FILTER (WHERE severity = 'critical') AS critical_count,
    COUNT(*) FILTER (WHERE severity = 'high') AS high_count,
    COUNT(*) FILTER (WHERE severity = 'warning') AS warning_count
FROM alerts
WHERE acknowledged = FALSE
GROUP BY bed_id;
