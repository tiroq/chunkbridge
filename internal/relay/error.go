package relay

import "fmt"

// RelayError is a structured error returned by Session.SendRequest when the
// exit node sends a FrameERROR frame. It carries an error code and a suggested
// HTTP status so the proxy can return an appropriate response without string
// matching or guessing.
type RelayError struct {
	Code       string
	HTTPStatus int
	Message    string
}

// Error implements the error interface.
func (e *RelayError) Error() string {
	return fmt.Sprintf("relay: %s: %s", e.Code, e.Message)
}
