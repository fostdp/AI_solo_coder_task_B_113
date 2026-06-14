-- =============================================
-- TimescaleDB 数据保留与压缩策略
-- 原始数据保留 30 天，降采样数据保留 1 年
-- =============================================

-- 启用时序表压缩 (原始数据)
ALTER TABLE vital_signs SET (
  timescaledb.compress,
  timescaledb.compress_segmentby = 'bed_id, sensor_type',
  timescaledb.compress_orderby = 'time DESC'
);

-- 创建压缩策略: 超过 7 天的原始数据自动压缩
SELECT add_compression_policy(
  'vital_signs',
  INTERVAL '7 days',
  if_not_exists => TRUE
);

-- 创建数据保留策略: 原始数据保留 30 天
SELECT add_retention_policy(
  'vital_signs',
  INTERVAL '30 days',
  if_not_exists => TRUE
);

-- =============================================
-- 降采样聚合 (1 分钟粒度, 保留 1 年)
-- =============================================

-- 创建降采样聚合视图 (连续聚合)
CREATE MATERIALIZED VIEW vital_signs_1min
WITH (timescaledb.continuous) AS
SELECT
  time_bucket('1 minute', time) AS bucket,
  bed_id,
  sensor_type,
  COUNT(*) AS sample_count,
  AVG(value) AS avg_value,
  MIN(value) AS min_value,
  MAX(value) AS max_value,
  STDDEV(value) AS std_value,
  FIRST(value, time) AS first_value,
  LAST(value, time) AS last_value
FROM vital_signs
GROUP BY bucket, bed_id, sensor_type
WITH NO DATA;

-- 启用连续聚合的压缩
ALTER MATERIALIZED VIEW vital_signs_1min SET (
  timescaledb.compress,
  timescaledb.compress_segmentby = 'bed_id, sensor_type',
  timescaledb.compress_orderby = 'bucket DESC'
);

-- 连续聚合的压缩策略
SELECT add_compression_policy(
  'vital_signs_1min',
  INTERVAL '1 day',
  if_not_exists => TRUE
);

-- 连续聚合的数据保留策略 (保留 1 年)
SELECT add_retention_policy(
  'vital_signs_1min',
  INTERVAL '365 days',
  if_not_exists => TRUE
);

-- 设置连续聚合的刷新策略
SELECT add_continuous_aggregate_policy(
  'vital_signs_1min',
  start_offset => INTERVAL '1 hour',
  end_offset => INTERVAL '1 minute',
  schedule_interval => INTERVAL '5 minutes',
  if_not_exists => TRUE
);

-- =============================================
-- 告警表保留策略 (保留 1 年)
-- =============================================

-- 告警表转换为超表 (如果尚未转换)
-- SELECT create_hypertable('alerts', 'created_at', if_not_exists => TRUE);

-- 告警表保留策略
SELECT add_retention_policy(
  'alerts',
  INTERVAL '365 days',
  if_not_exists => TRUE
);

-- =============================================
-- 信息查询
-- =============================================

-- 查看压缩策略
-- SELECT * FROM timescaledb_information.compression_settings;

-- 查看保留策略
-- SELECT * FROM timescaledb_information.retention_settings;

-- 查看连续聚合信息
-- SELECT * FROM timescaledb_information.continuous_aggregates;

-- 查看压缩统计
-- SELECT hypertable_name,
--        pg_size_pretty(before_compression_total_bytes) as before,
--        pg_size_pretty(after_compression_total_bytes) as after,
--        round(100 * (1 - after_compression_total_bytes::numeric / before_compression_total_bytes::numeric), 2) as compression_pct
-- FROM timescaledb_information.compressed_hypertable_stats;
