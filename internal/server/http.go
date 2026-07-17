package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

const recentWindow = 24 * time.Hour

type Handler struct {
	Library    *library.Service
	Generation *generation.Service
	Tasks      *task.Service
}

func (h Handler) WebMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleWebRoot)
	mux.HandleFunc("/books", h.handleBooks)
	mux.HandleFunc("/books/", h.handleBookRoute)
	mux.HandleFunc("/files/", h.handleFileRoute)
	mux.HandleFunc("/tasks", h.handleTasks)
	mux.HandleFunc("/tasks/", h.handleTaskRoute)
	return mux
}

func (h Handler) KindleMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleKindleIndex)
	mux.HandleFunc("/files/", h.handleFileRoute)
	return mux
}

func (h Handler) handleWebIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	records := h.recordsFor(r)
	data := webPageData{
		Records: h.recordViews(records, false),
		ShowAll: r.URL.Query().Get("all") == "1",
		Message: r.URL.Query().Get("message"),
	}
	if token := r.URL.Query().Get("duplicate"); token != "" {
		if pending, ok := h.Library.PendingDuplicate(token); ok {
			data.Duplicate = duplicateView{Token: token, Filename: pending.Filename, Existing: h.recordViews(pending.Existing, false)}
		} else {
			data.Message = "Duplicate confirmation expired; import the file again."
		}
	}
	if err := webTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
	http.NotFound(w, r)
}

func (h Handler) handleKindleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	books, err := h.Library.LatestKindleFiles(r.Context(), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var records []recordView
	cutoff := time.Now().Add(-recentWindow)
	for _, value := range books {
		if r.URL.Query().Get("all") != "1" && value.Book.ImportedAt.Before(cutoff) {
			continue
		}
		records = append(records, recordView{
			ID: value.Book.ID, OriginalName: value.Book.DisplayName,
			UploadedAt: value.Book.ImportedAt.Format("2006-01-02 15:04"),
			Files:      []fileView{newFileView(value.File)},
		})
	}
	data := kindlePageData{
		Records: records,
		ShowAll: r.URL.Query().Get("all") == "1",
	}
	if err := kindleTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, library.MaxEPUBBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "upload_too_large: request exceeds the 64 MiB EPUB limit", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing upload file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	result, err := h.Library.Import(r.Context(), library.ImportRequest{Filename: header.Filename, Reader: file, Now: time.Now()})
	if err != nil {
		code := library.ErrorCode(err)
		status := http.StatusInternalServerError
		if code != "" {
			status = http.StatusBadRequest
		}
		if code == "upload_too_large" {
			status = http.StatusRequestEntityTooLarge
		}
		message := err.Error()
		if code != "" {
			message = code + ": " + message
		}
		http.Error(w, message, status)
		return
	}
	if result.Duplicate {
		http.Redirect(w, r, "/books?duplicate="+url.QueryEscape(result.DuplicateToken), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/books/"+result.Book.ID+"?message=imported", http.StatusSeeOther)
}

func (h Handler) handleImportConfirm(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	book, err := h.Library.ConfirmImport(r.Context(), token, r.Form.Get("action"))
	if err != nil {
		http.Redirect(w, r, "/books?message="+urlMessage(err.Error()), http.StatusSeeOther)
		return
	}
	if book.ID == "" {
		http.Redirect(w, r, "/books?message=import+canceled", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/books/"+book.ID, http.StatusSeeOther)
}

func (h Handler) handleBookDetail(w http.ResponseWriter, r *http.Request, id string) {
	detail, ok, err := h.Library.GetBookDetail(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	var tasks []task.Task
	if h.Tasks != nil {
		tasks, err = h.Tasks.List(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	files := make([]fileView, 0, len(detail.Files))
	for _, file := range detail.Files {
		files = append(files, newFileView(file))
	}
	data := bookPageData{
		Book: recordView{
			ID: detail.Book.ID, OriginalName: detail.Book.DisplayName,
			UploadedAt:   detail.Book.ImportedAt.Format("2006-01-02 15:04"),
			InputFormat:  detail.Book.SourceFormat,
			TitleDefault: strings.TrimSuffix(detail.Book.Original.DisplayName, filepath.Ext(detail.Book.Original.DisplayName)),
		},
		Files: files, Tasks: taskViews(tasks), Message: r.URL.Query().Get("message"),
	}
	if err := bookTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleGenerate(w http.ResponseWriter, r *http.Request, bookID string) {
	if h.Generation == nil {
		http.Error(w, "generation service is unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	formats := r.Form["format"]
	if len(formats) == 0 {
		formats = []string{"azw3"}
	}
	_, err := h.Generation.Create(r.Context(), generation.CreateRequest{
		BookID: bookID, Formats: formats,
		Options: generation.Options{
			Title: r.Form.Get("title"), Author: r.Form.Get("author"), Language: r.Form.Get("language"),
			H1Regex: r.Form.Get("h1_regex"), H2Regex: r.Form.Get("h2_regex"),
			SplitLevel: parseInt(r.Form.Get("split_level")), LineHeight: parseFloat(r.Form.Get("line_height")),
			ParagraphSpacing: r.Form.Get("paragraph_spacing"), ParagraphIndent: r.Form.Get("paragraph_indent"), TextAlign: r.Form.Get("text_align"),
		},
	})
	if err != nil {
		http.Redirect(w, r, "/books/"+bookID+"?message="+urlMessage("generate failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/books/"+bookID+"?message=task+queued", http.StatusSeeOther)
}

func parseInt(raw string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(raw))
	return value
}

func parseFloat(raw string) float64 {
	value, _ := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return value
}

func (h Handler) handleFileRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/files/")
	if !strings.HasSuffix(rest, "/download") {
		http.NotFound(w, r)
		return
	}
	fileID := strings.TrimSuffix(rest, "/download")
	if fileID == "" || strings.Contains(fileID, "/") {
		http.NotFound(w, r)
		return
	}
	path, file, err := h.Library.DownloadFile(r.Context(), fileID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.DisplayName))
	http.ServeFile(w, r, path)
}

func (h Handler) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || h.Tasks == nil {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	values, err := h.Tasks.List(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tasksTemplate.Execute(w, tasksPageData{Tasks: taskViews(values), Message: r.URL.Query().Get("message")}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleTaskRoute(w http.ResponseWriter, r *http.Request) {
	if h.Tasks == nil {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/tasks/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, action := parts[0], parts[1]
	if action == "status" && r.Method == http.MethodGet {
		value, ok, err := h.Tasks.Get(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": value.ID, "status": value.Status, "stage": value.Stage, "current": value.ProgressCurrent, "total": value.ProgressTotal, "error_code": value.ErrorCode, "error_message": value.ErrorMessage})
		return
	}
	if r.Method != http.MethodPost || (action != "cancel" && action != "retry") {
		http.NotFound(w, r)
		return
	}
	original, ok, err := h.Tasks.Get(r.Context(), id)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	message := "task canceled"
	if action == "cancel" {
		err = h.Tasks.Cancel(r.Context(), id)
	} else {
		_, err = h.Tasks.Retry(r.Context(), id)
		message = "task retry queued"
	}
	if err != nil {
		message = err.Error()
	}
	http.Redirect(w, r, "/books/"+original.BookID+"?message="+urlMessage(message), http.StatusSeeOther)
}

func (h Handler) recordsFor(r *http.Request) []library.Book {
	var records []library.Book
	var err error
	if r.URL.Query().Get("all") == "1" {
		records, err = h.Library.AllBooks(r.Context())
	} else {
		records, err = h.Library.RecentBooks(r.Context(), time.Now().Add(-recentWindow))
	}
	if err != nil {
		return nil
	}
	return records
}

func (h Handler) recordViews(records []library.Book, kindle bool) []recordView {
	views := make([]recordView, 0, len(records))
	for _, record := range records {
		view := recordView{
			ID:           record.ID,
			OriginalName: record.DisplayName,
			UploadedAt:   record.ImportedAt.Format("2006-01-02 15:04"),
			LastError:    record.LegacyLastError,
		}
		view.Files = displayFileViews(record, kindle)
		view.InputFormat = strings.ToLower(record.Original.Format)
		view.Convertible = !kindle && (view.InputFormat == "txt" || view.InputFormat == "epub")
		view.TitleDefault = strings.TrimSuffix(record.Original.DisplayName, filepath.Ext(record.Original.DisplayName))
		if view.TitleDefault == "" {
			view.TitleDefault = strings.TrimSuffix(record.DisplayName, filepath.Ext(record.DisplayName))
		}
		if len(view.Files) > 0 || !kindle {
			views = append(views, view)
		}
	}
	return views
}

func displayFileViews(record library.Book, kindle bool) []fileView {
	original := record.Original
	output := record.LatestArtifact
	if kindle {
		if output.ID != "" && kindleFormat(output.Format) {
			return []fileView{newFileView(output)}
		}
		if original.ID != "" && kindleFormat(original.Format) {
			return []fileView{newFileView(original)}
		}
		return nil
	}

	var files []fileView
	if original.ID != "" {
		files = append(files, newFileView(original))
	}
	if output.ID != "" {
		files = append(files, newFileView(output))
	}
	return files
}

func newFileView(file library.File) fileView {
	return fileView{
		ID:        file.ID,
		Kind:      file.Role,
		Name:      file.DisplayName,
		Format:    strings.ToUpper(file.Format),
		Size:      humanSize(file.Size),
		URL:       downloadURL(file.ID),
		CreatedAt: file.CreatedAt.Format("2006-01-02 15:04"),
	}
}

func downloadURL(fileID string) string {
	return "/files/" + fileID + "/download"
}

func kindleFormat(format string) bool {
	switch strings.ToLower(format) {
	case "azw3", "mobi", "pdf", "txt":
		return true
	default:
		return false
	}
}

func humanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func urlMessage(message string) string {
	return url.QueryEscape(message)
}

type webPageData struct {
	Records   []recordView
	ShowAll   bool
	Message   string
	Duplicate duplicateView
}

type duplicateView struct {
	Token    string
	Filename string
	Existing []recordView
}

type kindlePageData struct {
	Records []recordView
	ShowAll bool
}

type bookPageData struct {
	Book    recordView
	Files   []fileView
	Tasks   []taskView
	Message string
}

type tasksPageData struct {
	Tasks   []taskView
	Message string
}

type recordView struct {
	ID           string
	OriginalName string
	UploadedAt   string
	LastError    string
	Files        []fileView
	Convertible  bool
	InputFormat  string
	TitleDefault string
}

type fileView struct {
	ID        string
	Kind      string
	Name      string
	Format    string
	Size      string
	URL       string
	CreatedAt string
}

type taskView struct {
	ID        string
	BookID    string
	Type      string
	Status    string
	Stage     string
	Progress  string
	Error     string
	CanCancel bool
	CanRetry  bool
	Active    bool
	CreatedAt string
}

func taskViews(values []task.Task) []taskView {
	views := make([]taskView, 0, len(values))
	for _, value := range values {
		progress := ""
		if value.ProgressTotal > 0 {
			progress = fmt.Sprintf("%d/%d", value.ProgressCurrent, value.ProgressTotal)
		}
		views = append(views, taskView{
			ID: value.ID, BookID: value.BookID, Type: string(value.Type), Status: string(value.Status),
			Stage: value.Stage, Progress: progress, Error: value.ErrorMessage,
			CanCancel: value.Status == task.Queued || value.Status == task.Running,
			CanRetry:  value.Status == task.Failed || value.Status == task.Canceled,
			Active:    value.Status == task.Queued || value.Status == task.Running,
			CreatedAt: value.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return views
}

var webTemplate = template.Must(template.New("web").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>Kindle Go</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
    h1 { margin-bottom: 8px; }
    fieldset { border: 1px solid #ccd3dc; padding: 16px; margin: 16px 0; }
    label { display: block; margin: 8px 0; }
    input[type=text], input[type=number], select { min-width: 320px; max-width: 100%; padding: 6px; }
    button { padding: 7px 12px; }
    .record { border-top: 1px solid #d8dee6; padding: 18px 0; }
    .files a { margin-right: 8px; }
    .error { color: #9b1c1c; }
    .muted { color: #627282; }
  </style>
</head>
<body>
  <h1>Kindle Go</h1>
  <p class="muted">Upload files from this computer, convert when needed, then download from Kindle.</p>
  {{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}

  <fieldset>
    <legend>Upload</legend>
    <form method="post" action="/books/import" enctype="multipart/form-data">
      <input type="file" name="file" accept=".txt,.epub" required>
      <button type="submit">Import</button>
    </form>
    <p class="muted">TXT up to 32 MiB; EPUB up to 64 MiB and 512 MiB expanded.</p>
  </fieldset>

  {{if .Duplicate.Token}}
  <fieldset>
    <legend>Duplicate source</legend>
    <p><strong>{{.Duplicate.Filename}}</strong> has the same content as an existing book.</p>
    {{range .Duplicate.Existing}}<p>{{.OriginalName}} — {{.UploadedAt}}</p>{{end}}
    <form method="post" action="/books/import/{{.Duplicate.Token}}/confirm">
      <button name="action" value="open" type="submit">Open existing book</button>
      <button name="action" value="import" type="submit">Import as a new book</button>
      <button name="action" value="cancel" type="submit">Cancel</button>
    </form>
  </fieldset>
  {{end}}

  <p>
    {{if .ShowAll}}<a href="/books">Show recent uploads</a>{{else}}<a href="/books?all=1">Show all history</a>{{end}}
  </p>

  {{range .Records}}
  <section class="record">
    <h2><a href="/books/{{.ID}}">{{.OriginalName}}</a></h2>
    <p class="muted">Uploaded {{.UploadedAt}}</p>
    {{if .LastError}}<p class="error">{{.LastError}}</p>{{end}}
    <div class="files">
      {{range .Files}}
        <p><a href="{{.URL}}">{{.Name}}</a> <span class="muted">{{.Kind}}, {{.Format}}, {{.Size}}</span></p>
      {{end}}
    </div>
    <p><a href="/books/{{.ID}}">Open details, generate files, and view tasks</a></p>
  </section>
  {{else}}
  <p>No uploads yet.</p>
  {{end}}
</body>
</html>`))

var bookTemplate = template.Must(template.New("book").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>{{.Book.OriginalName}} — Kindle Go</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
    fieldset { border: 1px solid #ccd3dc; padding: 16px; margin: 18px 0; }
    label { display: block; margin: 8px 0; }
    input[type=text], input[type=number] { min-width: 320px; padding: 6px; }
    table { width: 100%; border-collapse: collapse; margin: 12px 0; }
    th, td { text-align: left; border-bottom: 1px solid #d8dee6; padding: 8px; }
    .error { color: #9b1c1c; }
    .muted { color: #627282; }
    form.inline { display: inline; }
  </style>
</head>
<body>
  <p><a href="/books">← Library</a> · <a href="/tasks">All tasks</a></p>
  <h1>{{.Book.OriginalName}}</h1>
  <p class="muted">Imported {{.Book.UploadedAt}} · {{.Book.InputFormat}}</p>
  {{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}

  <fieldset>
    <legend>Generate</legend>
    <form method="post" action="/books/{{.Book.ID}}/generate">
      <label><input type="checkbox" name="format" value="azw3" checked> AZW3</label>
      {{if eq .Book.InputFormat "txt"}}<label><input type="checkbox" name="format" value="epub"> EPUB</label>{{end}}
      <label>Title <input type="text" name="title" value="{{.Book.TitleDefault}}"></label>
      <label>Author <input type="text" name="author"></label>
      <label>Language <input type="text" name="language"></label>
      {{if eq .Book.InputFormat "txt"}}
      <label>H1 regex <input type="text" name="h1_regex"></label>
      <label>H2 regex <input type="text" name="h2_regex"></label>
      <label>Split level <input type="number" name="split_level" min="1" max="2"></label>
      <label>Line height <input type="text" name="line_height"></label>
      <label>Paragraph spacing <input type="text" name="paragraph_spacing"></label>
      <label>Paragraph indent <input type="text" name="paragraph_indent"></label>
      <label>Text align <input type="text" name="text_align"></label>
      {{end}}
      <button type="submit">Queue generation</button>
    </form>
  </fieldset>

  <h2>Files</h2>
  <table><thead><tr><th>Name</th><th>Role</th><th>Format</th><th>Size</th><th>Created</th></tr></thead><tbody>
  {{range .Files}}<tr><td><a href="{{.URL}}">{{.Name}}</a></td><td>{{.Kind}}</td><td>{{.Format}}</td><td>{{.Size}}</td><td>{{.CreatedAt}}</td></tr>{{else}}<tr><td colspan="5">No files.</td></tr>{{end}}
  </tbody></table>

  <h2>Tasks</h2>
  <table><thead><tr><th>Type</th><th>Status</th><th>Stage</th><th>Progress</th><th>Created</th><th>Actions</th></tr></thead><tbody>
  {{range .Tasks}}<tr data-task-id="{{.ID}}" data-task-status="{{.Status}}"><td>{{.Type}}</td><td>{{.Status}}{{if .Error}}<div class="error">{{.Error}}</div>{{end}}</td><td>{{.Stage}}</td><td>{{.Progress}}</td><td>{{.CreatedAt}}</td><td>
    {{if .CanCancel}}<form class="inline" method="post" action="/tasks/{{.ID}}/cancel"><button type="submit">Cancel</button></form>{{end}}
    {{if .CanRetry}}<form class="inline" method="post" action="/tasks/{{.ID}}/retry"><button type="submit">Retry from beginning</button></form>{{end}}
  </td></tr>{{else}}<tr><td colspan="6">No tasks.</td></tr>{{end}}
  </tbody></table>
  <script>
    const active = [...document.querySelectorAll('[data-task-id]')].filter(row => ['queued','running'].includes(row.dataset.taskStatus));
    if (active.length) setTimeout(async () => {
      const changed = await Promise.all(active.map(async row => {
        const result = await fetch('/tasks/' + row.dataset.taskId + '/status');
        if (!result.ok) return false;
        const task = await result.json();
        return task.status !== row.dataset.taskStatus;
      }));
      if (changed.some(Boolean)) location.reload(); else location.reload();
    }, 2000);
  </script>
</body>
</html>`))

var tasksTemplate = template.Must(template.New("tasks").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Tasks — Kindle Go</title></head><body>
<p><a href="/books">← Library</a></p><h1>Tasks</h1>{{if .Message}}<p>{{.Message}}</p>{{end}}
<table><thead><tr><th>Book</th><th>Type</th><th>Status</th><th>Stage</th><th>Progress</th><th>Created</th></tr></thead><tbody>
{{range .Tasks}}<tr><td><a href="/books/{{.BookID}}">{{.BookID}}</a></td><td>{{.Type}}</td><td>{{.Status}}{{if .Error}} — {{.Error}}{{end}}</td><td>{{.Stage}}</td><td>{{.Progress}}</td><td>{{.CreatedAt}}</td></tr>{{else}}<tr><td colspan="6">No tasks.</td></tr>{{end}}
</tbody></table></body></html>`))

var kindleTemplate = template.Must(template.New("kindle").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>Kindle Downloads</title>
</head>
<body>
  <h1>Downloads</h1>
  <p>{{if .ShowAll}}<a href="/">Recent</a>{{else}}<a href="/?all=1">All files</a>{{end}}</p>
  {{range .Records}}
    <h2>{{.OriginalName}}</h2>
    <p>{{.UploadedAt}}</p>
    <ul>
    {{range .Files}}
      <li><a href="{{.URL}}">{{.Name}}</a> ({{.Format}}, {{.Size}})</li>
    {{end}}
    </ul>
  {{else}}
    <p>No downloadable files.</p>
  {{end}}
</body>
</html>`))
