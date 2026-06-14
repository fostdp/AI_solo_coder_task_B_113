package main

import (
	"sync"
	"sync/atomic"
)

type GoroutinePool struct {
	size       int
	taskCh     chan func()
	wg         sync.WaitGroup
	running    int64
	completed  int64
	stopCh     chan struct{}
	stopOnce   sync.Once
}

func NewGoroutinePool(size int) *GoroutinePool {
	if size <= 0 {
		size = 4
	}
	p := &GoroutinePool{
		size:   size,
		taskCh: make(chan func(), 1000),
		stopCh: make(chan struct{}),
	}
	for i := 0; i < size; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
	return p
}

func (p *GoroutinePool) worker(id int) {
	defer p.wg.Done()
	for {
		select {
		case task, ok := <-p.taskCh:
			if !ok {
				return
			}
			atomic.AddInt64(&p.running, 1)
			task()
			atomic.AddInt64(&p.running, -1)
			atomic.AddInt64(&p.completed, 1)
		case <-p.stopCh:
			return
		}
	}
}

func (p *GoroutinePool) Submit(task func()) error {
	select {
	case p.taskCh <- task:
		return nil
	case <-p.stopCh:
		return ErrPoolStopped
	}
}

func (p *GoroutinePool) Active() int {
	return int(atomic.LoadInt64(&p.running))
}

func (p *GoroutinePool) Completed() int64 {
	return atomic.LoadInt64(&p.completed)
}

func (p *GoroutinePool) Size() int {
	return p.size
}

func (p *GoroutinePool) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
		close(p.taskCh)
	})
	p.wg.Wait()
}

var ErrPoolStopped = &PoolError{msg: "goroutine pool已停止"}

type PoolError struct{ msg string }

func (e *PoolError) Error() string { return e.msg }
