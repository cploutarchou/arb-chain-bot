package screener

import (
	"errors"
	"fmt"
)

// ErrInvalid wraps validation failures (callers map it to 400).
var ErrInvalid = errors.New("screener: invalid settings")

// ErrNoChange rejects an Apply whose payload equals the current version.
var ErrNoChange = errors.New("screener: no changes against current version")

// ErrNotFound reports an unknown settings version, rule, template or
// event.
var ErrNotFound = errors.New("screener: not found")

// ErrStaleVersion rejects an Apply whose caller-supplied parent_version
// no longer matches the active version (optimistic concurrency); mirrors
// platform.ErrStaleVersion exactly (design D6/D11: the same
// parent_version pattern applies to every versioned document in this
// codebase).
var ErrStaleVersion = errors.New("screener: stale parent_version")

// StaleVersionError is the concrete error ErrStaleVersion wraps; use
// errors.As to recover Current for the response body.
type StaleVersionError struct {
	Current int64
}

func (e *StaleVersionError) Error() string {
	return fmt.Sprintf("%s: current version is %d", ErrStaleVersion, e.Current)
}

func (e *StaleVersionError) Is(target error) bool { return target == ErrStaleVersion }
