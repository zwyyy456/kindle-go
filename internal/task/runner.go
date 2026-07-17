package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/store"
)

type Executor interface {
	Execute(ctx context.Context, task Task, progress ProgressReporter) error
}

type ProgressReporter interface {
	Report(ctx context.Context, stage string, current, total int) error
}

type Runner struct {
	service    *Service
	executors  map[Type]Executor
	poll       time.Duration
	heartbeat  time.Duration
	staleAfter time.Duration

	prepareMu  sync.Mutex
	instanceID string
}

func NewRunner(service *Service, executors map[Type]Executor) *Runner {
	return &Runner{service: service, executors: executors, poll: 100 * time.Millisecond, heartbeat: 5 * time.Second, staleAfter: 15 * time.Second}
}

func (r *Runner) Run(ctx context.Context) error {
	if err := r.Prepare(ctx); err != nil {
		return err
	}
	r.prepareMu.Lock()
	instanceID := r.instanceID
	r.prepareMu.Unlock()
	defer r.release(instanceID)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workersDone := make(chan error, 1)
	go func() { workersDone <- r.runWorkers(runCtx) }()
	heartbeatErr := make(chan error, 1)
	go r.heartbeatLoop(runCtx, instanceID, heartbeatErr)
	select {
	case <-ctx.Done():
		cancel()
		return <-workersDone
	case err := <-heartbeatErr:
		cancel()
		<-workersDone
		return err
	case err := <-workersDone:
		cancel()
		return err
	}
}

func (r *Runner) Prepare(ctx context.Context) error {
	r.prepareMu.Lock()
	defer r.prepareMu.Unlock()
	if r.instanceID != "" {
		return nil
	}
	instanceID, err := store.NewID()
	if err != nil {
		return err
	}
	if err := r.service.store.ClaimRuntimeLock(ctx, instanceID, os.Getpid(), r.service.now(), r.staleAfter); err != nil {
		return err
	}
	if err := r.service.store.Initialize(ctx); err != nil {
		_ = r.service.store.ReleaseRuntimeLock(context.Background(), instanceID)
		return err
	}
	if _, err := r.service.store.RecoverRunningTasks(ctx, r.service.now()); err != nil {
		_ = r.service.store.ReleaseRuntimeLock(context.Background(), instanceID)
		return err
	}
	r.instanceID = instanceID
	return nil
}

func (r *Runner) release(instanceID string) {
	_ = r.service.store.ReleaseRuntimeLock(context.Background(), instanceID)
	r.prepareMu.Lock()
	if r.instanceID == instanceID {
		r.instanceID = ""
	}
	r.prepareMu.Unlock()
}

func (r *Runner) runWorkers(ctx context.Context) error {
	errCh := make(chan error, 2)
	go func() {
		errCh <- r.worker(ctx, []Type{GenerateEPUB, GenerateAZW3, BuildRevisionTXT, BuildRevisionEPUB})
	}()
	go func() { errCh <- r.worker(ctx, []Type{Proofread}) }()
	for completed := 0; completed < 2; completed++ {
		if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}

func (r *Runner) heartbeatLoop(ctx context.Context, instanceID string, result chan<- error) {
	ticker := time.NewTicker(r.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := r.service.store.HeartbeatRuntimeLock(ctx, instanceID, now); err != nil {
				result <- err
				return
			}
		}
	}
}

func (r *Runner) worker(ctx context.Context, types []Type) error {
	storeTypes := make([]string, len(types))
	for index, value := range types {
		storeTypes[index] = string(value)
	}
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		record, ok, err := r.service.store.ClaimNextTask(ctx, storeTypes, r.service.now())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if ok {
			r.execute(ctx, taskFromStore(record))
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-r.service.wake:
		case <-ticker.C:
		}
	}
}

func (r *Runner) execute(parent context.Context, value Task) {
	ctx, cancel := context.WithCancel(parent)
	r.service.register(value.ID, cancel)
	defer func() {
		cancel()
		r.service.unregister(value.ID)
	}()
	current, ok, err := r.service.Get(context.Background(), value.ID)
	if err != nil || !ok {
		return
	}
	if current.Status == Canceled {
		cancel()
		return
	}
	executor := r.executors[value.Type]
	if executor == nil {
		_, _ = r.service.store.FinishTask(context.Background(), value.ID, string(Failed), "executor_not_found", fmt.Sprintf("no executor registered for %s", value.Type), r.service.now())
		return
	}
	reporter := progressReporter{store: r.service.store, taskID: value.ID}
	err = executeSafely(ctx, executor, value, reporter)
	latest, ok, getErr := r.service.Get(context.Background(), value.ID)
	if getErr != nil || !ok || latest.Status == Canceled {
		return
	}
	if parent.Err() != nil {
		return
	}
	if err == nil {
		_, _ = r.service.store.FinishTask(context.Background(), value.ID, string(Completed), "", "", r.service.now())
		return
	}
	code := "executor_failed"
	message := err.Error()
	var executionErr *ExecutionError
	if errors.As(err, &executionErr) {
		if executionErr.Code != "" {
			code = executionErr.Code
		}
	}
	_, _ = r.service.store.FinishTask(context.Background(), value.ID, string(Failed), code, message, r.service.now())
}

func executeSafely(ctx context.Context, executor Executor, value Task, reporter ProgressReporter) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &ExecutionError{Code: "executor_panic", Message: fmt.Sprintf("executor panic: %v\n%s", recovered, debug.Stack())}
		}
	}()
	return executor.Execute(ctx, value, reporter)
}

type progressReporter struct {
	store interface {
		UpdateTaskProgress(context.Context, string, string, int, int) error
	}
	taskID string
}

func (r progressReporter) Report(ctx context.Context, stage string, current, total int) error {
	return r.store.UpdateTaskProgress(ctx, r.taskID, stage, current, total)
}
