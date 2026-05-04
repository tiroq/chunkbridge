package protocol

import "encoding/json"

// Error code constants carried in FrameERROR payloads.
// The proxy maps these codes to HTTP statuses.
const (
	ErrCodePolicyDenied        = "policy_denied"
	ErrCodeBadRequest          = "bad_request"
	ErrCodeUpstreamUnavailable = "upstream_unavailable"
	ErrCodeUpstreamTimeout     = "upstream_timeout"
	ErrCodeResponseTooLarge    = "response_too_large"
	ErrCodeInternalError       = "internal_error"
)

// ErrorPayload is the JSON structure carried in a FrameERROR frame's Payload.
// It provides enough information for the proxy to return a meaningful HTTP
// response without exposing internal details.
type ErrorPayload struct {
	Code       string `json:"code"`
	HTTPStatus int    `json:"http_status"`
	Message    string `json:"message"`
}

// MarshalErrorPayload encodes an ErrorPayload into bytes.
func MarshalErrorPayload(code string, httpStatus int, message string) ([]byte, error) {
	return json.Marshal(ErrorPayload{Code: code, HTTPStatus: httpStatus, Message: message})
}

// UnmarshalErrorPayload decodes bytes into an ErrorPayload.
func UnmarshalErrorPayload(data []byte) (ErrorPayload, error) {
	var ep ErrorPayload
	err := json.Unmarshal(data, &ep)
	return ep, err
}
