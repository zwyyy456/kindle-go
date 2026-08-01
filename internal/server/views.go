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
		ID:           file.ID,
		Kind:         file.Role,
		Name:         file.DisplayName,
		Format:       strings.ToUpper(file.Format),
		Size:         humanSize(file.Size),
		URL:          downloadURL(file.ID),
		CreatedAt:    file.CreatedAt.Format("2006-01-02 15:04"),
		TaskURL:      taskURL(file.TaskID),
		ProofreadURL: proofreadURL(file.BookID, file.ProofreadRunID),
		CanDelete:    file.Role != "original",
		Unresolved:   file.HasUnresolved,
	}
}

func enrichFileView(view *fileView, file library.File, sources map[string]library.File, tasks map[string]task.Task, runs map[string]proofread.Run) {
	if source, ok := sources[file.SourceFileID]; ok {
		view.SourceName = source.DisplayName
		view.SourceRole = sourceRoleLabel(source.Role)
		view.SourceURL = downloadURL(source.ID)
	}
	if value, ok := tasks[file.TaskID]; ok {
		view.ParameterSummary = parameterSummary(file.ParametersJSON, value.Type, runs[file.ProofreadRunID])
	}
}

func parameterSummary(raw string, kind task.Type, run proofread.Run) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	switch kind {
	case task.GenerateEPUB, task.GenerateAZW3:
		params, err := generation.ParseParameters(raw)
		if err != nil {
			return []string{"Parameter snapshot unavailable"}
		}
		return generationParameterSummary(params)
	case task.BuildRevisionTXT, task.BuildRevisionEPUB:
		params, err := proofread.ParseRevisionParameters(raw)
		if err != nil {
			return []string{"Parameter snapshot unavailable"}
		}
		return revisionParameterSummary(params, run)
	default:
		return nil
	}
}

func generationParameterSummary(params generation.Parameters) []string {
	lines := []string{
		fmt.Sprintf("Input/output: %s → %s", strings.ToUpper(params.InputFormat), strings.ToUpper(params.OutputFormat)),
		fmt.Sprintf("Metadata: title %q; author %q; language %s", params.Metadata.Title, params.Metadata.Author, params.Metadata.Language),
		fmt.Sprintf("Cover: %s", enabledLabel(params.Cover)),
		fmt.Sprintf("Text cleanup: merge lines %s; trim blank lines %s; split level %d", enabledLabel(params.TXT.MergeLines), enabledLabel(params.TXT.TrimBlankLines), params.TXT.SplitLevel),
		fmt.Sprintf("Layout: line height %.2f; indent %s; spacing %s; align %s", params.Style.LineHeight, params.Style.ParagraphIndent, params.Style.ParagraphSpacing, params.Style.TextAlign),
	}
	lines = append(lines, fmt.Sprintf("H1 regex: %s", params.TXT.H1Regex), fmt.Sprintf("H2 regex: %s", params.TXT.H2Regex))
	if len(params.TXT.DropRegex) == 0 {
		lines = append(lines, "Drop rules: None")
	} else {
		for _, pattern := range params.TXT.DropRegex {
			lines = append(lines, fmt.Sprintf("Drop rule: %s", pattern))
		}
	}
	if len(params.TXT.Replace) == 0 {
		lines = append(lines, "Replace rules: None")
	} else {
		for _, rule := range params.TXT.Replace {
			lines = append(lines, fmt.Sprintf("Replace rule: %s → %s", rule.Pattern, rule.With))
		}
	}
	return lines
}

func revisionParameterSummary(params proofread.RevisionParameters, run proofread.Run) []string {
	lines := []string{
		fmt.Sprintf("Input format: %s", strings.ToUpper(params.Format)),
		fmt.Sprintf("Engine: %s", params.EngineVersion),
		fmt.Sprintf("Decisions: %s", decisionSummary(params.Decisions)),
	}
	if run.ID != "" {
		model := run.Model
		if model == "" {
			model = "Codex default"
		}
		lines = append([]string{
			fmt.Sprintf("Proofread run: %s; model %s", run.CreatedAt.Format("2006-01-02 15:04"), model),
			fmt.Sprintf("Batch size: %d; concurrency: %d", run.BatchSize, run.Concurrency),
		}, lines...)
	}
	if params.HasUnresolved {
		lines = append(lines, "Unresolved items: kept according to confirmation")
	}
	return lines
}

func decisionSummary(decisions []proofread.RevisionDecision) string {
	counts := map[string]int{}
	for _, decision := range decisions {
		counts[decision.Outcome]++
	}
	return fmt.Sprintf("%d total; applied %d; rejected %d; pending %d; conflicts %d", len(decisions), counts["apply_automatic"]+counts["apply_accepted"]+counts["apply_modified"], counts["keep_rejected"], counts["keep_pending"], counts["keep_conflict"])
}

func enabledLabel(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func sourceRoleLabel(role string) string {
	switch role {
	case "original":
		return "Original"
	case "revision":
		return "Revision"
	default:
		return role
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

func taskURL(taskID string) string {
	if taskID == "" {
		return ""
	}
	return "/tasks/" + taskID
}

func proofreadURL(bookID, runID string) string {
	if bookID == "" || runID == "" {
		return ""
	}
	return "/books/" + bookID + "/proofreads/" + runID
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
	SourceName          string
	SourceRole          string
	SourceURL           string
	TaskURL             string
	ProofreadURL        string
	ParameterSummary    []string
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
