package types

import "errors"

// maskedError is what e.Err becomes when an option replaces the message the
// caller will read (ErrOptionWithHideErrMsg hides transport detail): the text
// is the replacement, but the original error stays reachable through Unwrap.
// Dropping it, as a bare errors.New did, made every hidden error opaque to
// errors.Is, so IsUpstreamFailure could not tell "the caller hung up"
// (context.Canceled) from "the upstream broke" and fed the circuit breaker
// the former as the latter.
type maskedError struct {
	msg   string
	cause error
}

func (m *maskedError) Error() string { return m.msg }
func (m *maskedError) Unwrap() error { return m.cause }

// applyOps runs the options and, if one swapped e.Err for an unrelated error,
// keeps the previous error as the cause behind the new text.
func applyOps(e *NewAPIError, ops []NewAPIErrorOptions) {
	orig := e.Err
	for _, op := range ops {
		op(e)
	}
	if orig != nil && e.Err != nil && !errors.Is(e.Err, orig) {
		e.Err = &maskedError{msg: e.Err.Error(), cause: orig}
	}
}
