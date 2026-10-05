package sdk

import (
	"encoding/json"
	"errors"
)

// SafeRetry is an explicit guarantee that repeating this configured action is safe.
type SafeRetry interface{ RetrySafe(json.RawMessage) bool }

type ErrorClass string

const (
	ErrorPermanent     ErrorClass = "permanent"
	ErrorTransient     ErrorClass = "transient"
	ErrorUnknownResult ErrorClass = "unknown_result"
)

type ExecutionError struct {
	Class ErrorClass
	Err   error
}

func (e *ExecutionError) Error() string { return e.Err.Error() }
func (e *ExecutionError) Unwrap() error { return e.Err }
func ClassifyError(err error) ErrorClass {
	var classified *ExecutionError
	if errors.As(err, &classified) {
		return classified.Class
	}
	return ErrorPermanent
}
