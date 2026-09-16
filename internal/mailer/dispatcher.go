package mailer

import (
	"context"
	"log"
	"sync"
)

// Dispatcher is a fixed worker pool that sends mail asynchronously. Stop drains
// the queue by closing it and waiting for workers to finish the jobs already
// accepted; it must be called during graceful shutdown after no more Enqueue
// calls will be made.
type Dispatcher struct {
	mailer   Mailer
	jobs     chan mailJob
	workers  int
	wg       sync.WaitGroup
	stopOnce sync.Once
}

type mailJob struct {
	ctx          context.Context
	recipient    string
	templateFile string
	data         any
}

// NewDispatcher constructs a worker pool for mailer with a small fixed number
// of workers and a bounded queue.
func NewDispatcher(mailer Mailer) *Dispatcher {
	return &Dispatcher{
		mailer:  mailer,
		jobs:    make(chan mailJob, 64),
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
func (d *Dispatcher) Enqueue(ctx context.Context, recipient, templateFile string, data any) {
	d.jobs <- mailJob{
		ctx:          ctx,
		recipient:    recipient,
		templateFile: templateFile,
		data:         data,
	}
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

func (d *Dispatcher) dispatch(job mailJob) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("mailer: recovered panic sending %q: %v", job.templateFile, recovered)
		}
	}()

	if err := d.mailer.Send(job.ctx, job.recipient, job.templateFile, job.data); err != nil {
		log.Printf("mailer: send %q to %s: %v", job.templateFile, job.recipient, err)
	}
}
