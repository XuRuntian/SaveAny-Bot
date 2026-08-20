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

func TestWatchMediaGroupHandlerSeparatesGroupKeys(t *testing.T) {
	handler := &watchMediaGroupHandler{
		groups: make(map[watchMediaGroupKey][]tfile.TGFileMessage),
		timers: make(map[watchMediaGroupKey]*time.Timer),
	}
	key := watchMediaGroupKey{chatID: 1, userID: 2, senderID: 3, groupID: 4}
	otherSender := watchMediaGroupKey{chatID: 1, userID: 2, senderID: 9, groupID: 4}
	results := make(chan []tfile.TGFileMessage, 2)

	handler.addFile(key, fakeWatchFile("a.jpg", 1), time.Millisecond, func(files []tfile.TGFileMessage) {
		results <- files
	})
	handler.addFile(key, fakeWatchFile("b.jpg", 2), time.Millisecond, func(files []tfile.TGFileMessage) {
		results <- files
	})
	handler.addFile(otherSender, fakeWatchFile("c.jpg", 3), time.Millisecond, func(files []tfile.TGFileMessage) {
		results <- files
	})

	gotSizes := map[int]int{}
	for range 2 {
		select {
		case files := <-results:
			gotSizes[len(files)]++
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for media group callbacks")
		}
	}
	if gotSizes[2] != 1 || gotSizes[1] != 1 {
		t.Fatalf("callback batch sizes = %v, want one 2-file batch and one 1-file batch", gotSizes)
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
