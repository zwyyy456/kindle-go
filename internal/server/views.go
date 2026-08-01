package server

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/proofread"
	appsettings "github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func (h Handler) recordViews(records []library.Book) []recordView {
	views := make([]recordView, 0, len(records))
	for _, record := range records {
		view := recordView{
			ID:              record.ID,
			OriginalName:    record.DisplayName,
			UploadedAt:      record.ImportedAt.Format("2006-01-02 15:04"),
			ProofreadStatus: record.ProofreadStatus,
		}
		view.Files = displayFileViews(record)
		view.InputFormat = strings.ToLower(record.Original.Format)
		view.TitleDefault = strings.TrimSuffix(record.Original.DisplayName, filepath.Ext(record.Original.DisplayName))
		if view.TitleDefault == "" {
			view.TitleDefault = strings.TrimSuffix(record.DisplayName, filepath.Ext(record.DisplayName))
		}
		views = append(views, view)
	}
	return views
}

func displayFileViews(record library.Book) []fileView {
	original := record.Original
	output := record.LatestArtifact
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
		ID:         file.ID,
		Kind:       file.Role,
		Name:       file.DisplayName,
		Format:     strings.ToUpper(file.Format),
		Size:       humanSize(file.Size),
		URL:        downloadURL(file.ID),
		CreatedAt:  file.CreatedAt.Format("2006-01-02 15:04"),
		CanDelete:  file.Role != "original",
		Unresolved: file.HasUnresolved,
	}
}

func fileViews(files []library.File) []fileView {
	views := make([]fileView, 0, len(files))
	for _, file := range files {
		views = append(views, newFileView(file))
	}
	return views
}

func downloadURL(fileID string) string {
	return "/files/" + fileID + "/download"
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
	Records      []recordView
	Message      string
	Duplicate    duplicateView
	Query        string
	Sort         string
	StatusFilter string
	Page         int
	PreviousPage int
	NextPage     int
	HasPrevious  bool
	HasNext      bool
	Total        int
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
	Book             recordView
	Files            []fileView
	Tasks            []taskView
	ProofreadRuns    []proofreadRunView
	GenerationInputs []generation.InputAssessment
	Message          string
	Compatibility    *library.CompatibilityReport
	CanGenerate      bool
	Defaults         appsettings.Values
	DropRegex        string
	ReplaceJSON      string
}

type proofreadRunView struct {
	ID          string
	Format      string
	Model       string
	CompletedAt string
}

type proofreadPageData struct {
	BookID       string
	Run          proofreadRunView
	Candidates   []proofread.CandidateView
	Filter       string
	Page         int
	PreviousPage int
	NextPage     int
	HasPrevious  bool
	HasNext      bool
	Total        int
	Unresolved   int
	Message      string
}

type settingsPageData struct {
	Values      appsettings.Values
	Runtime     appsettings.Runtime
	Diagnostics proofread.DependencyDiagnostics
	Message     string
	DropRegex   string
	ReplaceJSON string
	System      library.Diagnostics
	SystemFree  string
}

type tasksPageData struct {
	Tasks   []taskView
	Message string
}

type taskDetailPageData struct {
	Task   taskView
	Events []taskEventView
}

type txtPreviewPageData struct {
	BookID        string
	Charset       string
	OriginalLines int
	DroppedLines  int
	BlankLines    int
	MergedLines   int
	Sections      int
	Paragraphs    int
	TOC           []string
}

type recordView struct {
	ID              string
	OriginalName    string
	UploadedAt      string
	Files           []fileView
	InputFormat     string
	TitleDefault    string
	ProofreadStatus string
}

type fileView struct {
	ID                  string
	Kind                string
	Name                string
	Format              string
	Size                string
	URL                 string
	CreatedAt           string
	CanDelete           bool
	Unresolved          bool
	CompatibilityStatus string
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

type taskEventView struct {
	Seq       int
	Level     string
	Stage     string
	Message   string
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

func taskEventViews(values []task.Event) []taskEventView {
	views := make([]taskEventView, 0, len(values))
	for _, value := range values {
		views = append(views, taskEventView{
			Seq: value.Seq, Level: value.Level, Stage: value.Stage, Message: value.Message,
			CreatedAt: value.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return views
}
