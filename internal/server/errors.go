package server

import "net/http"

func (h Handler) writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	if h.Logger != nil {
		h.Logger.Printf("http error method=%s path=%s: %v", r.Method, r.URL.Path, err)
	}
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}
