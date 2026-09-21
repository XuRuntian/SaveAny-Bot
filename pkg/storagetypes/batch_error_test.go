package storagetypes

import (
	"errors"
	"strings"
	"testing"
)

func TestBatchSaveErrorListsSkippedItems(t *testing.T) {
	err := (&BatchSaveError{Failures: []BatchSaveFailure{
		{Index: 1, Path: "/target/bad.mov", Err: errors.New("invalid media")},
		{Index: 3, Path: "photo.png", Err: errors.New("empty media")},
	}}).Error()
	for _, want := range []string{"skipped 2 batch item(s)", "bad.mov", "photo.png"} {
		if !strings.Contains(err, want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}
