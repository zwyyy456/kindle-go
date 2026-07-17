package task

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/store"
)

type executorFunc func(context.Context, Task, ProgressReporter) error

func (f executorFunc) Execute(ctx context.Context, value Task, progress ProgressReporter) error {
	return f(ctx, value, progress)
}

func TestRunnerUsesOnePersistentFIFOWithIndependentExecutionSlots(t *testing.T) {
	service, bookID, inputID := newTaskTestService(t)
	generationStarted := make(chan string, 2)
	proofreadStarted := make(chan string, 1)
	releaseGeneration := make(chan struct{})
	releaseProofread := make(chan struct{})
	executors := map[Type]Executor{
		GenerateEPUB: executorFunc(func(ctx context.Context, value Task, _ ProgressReporter) error {
			generationStarted <- value.ID
			select {
			case <-releaseGeneration:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}),
		Proofread: executorFunc(func(ctx context.Context, value Task, _ ProgressReporter) error {
			proofreadStarted <- value.ID
			select {
			case <-releaseProofread:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}),
	}
	first, _ := service.Create(context.Background(), CreateRequest{BookID: bookID, Type: GenerateEPUB, InputFileID: inputID})
	second, _ := service.Create(context.Background(), CreateRequest{BookID: bookID, Type: GenerateEPUB, InputFileID: inputID})
	proofread, _ := service.Create(context.Background(), CreateRequest{BookID: bookID, Type: Proofread, InputFileID: inputID})

	cancelRunner, runnerDone := startRunner(t, service, executors)
	defer cancelRunner()
	if got := receive(t, generationStarted); got != first.ID {
		t.Fatalf("first generation task = %s, want %s", got, first.ID)
	}
	if got := receive(t, proofreadStarted); got != proofread.ID {
		t.Fatalf("proofread task = %s, want %s", got, proofread.ID)
	}
	select {
	case id := <-generationStarted:
		t.Fatalf("second generation started before first finished: %s", id)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseGeneration)
	if got := receive(t, generationStarted); got != second.ID {
		t.Fatalf("second generation task = %s, want %s", got, second.ID)
	}
	close(releaseProofread)
	waitStatus(t, service, first.ID, Completed)
	waitStatus(t, service, second.ID, Completed)
	waitStatus(t, service, proofread.ID, Completed)
	cancelRunner()
	waitRunner(t, runnerDone)
}

func TestCancelQueuedAndRunningAndRetryFromBeginning(t *testing.T) {
	service, bookID, inputID := newTaskTestService(t)
	queued, err := service.Create(context.Background(), CreateRequest{BookID: bookID, Type: GenerateEPUB, InputFileID: inputID, ParametersJSON: `{"title":"snapshot"}`})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Cancel(context.Background(), queued.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, service, queued.ID, Canceled)
	retry, err := service.Retry(context.Background(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID == queued.ID || retry.RetryOfTaskID != queued.ID || retry.ParametersJSON != queued.ParametersJSON || retry.Status != Queued {
		t.Fatalf("retry = %#v", retry)
	}

	started := make(chan struct{})
	executor := executorFunc(func(ctx context.Context, _ Task, _ ProgressReporter) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	cancelRunner, runnerDone := startRunner(t, service, map[Type]Executor{GenerateEPUB: executor})
	defer cancelRunner()
	<-started
	if err := service.Cancel(context.Background(), retry.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, service, retry.ID, Canceled)
	cancelRunner()
	waitRunner(t, runnerDone)
}

func TestRunnerTurnsExecutorErrorAndPanicIntoFailedTasks(t *testing.T) {
	service, bookID, inputID := newTaskTestService(t)
	failedTask, _ := service.Create(context.Background(), CreateRequest{BookID: bookID, Type: GenerateEPUB, InputFileID: inputID})
	panicTask, _ := service.Create(context.Background(), CreateRequest{BookID: bookID, Type: GenerateAZW3, InputFileID: inputID})
	executors := map[Type]Executor{
		GenerateEPUB: executorFunc(func(context.Context, Task, ProgressReporter) error {
			return &ExecutionError{Code: "conversion_failed", Message: "bad input"}
		}),
		GenerateAZW3: executorFunc(func(context.Context, Task, ProgressReporter) error {
			panic("boom")
		}),
	}
	cancelRunner, runnerDone := startRunner(t, service, executors)
	waitStatus(t, service, failedTask.ID, Failed)
	waitStatus(t, service, panicTask.ID, Failed)
	failed, _, _ := service.Get(context.Background(), failedTask.ID)
	panicked, _, _ := service.Get(context.Background(), panicTask.ID)
	if failed.ErrorCode != "conversion_failed" || failed.ErrorMessage != "bad input" {
		t.Fatalf("failed task = %#v", failed)
	}
	if panicked.ErrorCode != "executor_panic" || !strings.Contains(panicked.ErrorMessage, "boom") {
		t.Fatalf("panic task = %#v", panicked)
	}
	cancelRunner()
	waitRunner(t, runnerDone)
}

func TestRunnerRecoversInterruptedTaskAndKeepsQueuedTask(t *testing.T) {
	service, bookID, inputID := newTaskTestService(t)
	interrupted, _ := service.Create(context.Background(), CreateRequest{BookID: bookID, Type: GenerateEPUB, InputFileID: inputID})
	queued, _ := service.Create(context.Background(), CreateRequest{BookID: bookID, Type: GenerateEPUB, InputFileID: inputID})
	if claimed, ok, err := service.store.ClaimNextTask(context.Background(), []string{string(GenerateEPUB)}, time.Now()); err != nil || !ok || claimed.ID != interrupted.ID {
		t.Fatalf("claimed = %#v, %v, %v", claimed, ok, err)
	}
	release := make(chan struct{})
	executor := executorFunc(func(ctx context.Context, _ Task, _ ProgressReporter) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	cancelRunner, runnerDone := startRunner(t, service, map[Type]Executor{GenerateEPUB: executor})
	waitStatus(t, service, interrupted.ID, Failed)
	recovered, _, _ := service.Get(context.Background(), interrupted.ID)
	if recovered.ErrorCode != "task_process_interrupted" {
		t.Fatalf("recovered task = %#v", recovered)
	}
	waitStatus(t, service, queued.ID, Running)
	close(release)
	waitStatus(t, service, queued.ID, Completed)
	cancelRunner()
	waitRunner(t, runnerDone)
}

func TestCompletedTaskWinsBeforeLateCancel(t *testing.T) {
	service, bookID, inputID := newTaskTestService(t)
	value, _ := service.Create(context.Background(), CreateRequest{BookID: bookID, Type: GenerateEPUB, InputFileID: inputID})
	executor := executorFunc(func(context.Context, Task, ProgressReporter) error { return nil })
	cancelRunner, runnerDone := startRunner(t, service, map[Type]Executor{GenerateEPUB: executor})
	waitStatus(t, service, value.ID, Completed)
	var conflict *ConflictError
	if err := service.Cancel(context.Background(), value.ID); !errors.As(err, &conflict) {
		t.Fatalf("late cancel error = %v", err)
	}
	cancelRunner()
	waitRunner(t, runnerDone)
}

func TestCreateManyUsesOneGlobalQueueSequence(t *testing.T) {
	service, bookID, inputID := newTaskTestService(t)
	tasks, err := service.CreateMany(context.Background(), []CreateRequest{
		{BookID: bookID, Type: GenerateEPUB, InputFileID: inputID},
		{BookID: bookID, Type: Proofread, InputFileID: inputID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[1].QueueSeq != tasks[0].QueueSeq+1 {
		t.Fatalf("tasks = %#v", tasks)
	}
}

func TestOnlyOneRunnerCanOwnLibrary(t *testing.T) {
	service, _, _ := newTaskTestService(t)
	first := NewRunner(service, nil)
	second := NewRunner(service, nil)
	if err := first.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer first.release(first.instanceID)
	err := second.Prepare(context.Background())
	var held *store.RuntimeLockHeldError
	if !errors.As(err, &held) || held.Lock.PID == 0 {
		t.Fatalf("second runner prepare error = %v", err)
	}
}

func newTaskTestService(t *testing.T) (*Service, string, string) {
	t.Helper()
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	book, err := storage.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("source"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return NewService(storage), book.ID, book.Original.ID
}

func startRunner(t *testing.T, service *Service, executors map[Type]Executor) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NewRunner(service, executors).Run(ctx) }()
	return cancel, done
}

func waitRunner(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func waitStatus(t *testing.T, service *Service, id string, want Status) Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		value, ok, err := service.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if ok && value.Status == want {
			return value
		}
		time.Sleep(10 * time.Millisecond)
	}
	value, _, _ := service.Get(context.Background(), id)
	t.Fatalf("task status = %s, want %s: %#v", value.Status, want, value)
	return Task{}
}

func receive(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for executor")
		return ""
	}
}
