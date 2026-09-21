package storagetypes

import (
	"fmt"
	"path"
	"strings"
)

// BatchSaveFailure identifies one item that a batch-capable storage skipped.
type BatchSaveFailure struct {
	Index int
	Path  string
	Err   error
}

// BatchSaveError reports partial success. Callers may mark only the listed
// items as failed while treating the rest of the batch as completed.
type BatchSaveError struct {
	Failures []BatchSaveFailure
}

func (e *BatchSaveError) Error() string {
	if e == nil || len(e.Failures) == 0 {
		return "batch save completed with skipped items"
	}
	names := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		name := path.Base(failure.Path)
		if name == "." || name == "/" || name == "" {
			name = fmt.Sprintf("item %d", failure.Index+1)
		}
		names = append(names, name)
	}
	return fmt.Sprintf("skipped %d batch item(s): %s", len(e.Failures), strings.Join(names, ", "))
}
