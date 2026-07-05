package server

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/txt2epub/config"
)

const recentWindow = 24 * time.Hour

type Handler struct {
	Library    *Library
	BaseConfig txtconfig.Config
}

func (h Handler) WebMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleWebIndex)
	mux.HandleFunc("/upload", h.handleUpload)
	mux.HandleFunc("/convert", h.handleConvert)
	mux.HandleFunc("/download/", h.handleDownload)
	return mux
}

func (h Handler) KindleMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleKindleIndex)
	mux.HandleFunc("/download/", h.handleDownload)
	return mux
}

func (h Handler) handleWebIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	records := h.recordsFor(r)
	data := webPageData{
		Records: h.recordViews(records, false),
		ShowAll: r.URL.Query().Get("all") == "1",
		Message: r.URL.Query().Get("message"),
	}
	if err := webTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleKindleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	records := h.recordsFor(r)
	data := kindlePageData{
		Records: h.recordViews(records, true),
		ShowAll: r.URL.Query().Get("all") == "1",
	}
	if err := kindleTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing upload file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	if _, err := h.Library.AddUpload(header.Filename, file, time.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/?message=uploaded", http.StatusSeeOther)
}

func (h Handler) handleConvert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	opts := convertOptionsFromForm(r.Form.Get)
	record, ok := h.Library.Record(opts.RecordID)
	if !ok {
		http.Redirect(w, r, "/?message="+urlMessage("convert failed: record not found"), http.StatusSeeOther)
		return
	}
	inputPath, inputFile, err := h.Library.OriginalPath(record)
	if err != nil {
		http.Redirect(w, r, "/?message="+urlMessage("convert failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	format := outputFormat(opts.Format)
	now := time.Now()
	outputName := outputFileName(inputFile.Name, format, now)
	outputPath, relPath, err := h.Library.ConvertedPath(record.ID, outputName)
	if err != nil {
		http.Redirect(w, r, "/?message="+urlMessage("convert failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	if err := convertFile(inputPath, outputPath, inputFile.Format, h.BaseConfig, opts); err != nil {
		_ = h.Library.SetError(record.ID, err)
		http.Redirect(w, r, "/?message="+urlMessage("convert failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		_ = h.Library.SetError(record.ID, err)
		http.Redirect(w, r, "/?message="+urlMessage("convert failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	if err := h.Library.AddConverted(record.ID, outputName, relPath, info.Size(), now); err != nil {
		http.Redirect(w, r, "/?message="+urlMessage("convert failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?message=converted", http.StatusSeeOther)
}

func (h Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	recordID, kind, ok := parseDownloadPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	path, file, err := h.Library.ResolveFile(recordID, kind)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.Name))
	http.ServeFile(w, r, path)
}

func (h Handler) recordsFor(r *http.Request) []Record {
	if r.URL.Query().Get("all") == "1" {
		return h.Library.All()
	}
	return h.Library.Recent(time.Now(), recentWindow)
}

func (h Handler) recordViews(records []Record, kindle bool) []recordView {
	views := make([]recordView, 0, len(records))
	for _, record := range records {
		view := recordView{
			ID:           record.ID,
			OriginalName: record.OriginalName,
			UploadedAt:   record.UploadedAt.Format("2006-01-02 15:04"),
			LastError:    record.LastError,
		}
		view.Files = displayFileViews(record, kindle)
		view.InputFormat = strings.ToLower(record.Original.Format)
		view.Convertible = !kindle && (view.InputFormat == "txt" || view.InputFormat == "epub")
		view.TitleDefault = strings.TrimSuffix(record.Original.Name, filepath.Ext(record.Original.Name))
		if view.TitleDefault == "" {
			view.TitleDefault = strings.TrimSuffix(record.OriginalName, filepath.Ext(record.OriginalName))
		}
		if len(view.Files) > 0 || !kindle {
			views = append(views, view)
		}
	}
	return views
}

func parseDownloadPath(path string) (recordID, kind string, ok bool) {
	rest := strings.TrimPrefix(path, "/download/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		return "", "", false
	}
	switch parts[1] {
	case "original", "output":
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
}

func displayFileViews(record Record, kindle bool) []fileView {
	original := record.Original
	output := record.Output
	if kindle {
		if output.RelPath != "" && kindleFormat(output.Format) {
			return []fileView{newFileView(record.ID, "output", output)}
		}
		if original.RelPath != "" && kindleFormat(original.Format) {
			return []fileView{newFileView(record.ID, "original", original)}
		}
		return nil
	}

	var files []fileView
	if original.RelPath != "" {
		files = append(files, newFileView(record.ID, "original", original))
	}
	if output.RelPath != "" {
		files = append(files, newFileView(record.ID, "output", output))
	}
	return files
}

func newFileView(recordID, kind string, file FileEntry) fileView {
	return fileView{
		Kind:   kind,
		Name:   file.Name,
		Format: strings.ToUpper(file.Format),
		Size:   humanSize(file.Size),
		URL:    downloadURL(recordID, kind),
	}
}

func downloadURL(recordID, kind string) string {
	return "/download/" + recordID + "/" + kind
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
	Records []recordView
	ShowAll bool
	Message string
}

type kindlePageData struct {
	Records []recordView
	ShowAll bool
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
	Kind   string
	Name   string
	Format string
	Size   string
	URL    string
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
    <form method="post" action="/upload" enctype="multipart/form-data">
      <input type="file" name="file" required>
      <button type="submit">Upload</button>
    </form>
  </fieldset>

  <p>
    {{if .ShowAll}}<a href="/">Show recent uploads</a>{{else}}<a href="/?all=1">Show all history</a>{{end}}
  </p>

  {{range .Records}}
  <section class="record">
    <h2>{{.OriginalName}}</h2>
    <p class="muted">Uploaded {{.UploadedAt}}</p>
    {{if .LastError}}<p class="error">{{.LastError}}</p>{{end}}
    <div class="files">
      {{range .Files}}
        <p><a href="{{.URL}}">{{.Name}}</a> <span class="muted">{{.Kind}}, {{.Format}}, {{.Size}}</span></p>
      {{end}}
    </div>
    {{if .Convertible}}
    <details>
      <summary>Convert</summary>
      <form method="post" action="/convert">
        <input type="hidden" name="record_id" value="{{.ID}}">
        <label>Format
          <select name="format">
            <option value="azw3">AZW3</option>
            {{if eq .InputFormat "txt"}}<option value="epub">EPUB</option>{{end}}
          </select>
        </label>
        <label>Title <input type="text" name="title" value="{{.TitleDefault}}"></label>
        <label>Author <input type="text" name="author"></label>
        <label>Language <input type="text" name="language"></label>
        {{if eq .InputFormat "txt"}}
        <label>H1 regex <input type="text" name="h1_regex"></label>
        <label>H2 regex <input type="text" name="h2_regex"></label>
        <label>Split level <input type="number" name="split_level" min="1" max="2"></label>
        <label>Line height <input type="text" name="line_height"></label>
        <label>Paragraph spacing <input type="text" name="paragraph_spacing"></label>
        <label>Paragraph indent <input type="text" name="paragraph_indent"></label>
        <label>Text align <input type="text" name="text_align"></label>
        {{end}}
        <button type="submit">Convert</button>
      </form>
    </details>
    {{end}}
  </section>
  {{else}}
  <p>No uploads yet.</p>
  {{end}}
</body>
</html>`))

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
