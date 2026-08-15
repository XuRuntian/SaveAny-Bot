package database

import (
	"context"

	"github.com/gotd/td/constant"
)

func (user *User) WatchChat(ctx context.Context, chat WatchChat) error {
	if len(user.WatchChats) == 0 {
		user.WatchChats = make([]WatchChat, 0)
	}

	user.WatchChats = append(user.WatchChats, chat)
	return db.WithContext(ctx).Save(user.WatchChats).Error
}

func (user *User) UnwatchChat(ctx context.Context, chatID int64) error {
	var watchChat WatchChat
	err := db.WithContext(ctx).Where("chat_id = ? AND user_id = ?", chatID, user.ID).First(&watchChat).Error
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Unscoped().Delete(&watchChat).Error
}

func (user *User) WatchingChat(ctx context.Context, chatID int64) (bool, error) {
	var count int64
	err := db.WithContext(ctx).Model(&WatchChat{}).Where("chat_id = ? AND user_id = ?", chatID, user.ID).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func GetWatchChatsByChatID(ctx context.Context, chatID int64) ([]*WatchChat, error) {
	var watchChats []*WatchChat
	err := db.WithContext(ctx).Where("chat_id IN ?", watchChatIDCandidates(chatID)).Find(&watchChats).Error
	if err != nil {
		return nil, err
	}
	return watchChats, nil
}

func watchChatIDCandidates(chatID int64) []int64 {
	candidates := make([]int64, 0, 2)
	add := func(id int64) {
		if id == 0 {
			return
		}
		for _, candidate := range candidates {
			if candidate == id {
				return
			}
		}
		candidates = append(candidates, id)
	}

	add(chatID)
	tdlibID := constant.TDLibPeerID(chatID)
	if tdlibID.IsChat() || tdlibID.IsChannel() {
		add(tdlibID.ToPlain())
	}

	return candidates
}
