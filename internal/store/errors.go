package store

import "errors"

// Deterministic extraction failures. They are cached, so a scanned or legacy file is parsed once.
var (
	ErrUnsupported = errors.New("unsupported format")
	ErrNeedsOCR    = errors.New("needs OCR")
	ErrEncrypted   = errors.New("encrypted document")
	ErrMalformed   = errors.New("malformed document")
	ErrLimit       = errors.New("limit exceeded")
)

var failureCodes = []struct {
	code string
	kind error
}{
	{"unsupported", ErrUnsupported}, {"ocr", ErrNeedsOCR}, {"encrypted", ErrEncrypted}, {"malformed", ErrMalformed}, {"limit", ErrLimit},
}

type typedError struct {
	kind    error
	message string
}

func (err *typedError) Error() string { return err.message }
func (err *typedError) Unwrap() error { return err.kind }

func fail(kind error, message string) error { return &typedError{kind, message} }

// failureCode returns the cache code of a deterministic failure, or "" when the error may not be cached.
func failureCode(err error) string {
	var typed *typedError
	if !errors.As(err, &typed) {
		return ""
	}
	for _, failure := range failureCodes {
		if errors.Is(typed.kind, failure.kind) {
			return failure.code
		}
	}
	return ""
}

func failureFromCode(code, message string) error {
	for _, failure := range failureCodes {
		if failure.code == code {
			return &typedError{failure.kind, message}
		}
	}
	return nil
}
