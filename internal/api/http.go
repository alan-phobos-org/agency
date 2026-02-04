package api

import (
	"encoding/json"
	"net/http"
)

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteError writes a JSON error response with the given code and message.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, map[string]string{
		"error":   code,
		"message": message,
	})
}

// DecodeJSON decodes JSON from the request body into v.
// Returns true on success, false on error (and writes error response).
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, ErrorValidation, "Invalid JSON: "+err.Error())
		return false
	}
	return true
}

// CurrentTask represents info about a running task (used in status responses).
type CurrentTask struct {
	ID            string `json:"id"`
	StartedAt     string `json:"started_at"`
	PromptPreview string `json:"prompt_preview"`
}
