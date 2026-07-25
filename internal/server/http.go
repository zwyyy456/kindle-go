package server

import (
	"context"
	"log"
	"net/http"
	"strings"

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
	mux.HandleFunc("/", h.handleWebRoot)
	mux.HandleFunc("/books", h.handleBooks)
	mux.HandleFunc("/books/", h.handleBookRoute)
	mux.HandleFunc("/candidates/", h.handleCandidateRoute)
	mux.HandleFunc("/proofreads/", h.handleProofreadRoute)
	mux.HandleFunc("/files/", h.handleFileRoute)
	mux.HandleFunc("/tasks", h.handleTasks)
	mux.HandleFunc("/tasks/", h.handleTaskRoute)
	mux.HandleFunc("/settings", h.handleSettings)
	mux.HandleFunc("/settings/check-codex", h.handleCheckCodex)
	return mux
}

func (h Handler) KindleMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleKindleIndex)
	mux.HandleFunc("/files/", h.handleKindleFileRoute)
	return mux
}

func (h Handler) handleWebRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/books", http.StatusSeeOther)
}

func (h Handler) handleBooks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleWebIndex(w, r)
	case http.MethodPost:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h Handler) handleBookRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/books/")
	if path == "import" {
		h.handleImport(w, r)
		return
	}
	if strings.HasPrefix(path, "import/") && strings.HasSuffix(path, "/confirm") {
		token := strings.TrimSuffix(strings.TrimPrefix(path, "import/"), "/confirm")
		h.handleImportConfirm(w, r, token)
		return
	}
	parts := strings.Split(path, "/")
	if r.Method == http.MethodGet && len(parts) == 3 && parts[0] != "" && parts[1] == "proofreads" && parts[2] != "" {
		h.handleProofreadReview(w, r, parts[0], parts[2])
		return
	}
	if r.Method == http.MethodGet && path != "" && !strings.Contains(path, "/") {
		h.handleBookDetail(w, r, path)
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/generate") {
		bookID := strings.TrimSuffix(path, "/generate")
		if bookID != "" && !strings.Contains(bookID, "/") {
			h.handleGenerate(w, r, bookID)
			return
		}
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/txt-preview") {
		bookID := strings.TrimSuffix(path, "/txt-preview")
		if bookID != "" && !strings.Contains(bookID, "/") {
			h.handleTXTPreview(w, r, bookID)
			return
		}
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/proofreads") {
		bookID := strings.TrimSuffix(path, "/proofreads")
		if bookID != "" && !strings.Contains(bookID, "/") {
			h.handleCreateProofread(w, r, bookID)
			return
		}
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/delete") {
		bookID := strings.TrimSuffix(path, "/delete")
		if bookID != "" && !strings.Contains(bookID, "/") {
			h.handleDeleteBook(w, r, bookID)
			return
		}
	}
	http.NotFound(w, r)
}
