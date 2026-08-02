package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/flashdict/kindle2flashdict/internal/library"
)

func (h Handler) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("fileID")
	_, file, err := h.library.DownloadFile(r.Context(), fileID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.library.DeleteFile(r.Context(), fileID); err != nil {
		h.redirectActionError(w, r, "/books/"+file.BookID, err, "File deletion failed. Refresh the page and try again.")
		return
	}
	http.Redirect(w, r, "/books/"+file.BookID+"?message=file+deleted", http.StatusSeeOther)
}

func (h Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	h.handleDownloadWith(w, r, h.library.DownloadFile)
}

func (h Handler) handleDownloadWith(w http.ResponseWriter, r *http.Request, resolve func(context.Context, string) (string, library.File, error)) {
	path, file, err := resolve(r.Context(), r.PathValue("fileID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.DisplayName))
	h.logFile("download", file)
	http.ServeFile(w, r, path)
}

func (h Handler) logFile(event string, file library.File) {
	if h.Logger == nil {
		return
	}
	h.Logger.Printf("file event=%s file_id=%s book_id=%s role=%s format=%s size_bytes=%d", event, file.ID, file.BookID, file.Role, file.Format, file.Size)
}
