package api

import (
	"encoding/json"
	"net/http"
)

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v interface{}) {
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

// CurrentTask represents info about a running task (used in status responses).
type CurrentTask struct {
	ID            string `json:"id"`
	StartedAt     string `json:"started_at"`
	PromptPreview string `json:"prompt_preview"`
}
