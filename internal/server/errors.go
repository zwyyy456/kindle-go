package server

import (
	"errors"
	"net/http"

	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/proofread"
	appsettings "github.com/flashdict/kindle2flashdict/internal/settings"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func (h Handler) writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	h.logInternalError(r, err)
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}

func (h Handler) writeActionError(w http.ResponseWriter, r *http.Request, err error, status int, fallback string) {
	message, userFacing := h.actionError(r, err, fallback)
	if !userFacing {
		status = http.StatusInternalServerError
	}
	http.Error(w, message, status)
}

func (h Handler) redirectActionError(w http.ResponseWriter, r *http.Request, location string, err error, fallback string) {
	http.Redirect(w, r, location+urlMessageSeparator(h.actionErrorMessage(r, err, fallback)), http.StatusSeeOther)
}

func (h Handler) actionErrorMessage(r *http.Request, err error, fallback string) string {
	message, _ := h.actionError(r, err, fallback)
	return message
}

func (h Handler) actionError(r *http.Request, err error, fallback string) (string, bool) {
	if err == nil {
		return fallback, true
	}
	if message, ok := safeActionMessage(err); ok {
		return message, true
	}
	h.logInternalError(r, err)
	return fallback, false
}

func safeActionMessage(err error) (string, bool) {
	var validationErr *appsettings.ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Error(), true
	}
	var generationErr *generation.UserError
	if errors.As(err, &generationErr) {
		return generationErr.Error(), true
	}
	var proofreadErr *proofread.UserError
	if errors.As(err, &proofreadErr) {
		return proofreadErr.Error(), true
	}
	var importErr *library.ImportError
	if errors.As(err, &importErr) {
		return importErr.Code + ": " + importErr.Error(), true
	}
	var duplicateErr *library.DuplicateTokenError
	if errors.As(err, &duplicateErr) {
		return duplicateErr.Error(), true
	}
	var deletionErr *library.DeletionError
	if errors.As(err, &deletionErr) {
		return deletionErr.Error(), true
	}
	var conflictErr *task.ConflictError
	if errors.As(err, &conflictErr) {
		return conflictErr.Error(), true
	}
	var codexErr *proofread.CodexError
	if errors.As(err, &codexErr) {
		switch codexErr.Code {
		case "codex_not_found":
			return "Codex CLI was not found. See Settings diagnostics.", true
		case "codex_invalid_output":
			return "Codex returned invalid structured output. See Settings diagnostics.", true
		case "codex_unavailable":
			return "Codex is unavailable. See Settings diagnostics.", true
		}
	}
	return "", false
}

func (h Handler) logInternalError(r *http.Request, err error) {
	if h.Logger != nil && err != nil {
		h.Logger.Printf("http error method=%s path=%s: %v", r.Method, r.URL.Path, err)
	}
}

func urlMessageSeparator(message string) string {
	return "?message=" + urlMessage(message)
}
