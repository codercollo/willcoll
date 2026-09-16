package api

import (
	"encoding/json"
	"net/http"
)

// writeJSONError writes the enveloped JSON error response used across the API
// (spec §6.2: {"error": ...}). message may be a string or a map[string]string
// of field-level validation errors.
func writeJSONError(w http.ResponseWriter, status int, message any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
}

// serverErrorResponse writes the generic 500 response. The handler logs the
// underlying error with its own context before calling this (LGF §4).
func serverErrorResponse(w http.ResponseWriter) {
	writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
}

// notFoundResponse writes a 404 response (LGF §4).
func notFoundResponse(w http.ResponseWriter) {
	writeJSONError(w, http.StatusNotFound, "the requested resource could not be found")
}

// badRequestResponse writes a 400 response carrying err's message (LGF §4).
func badRequestResponse(w http.ResponseWriter, err error) {
	writeJSONError(w, http.StatusBadRequest, err.Error())
}

// failedValidationResponse writes a 422 response whose "error" value is the
// field -> message map (LGF §4).
func failedValidationResponse(w http.ResponseWriter, errors map[string]string) {
	writeJSONError(w, http.StatusUnprocessableEntity, errors)
}
