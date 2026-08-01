package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/proofread"
	appsettings "github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func (h Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		values, err := settingsFromForm(r)
		if err == nil {
			err = h.settings.Save(r.Context(), values)
		}
		if err != nil {
			http.Redirect(w, r, "/settings?message="+urlMessage(err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/settings?message=saved", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	values, err := h.settings.Current(r.Context())
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	diagnostics := proofread.DiagnoseDependencies(r.Context(), nil, "", "")
	if h.Diagnostics != nil {
		diagnostics = h.Diagnostics(r.Context())
	}
	data := settingsPageData{
		Values: values, Runtime: h.settings.Runtime(), Diagnostics: diagnostics,
		Message: r.URL.Query().Get("message"), DropRegex: strings.Join(values.TXT.DropRegex, "\n"),
	}
	data.System, err = h.library.Diagnostics(r.Context())
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	data.SystemFree = humanSize(int64(data.System.FreeBytes))
	replacements, _ := json.Marshal(values.TXT.Replace)
	data.ReplaceJSON = string(replacements)
	if err := settingsTemplate.Execute(w, data); err != nil {
		h.writeInternalError(w, r, err)
	}
}

func (h Handler) handleCheckCodex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	check := h.CheckCodex
	if check == nil {
		check = func(ctx context.Context) (string, error) { return proofread.CheckCodexLogin(ctx, nil, "") }
	}
	status, err := check(r.Context())
	message := "Codex check passed: " + status
	if err != nil {
		message = "Codex check failed: " + err.Error()
	}
	http.Redirect(w, r, "/settings?message="+urlMessage(message), http.StatusSeeOther)
}

func settingsFromForm(r *http.Request) (appsettings.Values, error) {
	values := appsettings.Values{
		Author: r.Form.Get("author"), Language: r.Form.Get("language"), Cover: r.Form.Get("cover") != "",
		KindleShowEPUB: r.Form.Get("kindle_show_epub") != "",
		TXT: txtconfig.TXTConfig{
			H1Regex: r.Form.Get("h1_regex"), H2Regex: r.Form.Get("h2_regex"), SplitLevel: parseInt(r.Form.Get("split_level")),
			MergeLines: r.Form.Get("merge_lines") != "", TrimBlankLines: r.Form.Get("trim_blank_lines") != "",
			DropRegex: nonBlankLines(r.Form.Get("drop_regex")),
		},
		Style: txtconfig.Style{
			LineHeight: parseFloat(r.Form.Get("line_height")), ParagraphIndent: r.Form.Get("paragraph_indent"),
			ParagraphSpacing: r.Form.Get("paragraph_spacing"), TextAlign: r.Form.Get("text_align"),
		},
		Proofread: appsettings.Proofread{Model: r.Form.Get("proofread_model"), BatchSize: parseInt(r.Form.Get("proofread_batch_size")), Concurrency: parseInt(r.Form.Get("proofread_concurrency"))},
	}
	if raw := strings.TrimSpace(r.Form.Get("replace_json")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &values.TXT.Replace); err != nil {
			return appsettings.Values{}, fmt.Errorf("replacement rules must be valid JSON: %w", err)
		}
	}
	return values, nil
}

func nonBlankLines(raw string) []string {
	var values []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			values = append(values, line)
		}
	}
	return values
}

func parseInt(raw string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(raw))
	return value
}

func parseFloat(raw string) float64 {
	value, _ := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return value
}

func (h Handler) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	values, err := h.tasks.List(r.Context(), "")
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	if err := tasksTemplate.Execute(w, tasksPageData{Tasks: taskViews(values), Message: r.URL.Query().Get("message")}); err != nil {
		h.writeInternalError(w, r, err)
	}
}

func (h Handler) handleTaskRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/tasks/")
	if r.Method == http.MethodGet && strings.HasSuffix(rest, ".json") && !strings.Contains(strings.TrimSuffix(rest, ".json"), "/") {
		h.writeTaskStatus(w, r, strings.TrimSuffix(rest, ".json"))
		return
	}
	if r.Method == http.MethodGet && rest != "" && !strings.Contains(rest, "/") {
		value, ok, err := h.tasks.Get(r.Context(), rest)
		if err != nil {
			h.writeInternalError(w, r, err)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		events, err := h.tasks.Events(r.Context(), rest)
		if err != nil {
			h.writeInternalError(w, r, err)
			return
		}
		if err := taskDetailTemplate.Execute(w, taskDetailPageData{Task: taskViews([]task.Task{value})[0], Events: taskEventViews(events)}); err != nil {
			h.writeInternalError(w, r, err)
		}
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, action := parts[0], parts[1]
	if action == "status" && r.Method == http.MethodGet {
		h.writeTaskStatus(w, r, id)
		return
	}
	if r.Method != http.MethodPost || (action != "cancel" && action != "retry") {
		http.NotFound(w, r)
		return
	}
	original, ok, err := h.tasks.Get(r.Context(), id)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	message := "task canceled"
	if action == "cancel" {
		err = h.tasks.Cancel(r.Context(), id)
	} else {
		_, err = h.tasks.Retry(r.Context(), id)
		message = "task retry queued"
	}
	if err != nil {
		message = err.Error()
	}
	http.Redirect(w, r, "/books/"+original.BookID+"?message="+urlMessage(message), http.StatusSeeOther)
}

func (h Handler) writeTaskStatus(w http.ResponseWriter, r *http.Request, id string) {
	value, ok, err := h.tasks.Get(r.Context(), id)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	events, err := h.tasks.Events(r.Context(), id)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": value.ID, "status": value.Status, "stage": value.Stage,
		"current": value.ProgressCurrent, "total": value.ProgressTotal,
		"error_code": value.ErrorCode, "error_message": value.ErrorMessage,
		"events": events,
	})
}
