package database

import (
	"context"

	"github.com/gotd/td/constant"
	"gorm.io/gorm"
)

func (user *User) WatchChat(ctx context.Context, chat WatchChat) error {
	if len(user.WatchChats) == 0 {
		user.WatchChats = make([]WatchChat, 0)
	}

	user.WatchChats = append(user.WatchChats, chat)
	return db.WithContext(ctx).Save(user.WatchChats).Error
}

func (user *User) UnwatchChat(ctx context.Context, chatID int64) error {
	result := db.WithContext(ctx).Unscoped().Where("chat_id IN ? AND user_id = ?", watchChatIDCandidates(chatID), user.ID).Delete(&WatchChat{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (user *User) UnwatchChatConfig(ctx context.Context, chatID int64, watch WatchChat) error {
	result := watchChatConfigQuery(db.WithContext(ctx).Unscoped(), user.ID, chatID, watch).Delete(&WatchChat{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (user *User) WatchingChat(ctx context.Context, chatID int64) (bool, error) {
	var count int64
	err := db.WithContext(ctx).Model(&WatchChat{}).Where("chat_id IN ? AND user_id = ?", watchChatIDCandidates(chatID), user.ID).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (user *User) WatchingChatConfig(ctx context.Context, chatID int64, watch WatchChat) (bool, error) {
	var count int64
	err := watchChatConfigQuery(db.WithContext(ctx).Model(&WatchChat{}), user.ID, chatID, watch).Count(&count).Error
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

func watchChatConfigQuery(tx *gorm.DB, userID uint, chatID int64, watch WatchChat) *gorm.DB {
	return tx.Where(
		"chat_id IN ? AND user_id = ? AND filter = ? AND group_mode = ? AND group_window_seconds = ? AND group_max = ?",
		watchChatIDCandidates(chatID),
		userID,
		watch.Filter,
		watch.GroupMode,
		watch.GroupWindowSeconds,
		watch.GroupMax,
	)
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
