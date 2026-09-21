package batchtfile

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

type partialBatchStorage struct {
	err error
}

func (*partialBatchStorage) Init(context.Context, storconfig.StorageConfig) error { return nil }
func (*partialBatchStorage) Type() storenum.StorageType                           { return storenum.Local }
func (*partialBatchStorage) Name() string                                         { return "partial" }
func (*partialBatchStorage) Exists(context.Context, string) bool                  { return false }
func (*partialBatchStorage) Save(context.Context, io.Reader, string) error        { return nil }
func (s *partialBatchStorage) SaveBatch(context.Context, []storagetypes.BatchItem) error {
	return s.err
}
func (s *partialBatchStorage) SaveBatchWithProgress(
	_ context.Context,
	_ []storagetypes.BatchItem,
	_ func(index int, uploaded, total int64),
) error {
	return s.err
}

func TestPartialBatchSaveMarksOnlySkippedItems(t *testing.T) {
	useProgressRegressionLocale(t)
	saveErr := &storagetypes.BatchSaveError{Failures: []storagetypes.BatchSaveFailure{{
		Index: 1,
		Path:  "bad.bin",
		Err:   errors.New("invalid Telegram media"),
	}}}
	stor := &partialBatchStorage{err: saveErr}
	task := newProgressRegressionTask(nil,
		progressRegressionFile{"good-a", 10},
		progressRegressionFile{"bad", 20},
		progressRegressionFile{"good-b", 30},
	)
	elems := []*TaskElement{
		{ID: "good-a", Storage: stor},
		{ID: "bad", Storage: stor},
		{ID: "good-b", Storage: stor},
	}
	items := []storagetypes.BatchItem{
		{Reader: bytes.NewReader(make([]byte, 10)), StoragePath: "good-a.bin", Size: 10},
		{Reader: bytes.NewReader(make([]byte, 20)), StoragePath: "bad.bin", Size: 20},
		{Reader: bytes.NewReader(make([]byte, 30)), StoragePath: "good-b.bin", Size: 30},
	}

	if err := task.saveBatchItems(t.Context(), elems, items); err != nil {
		t.Fatalf("saveBatchItems() returned a fatal error: %v", err)
	}
	got := task.Items()
	want := []ItemPhase{ItemPhaseCompleted, ItemPhaseFailed, ItemPhaseCompleted}
	for index := range want {
		if got[index].Phase != want[index] {
			t.Errorf("item %d phase = %v, want %v", index, got[index].Phase, want[index])
		}
	}
	if !strings.Contains(got[1].Error, "invalid Telegram media") {
		t.Fatalf("skipped item error = %q", got[1].Error)
	}

	message := buildBatchDoneMessage(task, nil, nil)
	if message.Err != nil {
		t.Fatalf("buildBatchDoneMessage() failed: %v", message.Err)
	}
	assertProgressRegressionContains(t, message.Text, "⚠️ 处理完成", "成功: 2", "已跳过: 1", "bad.bin")
}
