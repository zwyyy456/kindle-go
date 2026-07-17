package task

import "time"

type Type string

const (
	Proofread         Type = "proofread"
	BuildRevisionTXT  Type = "build_revision_txt"
	BuildRevisionEPUB Type = "build_revision_epub"
	GenerateEPUB      Type = "generate_epub"
	GenerateAZW3      Type = "generate_azw3"
)

type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Completed Status = "completed"
	Failed    Status = "failed"
	Canceled  Status = "canceled"
)

type Task struct {
	ID              string
	BookID          string
	Type            Type
	Status          Status
	QueueSeq        int64
	InputFileID     string
	RetryOfTaskID   string
	ParametersJSON  string
	Stage           string
	ProgressCurrent int
	ProgressTotal   int
	ErrorCode       string
	ErrorMessage    string
	CreatedAt       time.Time
	StartedAt       time.Time
	FinishedAt      time.Time
}

type Event struct {
	TaskID    string    `json:"task_id"`
	Seq       int       `json:"seq"`
	Level     string    `json:"level"`
	Stage     string    `json:"stage"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateRequest struct {
	BookID         string
	Type           Type
	InputFileID    string
	ParametersJSON string
	CreatedAt      time.Time
}

type ExecutionError struct {
	Code    string
	Message string
	Err     error
}

func (e *ExecutionError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *ExecutionError) Unwrap() error { return e.Err }

type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }
