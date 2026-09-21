package shortcut

import "testing"

func TestMarkLinkedMessageSeen(t *testing.T) {
	seen := make(map[linkedMessageKey]struct{})
	tests := []struct {
		name      string
		chatID    int64
		messageID int
		want      bool
	}{
		{name: "first message", chatID: 1, messageID: 10, want: true},
		{name: "duplicate message", chatID: 1, messageID: 10, want: false},
		{name: "same id in another chat", chatID: 2, messageID: 10, want: true},
		{name: "different id in same chat", chatID: 1, messageID: 11, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := markLinkedMessageSeen(seen, tt.chatID, tt.messageID); got != tt.want {
				t.Fatalf("markLinkedMessageSeen() = %v, want %v", got, tt.want)
			}
		})
	}
}
