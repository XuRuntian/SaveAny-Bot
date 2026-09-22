package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

func TestPlanMediaGroups(t *testing.T) {
	tests := []struct {
		name         string
		items        []batchMediaItem
		wantSizes    []int
		wantRejected int
	}{
		{
			name: "same source album",
			items: []batchMediaItem{
				albumItem("a", 1, true),
				albumItem("a", 1, true),
			},
			wantSizes: []int{2},
		},
		{
			name: "different source albums",
			items: []batchMediaItem{
				albumItem("a", 1, true),
				albumItem("b", 1, true),
			},
			wantSizes: []int{1, 1},
		},
		{
			name: "ungrouped messages",
			items: []batchMediaItem{
				albumItem("", 1, true),
				albumItem("", 1, true),
			},
			wantSizes: []int{1, 1},
		},
		{
			name: "different target chats",
			items: []batchMediaItem{
				albumItem("a", 1, true),
				albumItem("a", 2, true),
			},
			wantSizes: []int{1, 1},
		},
		{
			name: "ineligible media does not bridge albums",
			items: []batchMediaItem{
				albumItem("a", 1, true),
				albumItem("a", 1, false),
				albumItem("a", 1, true),
			},
			wantSizes: []int{1, 1, 1},
		},
		{
			name:      "reliable album size",
			items:     repeatedAlbumItems(11),
			wantSizes: []int{5, 5, 1},
		},
		{
			name:      "strict merge avoids trailing singleton",
			items:     repeatedStrictAlbumItems(11),
			wantSizes: []int{5, 4, 2},
		},
		{
			name: "strict merge skips ineligible and preserves later media",
			items: []batchMediaItem{
				strictAlbumItem("a", 1, true),
				strictAlbumItem("a", 1, false),
				strictAlbumItem("a", 1, true),
			},
			wantSizes:    []int{2},
			wantRejected: 1,
		},
		{
			name:         "strict merge rejects a standalone remainder",
			items:        []batchMediaItem{strictAlbumItem("a", 1, true)},
			wantRejected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups, rejected := planMediaGroups(tt.items)
			if len(groups) != len(tt.wantSizes) {
				t.Fatalf("got %d groups, want %d", len(groups), len(tt.wantSizes))
			}
			for i, want := range tt.wantSizes {
				if got := len(groups[i]); got != want {
					t.Errorf("group %d has %d items, want %d", i, got, want)
				}
			}
			if len(rejected) != tt.wantRejected {
				t.Errorf("got %d rejected items, want %d", len(rejected), tt.wantRejected)
			}
		})
	}
}

func TestAlbumRecoverySplit(t *testing.T) {
	tests := []struct {
		count        int
		requireGroup bool
		wantAt       int
		wantOK       bool
	}{
		{count: 1},
		{count: 2, wantAt: 1, wantOK: true},
		{count: 3, wantAt: 1, wantOK: true},
		{count: 4, wantAt: 2, wantOK: true},
		{count: 5, wantAt: 2, wantOK: true},
		{count: 2, requireGroup: true},
		{count: 3, requireGroup: true},
		{count: 4, requireGroup: true, wantAt: 2, wantOK: true},
		{count: 5, requireGroup: true, wantAt: 2, wantOK: true},
	}
	for _, tt := range tests {
		at, ok := albumRecoverySplit(tt.count, tt.requireGroup)
		if at != tt.wantAt || ok != tt.wantOK {
			t.Errorf("albumRecoverySplit(%d, %t) = (%d, %t), want (%d, %t)", tt.count, tt.requireGroup, at, ok, tt.wantAt, tt.wantOK)
		}
	}
}

func TestStrictMediaGroupsNeverProduceSingletons(t *testing.T) {
	for count := 2; count <= 73; count++ {
		groups, rejected := planMediaGroups(repeatedStrictAlbumItems(count))
		if len(rejected) != 0 {
			t.Fatalf("count %d rejected %d valid items", count, len(rejected))
		}
		seen := 0
		for _, group := range groups {
			if len(group) < 2 || len(group) > reliableAlbumItems {
				t.Fatalf("count %d produced invalid group size %d", count, len(group))
			}
			seen += len(group)
		}
		if seen != count {
			t.Fatalf("count %d planned %d items", count, seen)
		}
	}
}

func TestStrictMediaGroupNeverFallsBackToSingleSave(t *testing.T) {
	item := strictAlbumItem("manual", 1, true)
	item.index = 7
	item.item.StoragePath = "video.mp4"

	err := new(Telegram).saveMediaGroup(t.Context(), nil, []batchMediaItem{item}, nil)
	var partial *storagetypes.BatchSaveError
	if !errors.As(err, &partial) {
		t.Fatalf("saveMediaGroup() error = %v, want BatchSaveError", err)
	}
	if len(partial.Failures) != 1 || partial.Failures[0].Index != 7 {
		t.Fatalf("failures = %#v, want item index 7", partial.Failures)
	}
}

func TestIsRecoverableAlbumMediaError(t *testing.T) {
	for _, message := range []string{
		tg.ErrMediaEmpty,
		tg.ErrMediaFileInvalid,
		tg.ErrMediaGroupedInvalid,
		tg.ErrMediaInvalid,
		tg.ErrMediaTypeInvalid,
	} {
		err := fmt.Errorf("send album: %w", tgerr.New(400, message))
		if !isRecoverableAlbumMediaError(err) {
			t.Errorf("error %q was not recoverable", message)
		}
	}
	if isRecoverableAlbumMediaError(tgerr.New(500, "INTERNAL")) {
		t.Fatal("transient server error was treated as invalid media")
	}
}

func TestIsSkippableBatchItemError(t *testing.T) {
	for _, message := range []string{
		tg.ErrMediaEmpty,
		tg.ErrDocumentInvalid,
		tg.ErrFileContentTypeInvalid,
		tg.ErrFileEmtpy,
		tg.ErrPhotoInvalid,
		tg.ErrPhotoInvalidDimensions,
		tg.ErrVideoFileInvalid,
	} {
		if !isSkippableBatchItemError(tgerr.New(400, message)) {
			t.Errorf("error %q was not skippable", message)
		}
	}
	for _, err := range []error{
		context.Canceled,
		tgerr.New(500, "INTERNAL"),
		tgerr.New(400, "CHAT_WRITE_FORBIDDEN"),
	} {
		if isSkippableBatchItemError(err) {
			t.Errorf("non-media error %q was treated as skippable", err)
		}
	}
}

func TestMediaCaption(t *testing.T) {
	empty := ""
	original := "original caption"
	tests := []struct {
		name     string
		override *string
		wantLen  int
	}{
		{name: "filename fallback", wantLen: 1},
		{name: "preserve empty source caption", override: &empty, wantLen: 0},
		{name: "preserve source caption", override: &original, wantLen: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(mediaCaption("file.jpg", tt.override)); got != tt.wantLen {
				t.Fatalf("got %d caption options, want %d", got, tt.wantLen)
			}
		})
	}
}

func TestUploadedMessageMediaToInput(t *testing.T) {
	tests := []struct {
		name  string
		media tg.MessageMediaClass
		check func(t *testing.T, input tg.InputMediaClass)
	}{
		{
			name: "photo",
			media: &tg.MessageMediaPhoto{
				Photo:      &tg.Photo{ID: 11, AccessHash: 12, FileReference: []byte{13}},
				TTLSeconds: 14,
			},
			check: func(t *testing.T, input tg.InputMediaClass) {
				photo, ok := input.(*tg.InputMediaPhoto)
				if !ok {
					t.Fatalf("unexpected photo input: %#v", input)
				}
				photoID, ok := photo.ID.(*tg.InputPhoto)
				if !ok || photoID.ID != 11 || photo.TTLSeconds != 14 {
					t.Fatalf("unexpected photo input: %#v", input)
				}
			},
		},
		{
			name: "document",
			media: &tg.MessageMediaDocument{
				Document:   &tg.Document{ID: 21, AccessHash: 22, FileReference: []byte{23}},
				TTLSeconds: 24,
			},
			check: func(t *testing.T, input tg.InputMediaClass) {
				document, ok := input.(*tg.InputMediaDocument)
				if !ok {
					t.Fatalf("unexpected document input: %#v", input)
				}
				documentID, ok := document.ID.(*tg.InputDocument)
				if !ok || documentID.ID != 21 || document.TTLSeconds != 24 {
					t.Fatalf("unexpected document input: %#v", input)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, err := uploadedMessageMediaToInput(tt.media)
			if err != nil {
				t.Fatalf("uploadedMessageMediaToInput() failed: %v", err)
			}
			tt.check(t, input)
		})
	}

	if _, err := uploadedMessageMediaToInput(&tg.MessageMediaEmpty{}); err == nil {
		t.Fatal("empty media was accepted")
	}
}

func TestInspectBatchItemRewindsBeforeMimetypeDetection(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	reader := bytes.NewReader(data)
	if _, err := reader.Seek(4, io.SeekStart); err != nil {
		t.Fatalf("failed to set initial reader offset: %v", err)
	}

	mediaItem, err := new(Telegram).inspectBatchItem(nil, storagetypes.BatchItem{
		Reader:      reader,
		StoragePath: "photo.png",
		Size:        int64(len(data)),
	})
	if err != nil {
		t.Fatalf("inspectBatchItem returned an error: %v", err)
	}
	if !mediaItem.albumEligible {
		t.Fatal("albumEligible = false, want true for PNG input")
	}
	if offset, err := reader.Seek(0, io.SeekCurrent); err != nil {
		t.Fatalf("failed to get final reader offset: %v", err)
	} else if offset != 0 {
		t.Fatalf("reader offset = %d, want 0", offset)
	}
}

func albumItem(group string, chatID int64, eligible bool) batchMediaItem {
	return batchMediaItem{
		item:          storagetypes.BatchItem{SourceGroupKey: group},
		chatID:        chatID,
		albumEligible: eligible,
	}
}

func repeatedAlbumItems(count int) []batchMediaItem {
	items := make([]batchMediaItem, count)
	for i := range items {
		items[i] = albumItem("a", 1, true)
	}
	return items
}

func strictAlbumItem(group string, chatID int64, eligible bool) batchMediaItem {
	item := albumItem(group, chatID, eligible)
	item.item.RequireGroup = true
	return item
}

func repeatedStrictAlbumItems(count int) []batchMediaItem {
	items := make([]batchMediaItem, count)
	for i := range items {
		items[i] = strictAlbumItem("a", 1, true)
	}
	return items
}
