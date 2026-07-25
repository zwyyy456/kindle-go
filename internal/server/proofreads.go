package server

import (
	"net/http"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/proofread"
)

func (h Handler) handleCandidateRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/candidates/")
	if r.Method != http.MethodPost || !strings.HasSuffix(path, "/decision") {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSuffix(path, "/decision")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	run, err := h.proofreads.Decide(r.Context(), id, proofread.DecisionRequest{Decision: r.Form.Get("decision"), Replacement: r.Form.Get("replacement")})
	if run.ID == "" {
		http.NotFound(w, r)
		return
	}
	message := "candidate decision saved"
	if err != nil {
		message = "decision failed: " + err.Error()
	}
	http.Redirect(w, r, "/books/"+run.BookID+"/proofreads/"+run.ID+"?message="+urlMessage(message), http.StatusSeeOther)
}

func (h Handler) handleProofreadRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/proofreads/")
	if r.Method != http.MethodPost || !strings.HasSuffix(path, "/revisions") {
		http.NotFound(w, r)
		return
	}
	runID := strings.TrimSuffix(path, "/revisions")
	if runID == "" || strings.Contains(runID, "/") {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	created, err := h.proofreads.CreateRevision(r.Context(), runID, r.Form.Get("confirm_unresolved") == "1")
	if err != nil {
		run, ok, runErr := h.proofreads.Run(r.Context(), runID)
		if runErr == nil && ok {
			http.Redirect(w, r, "/books/"+run.BookID+"/proofreads/"+runID+"?message="+urlMessage("revision failed: "+err.Error()), http.StatusSeeOther)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/books/"+created.BookID+"?message=revision+task+queued", http.StatusSeeOther)
}

func (h Handler) handleProofreadReview(w http.ResponseWriter, r *http.Request, bookID, runID string) {
	page, ok, err := h.proofreads.Review(r.Context(), bookID, runID, proofread.ReviewQuery{Filter: r.URL.Query().Get("filter"), Page: parseInt(r.URL.Query().Get("page"))})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	data := proofreadPageData{
		BookID: bookID, Run: proofreadRunView{ID: page.Run.ID, Format: strings.ToUpper(page.Run.Format), Model: page.Run.Model, CompletedAt: page.Run.CompletedAt.Format("2006-01-02 15:04")},
		Candidates: page.Candidates, Filter: page.Filter, Page: page.Page, PreviousPage: page.Page - 1, NextPage: page.Page + 1,
		HasPrevious: page.HasPrevious, HasNext: page.HasNext, Total: page.Total, Unresolved: page.Unresolved, Message: r.URL.Query().Get("message"),
	}
	if err := proofreadTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h Handler) handleCreateProofread(w http.ResponseWriter, r *http.Request, bookID string) {
	if _, err := h.proofreads.Create(r.Context(), bookID); err != nil {
		http.Redirect(w, r, "/books/"+bookID+"?message="+urlMessage("proofread failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/books/"+bookID+"?message=proofread+task+queued", http.StatusSeeOther)
}
