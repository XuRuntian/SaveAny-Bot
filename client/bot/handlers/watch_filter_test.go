package handlers

import "testing"

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
