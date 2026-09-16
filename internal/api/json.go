package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// maxJSONBytes caps the size of an incoming JSON request body (LGF §3.4).
const maxJSONBytes = 1_048_576 // 1 MiB

// writeJSON writes data as JSON. When envelope is non-empty the payload is
// wrapped in a single-key object keyed by envelope (spec §6.2's {"data": ...});
// an empty envelope writes the payload as-is (e.g. GET /healthz).
func writeJSON(w http.ResponseWriter, status int, data any, envelope string) {
	if envelope != "" {
		data = map[string]any{envelope: data}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// readJSON decodes the request body into dst. It caps the body size via
// http.MaxBytesReader (LGF §3.4) and rejects unknown fields and trailing JSON
// values via json.Decoder.DisallowUnknownFields plus a second decode
// (LGF §4.1–4.3). On failure it writes the error and returns false.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}

	// Reject a request body carrying more than one JSON value (LGF §4.3).
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeJSONError(w, http.StatusBadRequest, "request body must contain a single JSON object")
		return false
	}

	return true
}
