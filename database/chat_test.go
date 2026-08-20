package database

import (
	"context"
	"reflect"
	"testing"

	"gorm.io/gorm"
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

func TestWatchChatUsesCompatibleChatIDCandidates(t *testing.T) {
	oldDB := db
	testDB, err := gorm.Open(GetDialect("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := testDB.AutoMigrate(&User{}, &WatchChat{}); err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}
	db = testDB
	t.Cleanup(func() {
		db = oldDB
	})

	ctx := context.Background()
	user := &User{ChatID: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := user.WatchChat(ctx, WatchChat{UserID: user.ID, ChatID: 3688621340}); err != nil {
		t.Fatalf("WatchChat failed: %v", err)
	}

	watching, err := user.WatchingChat(ctx, -1003688621340)
	if err != nil {
		t.Fatalf("WatchingChat failed: %v", err)
	}
	if !watching {
		t.Fatal("WatchingChat did not match compatible channel ID")
	}

	if err := user.UnwatchChat(ctx, -1003688621340); err != nil {
		t.Fatalf("UnwatchChat failed: %v", err)
	}
	watching, err = user.WatchingChat(ctx, 3688621340)
	if err != nil {
		t.Fatalf("WatchingChat after unwatch failed: %v", err)
	}
	if watching {
		t.Fatal("WatchingChat still matched after compatible unwatch")
	}
}
