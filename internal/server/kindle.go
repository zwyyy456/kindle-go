package server

import (
	"context"
	"net/http"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/library"
)

const recentWindow = 24 * time.Hour

func (h Handler) handleKindleIndex(w http.ResponseWriter, r *http.Request) {
	values, err := h.settings.Current(r.Context())
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	books, err := h.library.LatestKindleFiles(r.Context(), values.KindleShowEPUB)
	if err != nil {
		h.writeInternalError(w, r, err)
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
			Files:      fileViews(value.Files),
		})
	}
	data := kindlePageData{Records: records, ShowAll: r.URL.Query().Get("all") == "1"}
	if err := kindleTemplate.Execute(w, data); err != nil {
		h.writeInternalError(w, r, err)
	}
}

func (h Handler) handleKindleDownload(w http.ResponseWriter, r *http.Request) {
	values, err := h.settings.Current(r.Context())
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	h.handleDownloadWith(w, r, func(ctx context.Context, id string) (string, library.File, error) {
		return h.library.DownloadKindleFile(ctx, id, values.KindleShowEPUB)
	})
}
