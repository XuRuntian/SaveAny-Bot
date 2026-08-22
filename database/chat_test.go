package database

import (
	"context"
	"reflect"
	"strings"
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
	useWatchChatTestDB(t)

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

func TestWatchingChatConfigAllowsDifferentFiltersInSameChat(t *testing.T) {
	useWatchChatTestDB(t)

	ctx := context.Background()
	user := &User{ChatID: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	first := WatchChat{UserID: user.ID, ChatID: 3688621340, Filter: "from:111"}
	if err := user.WatchChat(ctx, first); err != nil {
		t.Fatalf("WatchChat failed: %v", err)
	}

	watching, err := user.WatchingChatConfig(ctx, -1003688621340, first)
	if err != nil {
		t.Fatalf("WatchingChatConfig failed: %v", err)
	}
	if !watching {
		t.Fatal("WatchingChatConfig did not match identical watch config")
	}

	differentSender := WatchChat{UserID: user.ID, ChatID: 3688621340, Filter: "from:222"}
	watching, err = user.WatchingChatConfig(ctx, -1003688621340, differentSender)
	if err != nil {
		t.Fatalf("WatchingChatConfig for different sender failed: %v", err)
	}
	if watching {
		t.Fatal("WatchingChatConfig matched different sender filter")
	}
}

func TestUnwatchChatRemovesAllCompatibleRows(t *testing.T) {
	useWatchChatTestDB(t)

	ctx := context.Background()
	user := &User{ChatID: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	watches := []WatchChat{
		{UserID: user.ID, ChatID: 3688621340, Filter: "from:111"},
		{UserID: user.ID, ChatID: 3688621340, Filter: "from:222"},
	}
	for _, watch := range watches {
		if err := user.WatchChat(ctx, watch); err != nil {
			t.Fatalf("WatchChat failed: %v", err)
		}
	}

	if err := user.UnwatchChat(ctx, -1003688621340); err != nil {
		t.Fatalf("UnwatchChat failed: %v", err)
	}

	watching, err := user.WatchingChat(ctx, 3688621340)
	if err != nil {
		t.Fatalf("WatchingChat failed: %v", err)
	}
	if watching {
		t.Fatal("WatchingChat still matched after unwatching all compatible rows")
	}
}

func TestUnwatchChatConfigRemovesOnlyMatchingConfig(t *testing.T) {
	useWatchChatTestDB(t)

	ctx := context.Background()
	user := &User{ChatID: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	first := WatchChat{UserID: user.ID, ChatID: 3688621340, Filter: "from:111"}
	second := WatchChat{UserID: user.ID, ChatID: 3688621340, Filter: "from:222"}
	for _, watch := range []WatchChat{first, second} {
		if err := user.WatchChat(ctx, watch); err != nil {
			t.Fatalf("WatchChat failed: %v", err)
		}
	}

	if err := user.UnwatchChatConfig(ctx, -1003688621340, first); err != nil {
		t.Fatalf("UnwatchChatConfig failed: %v", err)
	}
	watching, err := user.WatchingChatConfig(ctx, -1003688621340, first)
	if err != nil {
		t.Fatalf("WatchingChatConfig first failed: %v", err)
	}
	if watching {
		t.Fatal("first watch config still matched after unwatch")
	}
	watching, err = user.WatchingChatConfig(ctx, -1003688621340, second)
	if err != nil {
		t.Fatalf("WatchingChatConfig second failed: %v", err)
	}
	if !watching {
		t.Fatal("second watch config was removed unexpectedly")
	}
}

func useWatchChatTestDB(t *testing.T) {
	t.Helper()
	oldDB := db
	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	testDB, err := gorm.Open(GetDialect(dsn), &gorm.Config{})
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
}
