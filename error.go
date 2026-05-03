package shredstream

import "errors"

var (
	ErrTooShort       = errors.New("shred too short")
	ErrUnknownVariant = errors.New("unknown variant")
	ErrPayloadInvalid = errors.New("payload invalid")
)

type ParseError struct {
	Err error
}

func (e *ParseError) Error() string { return e.Err.Error() }
func (e *ParseError) Unwrap() error { return e.Err }
func (e *ParseError) Is(target error) bool {
	return errors.Is(e.Err, target)
}

func newParseError(err error) *ParseError { return &ParseError{Err: err} }

type DecodeError struct {
	Msg string
	Err error
}

func (e *DecodeError) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}
func (e *DecodeError) Unwrap() error { return e.Err }
