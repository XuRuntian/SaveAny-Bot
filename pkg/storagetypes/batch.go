package storagetypes

import "io"

// BatchItem describes one seekable file in a logical batch storage operation.
type BatchItem struct {
	Reader      io.ReadSeeker
	StoragePath string
	Size        int64

	// SourceGroupKey is empty for standalone source messages.
	SourceGroupKey string
	// RequireGroup prevents a grouped save from silently degrading to
	// standalone files. Backends may reject items that cannot preserve the
	// requested grouping relationship.
	RequireGroup bool
	Caption      string
	// PreserveCaption distinguishes an intentionally empty source caption from
	// the storage backend's default caption.
	PreserveCaption bool
}
