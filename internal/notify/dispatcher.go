package notify

import (
	"context"
	"log"
	"sync"
)

// Dispatcher is a fixed worker pool that sends SMS asynchronously.
type Dispatcher struct {
	gateway  SMSGateway
	jobs     chan smsJob
	workers  int
	wg       sync.WaitGroup
	stopOnce sync.Once
}

type smsJob struct {
	ctx    context.Context
	msisdn string
	body   string
}

// NewDispatcher constructs a worker pool for the given SMS gateway.
func NewDispatcher(gateway SMSGateway) *Dispatcher {
	return &Dispatcher{
		gateway: gateway,
		jobs:    make(chan smsJob, 64),
		workers: 3,
	}
}

// Start launches the worker goroutines.
func (d *Dispatcher) Start() {
	d.wg.Add(d.workers)
	for i := 0; i < d.workers; i++ {
		go d.worker()
	}
}

// Enqueue queues a send. It blocks until a worker slot or queue space is free.
func (d *Dispatcher) Enqueue(ctx context.Context, msisdn, body string) {
	d.jobs <- smsJob{ctx: ctx, msisdn: msisdn, body: body}
}

// Stop closes the queue and waits for all accepted jobs to finish.
func (d *Dispatcher) Stop() {
	d.stopOnce.Do(func() {
		close(d.jobs)
		d.wg.Wait()
	})
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for job := range d.jobs {
		d.dispatch(job)
	}
}

func (d *Dispatcher) dispatch(job smsJob) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("notify: recovered panic sending to %s: %v", job.msisdn, recovered)
		}
	}()

	if err := d.gateway.Send(job.ctx, job.msisdn, job.body); err != nil {
		log.Printf("notify: send to %s: %v", job.msisdn, err)
	}
}
