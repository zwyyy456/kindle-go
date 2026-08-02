package server

import (
	"context"
	"log"
	"net/http"

	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/proofread"
	appsettings "github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

type Handler struct {
	library     *library.Service
	generation  *generation.Service
	tasks       *task.Service
	settings    *appsettings.Service
	proofreads  *proofread.Service
	Diagnostics func(context.Context) proofread.DependencyDiagnostics
	CheckCodex  func(context.Context) (string, error)
	Logger      *log.Logger
}

func NewHandler(
	libraryService *library.Service,
	generationService *generation.Service,
	taskService *task.Service,
	settingsService *appsettings.Service,
	proofreadService *proofread.Service,
) Handler {
	if libraryService == nil || generationService == nil || taskService == nil || settingsService == nil || proofreadService == nil {
		panic("server.NewHandler requires library, generation, task, settings, and proofread services")
	}
	return Handler{
		library: libraryService, generation: generationService, tasks: taskService,
		settings: settingsService, proofreads: proofreadService,
	}
}

func (h Handler) WebMux() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(webStaticFiles))))
	mux.HandleFunc("GET /{$}", h.handleWebRoot)
	mux.HandleFunc("GET /books", h.handleWebIndex)
	mux.HandleFunc("POST /books/import", h.handleImport)
	mux.HandleFunc("POST /books/import/{token}/confirm", h.handleImportConfirm)
	mux.HandleFunc("GET /books/{bookID}", h.handleBookDetail)
	mux.HandleFunc("POST /books/{bookID}/generate", h.handleGenerate)
	mux.HandleFunc("POST /books/{bookID}/txt-preview", h.handleTXTPreview)
	mux.HandleFunc("POST /books/{bookID}/proofreads", h.handleCreateProofread)
	mux.HandleFunc("GET /books/{bookID}/proofreads/{runID}", h.handleProofreadReview)
	mux.HandleFunc("POST /books/{bookID}/delete", h.handleDeleteBook)
	mux.HandleFunc("POST /candidates/{candidateID}/decision", h.handleCandidateDecision)
	mux.HandleFunc("POST /proofreads/{runID}/revisions", h.handleCreateRevision)
	mux.HandleFunc("GET /files/{fileID}/download", h.handleDownload)
	mux.HandleFunc("POST /files/{fileID}/delete", h.handleFileDelete)
	mux.HandleFunc("GET /tasks", h.handleTasks)
	mux.HandleFunc("GET /tasks/{taskID}", h.handleTaskDetail)
	mux.HandleFunc("GET /tasks/{taskID}/status", h.handleTaskStatus)
	mux.HandleFunc("POST /tasks/{taskID}/cancel", h.handleTaskCancel)
	mux.HandleFunc("POST /tasks/{taskID}/retry", h.handleTaskRetry)
	mux.HandleFunc("GET /settings", h.handleSettings)
	mux.HandleFunc("POST /settings", h.handleSettings)
	mux.HandleFunc("POST /settings/check-codex", h.handleCheckCodex)
	return mux
}

func (h Handler) KindleMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.handleKindleIndex)
	mux.HandleFunc("GET /files/{fileID}/download", h.handleKindleDownload)
	return mux
}

func (h Handler) handleWebRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/books", http.StatusSeeOther)
}
