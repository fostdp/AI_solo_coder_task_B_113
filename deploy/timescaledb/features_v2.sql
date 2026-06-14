-- =============================================
-- 战地医院移动ICU系统 - 高级功能扩展迁移脚本 (V2)
-- 包含: VAP风险预测、GNN耐药传播、优化调度、转运风险评估
-- =============================================

-- =============================================
-- 呼吸机参数时序表 (VAP风险预测输入)
-- =============================================
CREATE TABLE IF NOT EXISTS ventilator_params (
    time TIMESTAMPTZ NOT NULL,
    bed_id INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    peak_pressure FLOAT,
    tidal_volume FLOAT,
    oral_secretion_grade FLOAT,
    ventilator_hours INT
);

SELECT create_hypertable('ventilator_params', 'time', if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_ventilator_params_bed_time ON ventilator_params (bed_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_ventilator_params_bed_sensor ON ventilator_params (bed_id, peak_pressure, time DESC);

-- =============================================
-- VAP风险预测结果时序表 (Cox模型输出)
-- =============================================
CREATE TABLE IF NOT EXISTS vap_risk_records (
    id SERIAL,
    time TIMESTAMPTZ NOT NULL,
    bed_id INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    risk_probability FLOAT,
    hazards_ratio FLOAT,
    predicted_onset_hours FLOAT,
    feature_weights JSONB,
    PRIMARY KEY (time, id)
);

SELECT create_hypertable('vap_risk_records', 'time', if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_vap_risk_records_bed_time ON vap_risk_records (bed_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_vap_risk_records_risk ON vap_risk_records (risk_probability DESC, time DESC);

-- =============================================
-- 细菌培养结果表 (普通表)
-- =============================================
CREATE TABLE IF NOT EXISTS culture_results (
    id SERIAL PRIMARY KEY,
    bed_id INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    bacteria_name VARCHAR(100) NOT NULL,
    resistance_genes TEXT,
    antibiotic_sensitivity JSONB,
    time TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_culture_results_bed ON culture_results (bed_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_culture_results_bacteria ON culture_results (bacteria_name, time DESC);
CREATE INDEX IF NOT EXISTS idx_culture_results_time ON culture_results (time DESC);

-- =============================================
-- GNN耐药传播预测结果时序表
-- =============================================
CREATE TABLE IF NOT EXISTS resistance_predictions (
    id SERIAL,
    time TIMESTAMPTZ NOT NULL,
    bed_id INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    bacteria_name VARCHAR(100) NOT NULL,
    gene_spread_prob FLOAT,
    spread_path INTEGER[],
    edge_weights FLOAT[],
    PRIMARY KEY (time, id)
);

SELECT create_hypertable('resistance_predictions', 'time', if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_resistance_predictions_bed_time ON resistance_predictions (bed_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_resistance_predictions_bacteria ON resistance_predictions (bacteria_name, time DESC);
CREATE INDEX IF NOT EXISTS idx_resistance_predictions_prob ON resistance_predictions (gene_spread_prob DESC, time DESC);

-- =============================================
-- 优化调度求解结果表 (普通表)
-- =============================================
CREATE TABLE IF NOT EXISTS optimizer_solutions (
    id SERIAL PRIMARY KEY,
    solve_time_ms INT,
    negative_pressure_assign JSONB,
    nurse_schedule JSONB,
    unmet_needs TEXT[],
    cost FLOAT,
    status VARCHAR(20) DEFAULT 'pending',
    time TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_optimizer_solutions_status ON optimizer_solutions (status, time DESC);
CREATE INDEX IF NOT EXISTS idx_optimizer_solutions_cost ON optimizer_solutions (cost, time DESC);
CREATE INDEX IF NOT EXISTS idx_optimizer_solutions_time ON optimizer_solutions (time DESC);

-- =============================================
-- 转运请求表 (普通表)
-- =============================================
CREATE TABLE IF NOT EXISTS transport_requests (
    id SERIAL PRIMARY KEY,
    from_bed INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    to_bed INT NOT NULL REFERENCES beds(id) ON DELETE CASCADE,
    distance_meters INT,
    priority INT DEFAULT 0,
    urgent BOOLEAN DEFAULT FALSE,
    time TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_transport_requests_from ON transport_requests (from_bed, time DESC);
CREATE INDEX IF NOT EXISTS idx_transport_requests_to ON transport_requests (to_bed, time DESC);
CREATE INDEX IF NOT EXISTS idx_transport_requests_priority ON transport_requests (priority DESC, urgent DESC, time DESC);
CREATE INDEX IF NOT EXISTS idx_transport_requests_urgent ON transport_requests (urgent, time DESC);

-- =============================================
-- 转运风险评估结果表 (普通表)
-- =============================================
CREATE TABLE IF NOT EXISTS transport_risk_results (
    id SERIAL PRIMARY KEY,
    request_id INT NOT NULL REFERENCES transport_requests(id) ON DELETE CASCADE,
    risk_score INT,
    risk_level VARCHAR(20),
    adverse_event_prob FLOAT,
    feature_contrib JSONB,
    recommendations TEXT[],
    time TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_transport_risk_results_request ON transport_risk_results (request_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_transport_risk_results_level ON transport_risk_results (risk_level, time DESC);
CREATE INDEX IF NOT EXISTS idx_transport_risk_results_score ON transport_risk_results (risk_score DESC, time DESC);
CREATE INDEX IF NOT EXISTS idx_transport_risk_results_time ON transport_risk_results (time DESC);
