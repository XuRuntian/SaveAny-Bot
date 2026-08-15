package database

import (
	"reflect"
	"testing"
)

func TestWatchChatIDCandidates(t *testing.T) {
	tests := []struct {
		name   string
		chatID int64
		want   []int64
	}{
		{
			name:   "plain chat id",
			chatID: 3688621340,
			want:   []int64{3688621340},
		},
		{
			name:   "tdlib channel id includes plain id",
			chatID: -1003688621340,
			want:   []int64{-1003688621340, 3688621340},
		},
		{
			name:   "tdlib chat id includes plain id",
			chatID: -3688621340,
			want:   []int64{-3688621340, 3688621340},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := watchChatIDCandidates(tt.chatID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("watchChatIDCandidates(%d) = %v, want %v", tt.chatID, got, tt.want)
			}
		})
	}
}
