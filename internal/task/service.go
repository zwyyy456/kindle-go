package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/store"
)

type Service struct {
	store *store.Store
	now   func() time.Time
	wake  chan struct{}

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func NewService(storage *store.Store) *Service {
	return &Service{store: storage, now: time.Now, wake: make(chan struct{}, 2), running: make(map[string]context.CancelFunc)}
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (Task, error) {
	record, err := s.store.CreateTask(ctx, store.CreateTaskParams{BookID: req.BookID, Type: string(req.Type), InputFileID: req.InputFileID, ParametersJSON: req.ParametersJSON, CreatedAt: req.CreatedAt})
	if err != nil {
		return Task{}, err
	}
	s.notify()
	return taskFromStore(record), nil
}

func (s *Service) CreateMany(ctx context.Context, requests []CreateRequest) ([]Task, error) {
	params := make([]store.CreateTaskParams, 0, len(requests))
	for _, req := range requests {
		params = append(params, store.CreateTaskParams{BookID: req.BookID, Type: string(req.Type), InputFileID: req.InputFileID, ParametersJSON: req.ParametersJSON, CreatedAt: req.CreatedAt})
	}
	records, err := s.store.CreateTasks(ctx, params)
	if err != nil {
		return nil, err
	}
	tasks := tasksFromStore(records)
	if len(tasks) != 0 {
		s.notify()
	}
	return tasks, nil
}

func (s *Service) Get(ctx context.Context, id string) (Task, bool, error) {
	record, ok, err := s.store.Task(ctx, id)
	return taskFromStore(record), ok, err
}

func (s *Service) List(ctx context.Context, bookID string) ([]Task, error) {
	records, err := s.store.ListTasks(ctx, bookID)
	return tasksFromStore(records), err
}

func (s *Service) Events(ctx context.Context, taskID string) ([]Event, error) {
	records, err := s.store.TaskEvents(ctx, taskID)
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(records))
	for _, record := range records {
		events = append(events, Event{
			TaskID: record.TaskID, Seq: record.Seq, Level: record.Level, Stage: record.Stage,
			Message: record.Message, CreatedAt: record.CreatedAt,
		})
	}
	return events, nil
}

func (s *Service) Cancel(ctx context.Context, id string) error {
	previous, changed, err := s.store.CancelTask(ctx, id, s.now())
	if err != nil {
		return err
	}
	if !changed {
		if previous == "" {
			return fmt.Errorf("task %q not found", id)
		}
		return &ConflictError{Message: fmt.Sprintf("task %q is already %s", id, previous)}
	}
	if previous == string(Running) {
		s.mu.Lock()
		cancel := s.running[id]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return nil
}

func (s *Service) Retry(ctx context.Context, id string) (Task, error) {
	record, err := s.store.RetryTask(ctx, id, s.now())
	if err != nil {
		return Task{}, err
	}
	s.notify()
	return taskFromStore(record), nil
}

func (s *Service) notify() {
	for index := 0; index < cap(s.wake); index++ {
		select {
		case s.wake <- struct{}{}:
		default:
			return
		}
	}
}

func (s *Service) register(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	s.running[id] = cancel
	s.mu.Unlock()
}

func (s *Service) unregister(id string) {
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
}

func taskFromStore(record store.TaskRecord) Task {
	return Task{
		ID: record.ID, BookID: record.BookID, Type: Type(record.Type), Status: Status(record.Status), QueueSeq: record.QueueSeq,
		InputFileID: record.InputFileID, RetryOfTaskID: record.RetryOfTaskID, ParametersJSON: record.ParametersJSON,
		Stage: record.Stage, ProgressCurrent: record.ProgressCurrent, ProgressTotal: record.ProgressTotal,
		ErrorCode: record.ErrorCode, ErrorMessage: record.ErrorMessage,
		CreatedAt: record.CreatedAt, StartedAt: record.StartedAt, FinishedAt: record.FinishedAt,
	}
}

func tasksFromStore(records []store.TaskRecord) []Task {
	tasks := make([]Task, 0, len(records))
	for _, record := range records {
		tasks = append(tasks, taskFromStore(record))
	}
	return tasks
}
