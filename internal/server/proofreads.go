package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/proofread"
)

func (h Handler) handleCandidateDecision(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	run, err := h.proofreads.Decide(r.Context(), r.PathValue("candidateID"), proofread.DecisionRequest{Decision: r.Form.Get("decision"), Replacement: r.Form.Get("replacement")})
	if err != nil {
		if isCandidateNotFound(err) {
			http.NotFound(w, r)
			return
		}
		if run.ID == "" {
			h.writeInternalError(w, r, err)
			return
		}
		message, ok := safeActionMessage(err)
		if !ok {
			h.writeInternalError(w, r, err)
			return
		}
		http.Redirect(w, r, "/books/"+run.BookID+"/proofreads/"+run.ID+"?message="+urlMessage(message), http.StatusSeeOther)
		return
	}
	if run.ID == "" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/books/"+run.BookID+"/proofreads/"+run.ID+"?message="+urlMessage("candidate decision saved"), http.StatusSeeOther)
}

func isCandidateNotFound(err error) bool {
	var userErr *proofread.UserError
	return errors.As(err, &userErr) && userErr.Error() == "candidate not found"
}

func (h Handler) handleCreateRevision(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	created, err := h.proofreads.CreateRevision(r.Context(), runID, r.Form.Get("confirm_unresolved") == "1")
	if err != nil {
		run, ok, runErr := h.proofreads.Run(r.Context(), runID)
		if runErr == nil && ok {
			h.redirectActionError(w, r, "/books/"+run.BookID+"/proofreads/"+runID, err, "Revision task could not be queued. Resolve the review items and try again.")
			return
		}
		h.writeActionError(w, r, err, http.StatusBadRequest, "Revision task could not be queued. Refresh the review and try again.")
		return
	}
	http.Redirect(w, r, "/books/"+created.BookID+"?message=revision+task+queued", http.StatusSeeOther)
}

func (h Handler) handleProofreadReview(w http.ResponseWriter, r *http.Request) {
	bookID, runID := r.PathValue("bookID"), r.PathValue("runID")
	page, ok, err := h.proofreads.Review(r.Context(), bookID, runID, proofread.ReviewQuery{Filter: r.URL.Query().Get("filter"), Page: parseInt(r.URL.Query().Get("page"))})
	if err != nil {
		h.writeInternalError(w, r, err)
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
		h.writeInternalError(w, r, err)
	}
}

func (h Handler) handleCreateProofread(w http.ResponseWriter, r *http.Request) {
	bookID := r.PathValue("bookID")
	if _, err := h.proofreads.Create(r.Context(), bookID); err != nil {
		h.redirectActionError(w, r, "/books/"+bookID, err, "Proofreading task could not be queued. Check the book and try again.")
		return
	}
	http.Redirect(w, r, "/books/"+bookID+"?message=proofread+task+queued", http.StatusSeeOther)
}
