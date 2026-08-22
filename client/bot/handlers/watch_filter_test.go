package handlers

import (
	"testing"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func TestWatchFilterMatches(t *testing.T) {
	tests := []struct {
		name     string
		filter   string
		msgText  string
		senderID int64
		want     bool
		wantErr  bool
	}{
		{
			name:    "message regex matches",
			filter:  "msgre:.*hello.*",
			msgText: "hello world",
			want:    true,
		},
		{
			name:    "message regex does not match",
			filter:  "msgre:.*hello.*",
			msgText: "world",
			want:    false,
		},
		{
			name:     "sender id matches",
			filter:   "from:123456789",
			senderID: 123456789,
			want:     true,
		},
		{
			name:     "sender id does not match",
			filter:   "from:123456789",
			senderID: 987654321,
			want:     false,
		},
		{
			name:     "sender id matches list",
			filter:   "from:123456789,987654321",
			senderID: 987654321,
			want:     true,
		},
		{
			name:     "sender id matches spaced list",
			filter:   "from:123456789, 987654321",
			senderID: 987654321,
			want:     true,
		},
		{
			name:     "sender id does not match list",
			filter:   "from:123456789,987654321",
			senderID: 111111111,
			want:     false,
		},
		{
			name:    "regex may contain colon",
			filter:  "msgre:https://example.com/.*",
			msgText: "https://example.com/video",
			want:    true,
		},
		{
			name:    "unsupported filter returns error",
			filter:  "unknown:value",
			wantErr: true,
		},
		{
			name:    "invalid regex returns error",
			filter:  "msgre:*",
			wantErr: true,
		},
		{
			name:    "empty sender id in list returns error",
			filter:  "from:123456789,",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := watchFilterMatches(tt.filter, tt.msgText, tt.senderID)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("watchFilterMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseWatchOptions(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		wantFilter        string
		wantGroupMode     string
		wantWindowSeconds int
		wantMax           int
		wantErr           bool
	}{
		{
			name: "empty options",
		},
		{
			name:       "sender filter numeric list",
			args:       []string{"from:123456789,987654321"},
			wantFilter: "from:123456789,987654321",
		},
		{
			name:              "soft group default options",
			args:              []string{"group:soft"},
			wantGroupMode:     watchGroupSoft,
			wantWindowSeconds: defaultWatchSoftGroupWindowSeconds,
			wantMax:           defaultWatchSoftGroupMax,
		},
		{
			name:              "soft group explicit options",
			args:              []string{"from:123456789", "group:soft", "window:8s", "max:5"},
			wantFilter:        "from:123456789",
			wantGroupMode:     watchGroupSoft,
			wantWindowSeconds: 8,
			wantMax:           5,
		},
		{
			name:    "window requires soft group",
			args:    []string{"window:8s"},
			wantErr: true,
		},
		{
			name:    "duplicate filters rejected",
			args:    []string{"from:123456789", "msgre:hello"},
			wantErr: true,
		},
		{
			name:    "oversized max rejected",
			args:    []string{"group:soft", "max:11"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWatchOptions(nil, tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.filter != tt.wantFilter {
				t.Fatalf("filter = %q, want %q", got.filter, tt.wantFilter)
			}
			if got.groupMode != tt.wantGroupMode {
				t.Fatalf("groupMode = %q, want %q", got.groupMode, tt.wantGroupMode)
			}
			if got.groupWindowSeconds != tt.wantWindowSeconds {
				t.Fatalf("groupWindowSeconds = %d, want %d", got.groupWindowSeconds, tt.wantWindowSeconds)
			}
			if got.groupMax != tt.wantMax {
				t.Fatalf("groupMax = %d, want %d", got.groupMax, tt.wantMax)
			}
		})
	}
}

func TestWatchMediaGroupHandlerSeparatesGroupKeys(t *testing.T) {
	handler := &watchMediaGroupHandler{
		groups: make(map[watchMediaGroupKey][]tfile.TGFileMessage),
		timers: make(map[watchMediaGroupKey]*time.Timer),
	}
	key := watchMediaGroupKey{chatID: 1, userID: 2, senderID: 3, groupID: 4}
	otherSender := watchMediaGroupKey{chatID: 1, userID: 2, senderID: 9, groupID: 4}
	otherWatch := watchMediaGroupKey{chatID: 1, userID: 2, watchID: 7, senderID: 3, groupID: 4}
	results := make(chan []tfile.TGFileMessage, 3)

	handler.addFile(key, fakeWatchFile("a.jpg", 1), time.Millisecond, 0, func(files []tfile.TGFileMessage) {
		results <- files
	})
	handler.addFile(key, fakeWatchFile("b.jpg", 2), time.Millisecond, 0, func(files []tfile.TGFileMessage) {
		results <- files
	})
	handler.addFile(otherSender, fakeWatchFile("c.jpg", 3), time.Millisecond, 0, func(files []tfile.TGFileMessage) {
		results <- files
	})
	handler.addFile(otherWatch, fakeWatchFile("d.jpg", 4), time.Millisecond, 0, func(files []tfile.TGFileMessage) {
		results <- files
	})

	gotSizes := map[int]int{}
	for range 3 {
		select {
		case files := <-results:
			gotSizes[len(files)]++
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for media group callbacks")
		}
	}
	if gotSizes[2] != 1 || gotSizes[1] != 2 {
		t.Fatalf("callback batch sizes = %v, want one 2-file batch and two 1-file batches", gotSizes)
	}
}

func TestWatchMediaGroupHandlerFlushesAtMaxItems(t *testing.T) {
	handler := &watchMediaGroupHandler{
		groups: make(map[watchMediaGroupKey][]tfile.TGFileMessage),
		timers: make(map[watchMediaGroupKey]*time.Timer),
	}
	key := watchMediaGroupKey{chatID: 1, userID: 2, senderID: 3, soft: true}
	results := make(chan []tfile.TGFileMessage, 1)

	handler.addFile(key, fakeWatchFile("a.jpg", 1), time.Hour, 2, func(files []tfile.TGFileMessage) {
		results <- files
	})
	handler.addFile(key, fakeWatchFile("b.jpg", 2), time.Hour, 2, func(files []tfile.TGFileMessage) {
		results <- files
	})

	select {
	case files := <-results:
		if len(files) != 2 {
			t.Fatalf("callback batch size = %d, want 2", len(files))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for max-item callback")
	}
}

type fakeTGFileMessage struct {
	name string
	msg  *tg.Message
}

func fakeWatchFile(name string, messageID int) tfile.TGFileMessage {
	return &fakeTGFileMessage{name: name, msg: &tg.Message{ID: messageID}}
}

func (f *fakeTGFileMessage) Location() tg.InputFileLocationClass { return nil }
func (f *fakeTGFileMessage) Dler() downloader.Client             { return nil }
func (f *fakeTGFileMessage) Size() int64                         { return 0 }
func (f *fakeTGFileMessage) Name() string                        { return f.name }
func (f *fakeTGFileMessage) SetName(name string)                 { f.name = name }
func (f *fakeTGFileMessage) Message() *tg.Message                { return f.msg }
