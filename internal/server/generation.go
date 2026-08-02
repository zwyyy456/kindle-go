package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/generation"
)

func (h Handler) handleGenerate(w http.ResponseWriter, r *http.Request) {
	bookID := r.PathValue("bookID")
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	formats := r.Form["format"]
	if len(formats) == 0 {
		formats = []string{"azw3"}
	}
	options, err := generationOptionsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, err = h.generation.Create(r.Context(), generation.CreateRequest{
		BookID: bookID, InputFileID: r.Form.Get("input_file_id"), Formats: formats,
		Options: options,
	})
	if err != nil {
		h.redirectActionError(w, r, "/books/"+bookID, err, "Generation task could not be queued. Check the selected input and try again.")
		return
	}
	http.Redirect(w, r, "/books/"+bookID+"?message=task+queued", http.StatusSeeOther)
}

func (h Handler) handleTXTPreview(w http.ResponseWriter, r *http.Request) {
	bookID := r.PathValue("bookID")
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	options, err := generationOptionsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	analysis, err := h.generation.PreviewTXT(r.Context(), generation.PreviewRequest{
		BookID: bookID, InputFileID: r.Form.Get("input_file_id"), Options: options,
	})
	if err != nil {
		h.writeActionError(w, r, err, http.StatusBadRequest, "TXT preview could not be generated. Check the selected input and parameters.")
		return
	}
	data := txtPreviewPageData{
		BookID: bookID, Charset: analysis.Charset,
		OriginalLines: analysis.Stats.Text.OriginalLines, DroppedLines: analysis.Stats.Text.DroppedLines,
		BlankLines: analysis.Stats.Text.BlankLines, MergedLines: analysis.Stats.Text.MergedLines,
		Sections: analysis.Stats.SectionCount, Paragraphs: analysis.Stats.ParagraphCount,
	}
	for _, entry := range analysis.TOC {
		data.TOC = append(data.TOC, entry.Title)
		for _, child := range entry.Children {
			data.TOC = append(data.TOC, "— "+child.Title)
		}
	}
	if err := txtPreviewTemplate.Execute(w, data); err != nil {
		h.writeInternalError(w, r, err)
	}
}

func generationOptionsFromForm(r *http.Request) (generation.Options, error) {
	options := generation.Options{
		Title: r.Form.Get("title"), Author: r.Form.Get("author"), Language: r.Form.Get("language"),
		H1Regex: r.Form.Get("h1_regex"), H2Regex: r.Form.Get("h2_regex"),
		SplitLevel: parseInt(r.Form.Get("split_level")), LineHeight: parseFloat(r.Form.Get("line_height")),
		ParagraphSpacing: r.Form.Get("paragraph_spacing"), ParagraphIndent: r.Form.Get("paragraph_indent"), TextAlign: r.Form.Get("text_align"),
		Cover: formBool(r, "cover"), MergeLines: formBool(r, "merge_lines"), TrimBlankLines: formBool(r, "trim_blank_lines"),
	}
	if r.Form.Has("drop_regex") {
		dropRegex := nonBlankLines(r.Form.Get("drop_regex"))
		options.DropRegex = &dropRegex
	}
	if r.Form.Has("replace_json") {
		replacements := []txtconfig.ReplaceRule{}
		if raw := strings.TrimSpace(r.Form.Get("replace_json")); raw != "" {
			if err := json.Unmarshal([]byte(raw), &replacements); err != nil {
				return generation.Options{}, fmt.Errorf("replacement rules must be valid JSON: %w", err)
			}
		}
		options.Replace = &replacements
	}
	return options, nil
}

func formBool(r *http.Request, name string) *bool {
	if r.Form.Get(name+"_present") == "" {
		return nil
	}
	value := r.Form.Get(name) != ""
	return &value
}
