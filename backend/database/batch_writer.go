package database

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
	"field-hospital-icu/config"
	"field-hospital-icu/models"
)

const (
	DefaultBatchSize     = 500
	DefaultFlushInterval = 100 * time.Millisecond
	DefaultQueueSize     = 50000
)

type BatchWriter struct {
	batchSize     int
	flushInterval time.Duration
	queue         chan models.VitalSign
	buffer        []models.VitalSign
	mux           sync.Mutex
	stopChan      chan struct{}
	wg            sync.WaitGroup
	running       bool

	totalInserted  uint64
	totalDropped   uint64
	flushCount     uint64
	maxBatchLatency int64
}

var VitalWriter *BatchWriter

func InitBatchWriter() {
	VitalWriter = NewBatchWriter(DefaultBatchSize, DefaultFlushInterval, DefaultQueueSize)
	VitalWriter.Start()
	log.Println("批量写入器初始化完成，批次大小:", DefaultBatchSize)
}

func InitBatchWriterFromConfig(cfg config.BatchWriterConfig) {
	VitalWriter = NewBatchWriter(
		cfg.BatchSize,
		time.Duration(cfg.FlushIntervalMs)*time.Millisecond,
		cfg.QueueSize,
	)
	VitalWriter.Start()
	log.Printf("批量写入器初始化完成，批次大小: %d, 刷盘间隔: %dms, 队列: %d",
		cfg.BatchSize, cfg.FlushIntervalMs, cfg.QueueSize)
}

func NewBatchWriter(batchSize int, flushInterval time.Duration, queueSize int) *BatchWriter {
	return &BatchWriter{
		batchSize:     batchSize,
		flushInterval: flushInterval,
		queue:         make(chan models.VitalSign, queueSize),
		buffer:        make([]models.VitalSign, 0, batchSize),
		stopChan:      make(chan struct{}),
	}
}

func (bw *BatchWriter) Start() {
	bw.mux.Lock()
	defer bw.mux.Unlock()

	if bw.running {
		return
	}

	bw.running = true
	bw.wg.Add(2)
	go bw.collectWorker()
	go bw.flushWorker()
}

func (bw *BatchWriter) Stop() {
	bw.mux.Lock()
	if !bw.running {
		bw.mux.Unlock()
		return
	}
	bw.running = false
	close(bw.stopChan)
	bw.mux.Unlock()

	bw.wg.Wait()
	bw.flushBuffer()
}

func (bw *BatchWriter) Write(v models.VitalSign) bool {
	select {
	case bw.queue <- v:
		return true
	default:
		atomic.AddUint64(&bw.totalDropped, 1)
		return false
	}
}

func (bw *BatchWriter) collectWorker() {
	defer bw.wg.Done()

	for {
		select {
		case <-bw.stopChan:
			return
		case v := <-bw.queue:
			bw.mux.Lock()
			bw.buffer = append(bw.buffer, v)

			if len(bw.buffer) >= bw.batchSize {
				buf := bw.buffer
				bw.buffer = make([]models.VitalSign, 0, bw.batchSize)
				bw.mux.Unlock()
				bw.executeFlush(buf)
			} else {
				bw.mux.Unlock()
			}
		}
	}
}

func (bw *BatchWriter) flushWorker() {
	defer bw.wg.Done()
	ticker := time.NewTicker(bw.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bw.stopChan:
			return
		case <-ticker.C:
			bw.mux.Lock()
			if len(bw.buffer) > 0 {
				buf := bw.buffer
				bw.buffer = make([]models.VitalSign, 0, bw.batchSize)
				bw.mux.Unlock()
				bw.executeFlush(buf)
			} else {
				bw.mux.Unlock()
			}
		}
	}
}

func (bw *BatchWriter) executeFlush(batch []models.VitalSign) {
	if len(batch) == 0 {
		return
	}

	startTime := time.Now()

	ctx := context.Background()
	tx, err := DB.Begin(ctx)
	if err != nil {
		log.Printf("批量写入开启事务失败: %v", err)
		return
	}
	defer tx.Rollback(ctx)

	stmt := `INSERT INTO vital_signs (time, bed_id, sensor_type, value, unit) VALUES ($1, $2, $3, $4, $5)`

	for _, v := range batch {
		_, err := tx.Exec(ctx, stmt, v.Time, v.BedID, v.SensorType, v.Value, v.Unit)
		if err != nil {
			log.Printf("批量写入插入失败: %v", err)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("批量写入提交失败: %v", err)
		return
	}

	latency := time.Since(startTime).Milliseconds()
	atomic.AddUint64(&bw.totalInserted, uint64(len(batch)))
	atomic.AddUint64(&bw.flushCount, 1)
	atomic.CompareAndSwapInt64(&bw.maxBatchLatency, 0, latency)
	for {
		old := atomic.LoadInt64(&bw.maxBatchLatency)
		if latency <= old {
			break
		}
		if atomic.CompareAndSwapInt64(&bw.maxBatchLatency, old, latency) {
			break
		}
	}
}

func (bw *BatchWriter) flushBuffer() {
	bw.mux.Lock()
	buf := bw.buffer
	bw.buffer = make([]models.VitalSign, 0, bw.batchSize)
	bw.mux.Unlock()

	if len(buf) > 0 {
		bw.executeFlush(buf)
	}
}

func (bw *BatchWriter) Stats() BatchStats {
	return BatchStats{
		TotalInserted:  atomic.LoadUint64(&bw.totalInserted),
		TotalDropped:   atomic.LoadUint64(&bw.totalDropped),
		FlushCount:     atomic.LoadUint64(&bw.flushCount),
		MaxBatchLatency: atomic.LoadInt64(&bw.maxBatchLatency),
		QueueLength:    len(bw.queue),
	}
}

type BatchStats struct {
	TotalInserted   uint64
	TotalDropped    uint64
	FlushCount      uint64
	MaxBatchLatency int64
	QueueLength     int
}

func WriteVitalSign(v models.VitalSign) bool {
	if VitalWriter != nil {
		return VitalWriter.Write(v)
	}
	return false
}

func GetWriterStats() BatchStats {
	if VitalWriter != nil {
		return VitalWriter.Stats()
	}
	return BatchStats{}
}
