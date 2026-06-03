// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"net/http"
)

// errorEnvelope is the standardized v0.25 (#537) error response shape
// for every 4xx/5xx returned by the HTTP gateway.
//
//	{
//	  "error": {
//	    "code":    "<machine-readable code>",
//	    "message": "<human-readable description>",
//	    "details": { … optional structured fields … }
//	  }
//	}
//
// Replaces the v0.22.1 transitional `{"error": "<text>"}` shape. The
// breaking change is intentional and called out in the v0.25 CHANGELOG:
// generated SDKs against the OpenAPI Error component need a regen, and
// hand-written clients reading `body.error` as a string need to read
// `body.error.message` instead.
//
// Standard codes (see CHANGELOG for full list):
//   - bad_request       — malformed input, missing required field
//   - invalid_json_body — request body is not well-formed JSON (#714)
//   - not_found         — resource doesn't exist
//   - unauthorized      — missing/invalid bearer token
//   - method_not_allowed
//   - internal_error    — server-side failure (5xx)
//   - value_too_long    — input exceeded a per-field size cap
type errorEnvelope struct {
	Error errEnvelopeBody `json:"error"`
}

type errEnvelopeBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// writeError sets Content-Type + status, then writes the standard
// envelope. Code is the machine-readable identifier (snake_case);
// message is the human-readable description; details is optional
// structured context (e.g. {"field": "id", "max_length": 256}).
//
// Sets Content-Type to application/json — many handlers already set
// it via the dispatcher, but writeError is the single source of truth
// for error responses, so it sets it again to be safe. Idempotent.
func writeError(w http.ResponseWriter, status int, code, message string, details ...map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	body := errorEnvelope{Error: errEnvelopeBody{Code: code, Message: message}}
	if len(details) > 0 && len(details[0]) > 0 {
		body.Error.Details = details[0]
	}
	json.NewEncoder(w).Encode(body)
}
