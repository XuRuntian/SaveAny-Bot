package handlers

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/msgelem"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/shortcut"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/tcbdata"
	"github.com/krau/SaveAny-Bot/storage"
	"gorm.io/gorm"
)

func handleAddCallback(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	dataParts := strings.Split(string(update.CallbackQuery.Data), " ")
	if len(dataParts) < 2 {
		return fmt.Errorf("invalid callback data: %q", update.CallbackQuery.Data)
	}
	dataid := dataParts[1]
	queryID := update.CallbackQuery.GetQueryID()
	msgID := update.CallbackQuery.GetMsgID()
	userID := update.CallbackQuery.GetUserID()
	logger.Debug("Handling add callback", "user_id", userID, "msg_id", msgID, "data_id", dataid)
	data, err := shortcut.GetCallbackDataWithAnswer[tcbdata.Add](ctx, update, dataid)
	if err != nil {
		logger.Warn("Add callback data is invalid or expired", "user_id", userID, "msg_id", msgID, "data_id", dataid, "error", err)
		return err
	}
	ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
		QueryID:   queryID,
		CacheTime: 1,
	})
	logger.Debug(
		"Loaded add callback data",
		"user_id", userID,
		"msg_id", msgID,
		"task_type", data.TaskType,
		"storage", data.SelectedStorName,
		"files", len(data.Files),
		"as_batch", data.AsBatch,
		"set_dir", data.SettedDir,
	)

	selectedStorage, err := storage.GetStorageByUserIDAndName(ctx, userID, data.SelectedStorName)
	if err != nil {
		logger.Errorf("Failed to get storage: %s", err)
		ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgCommonErrorGetStorageFailed, map[string]any{
			"Error": err.Error(),
		})))
		return dispatcher.EndGroups
	}
	dirs, err := database.GetDirsByUserChatIDAndStorageName(ctx, userID, data.SelectedStorName)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get user directories: %w", err)
	}

	if !data.SettedDir && len(dirs) != 0 {
		// ask for directory selection
		markup, err := msgelem.BuildSetDirMarkupForAdd(dirs, dataid)
		if err != nil {
			logger.Errorf("Failed to build directory keyboard: %s", err)
			ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgCommonErrorBuildStorageSelectKeyboardFailed, map[string]any{
				"Error": err.Error(),
			})))
			return dispatcher.EndGroups
		}
		logger.Debug("Add callback asks for directory selection", "user_id", userID, "msg_id", msgID, "storage", data.SelectedStorName, "dir_count", len(dirs))
		ctx.EditMessage(userID, &tg.MessagesEditMessageRequest{
			ID:          update.CallbackQuery.GetMsgID(),
			Message:     i18n.T(i18nk.BotMsgCommonPromptSelectDir, nil),
			ReplyMarkup: markup,
		})
		return dispatcher.EndGroups
	}

	dirPath := ""
	if data.DirID != 0 {
		dir, err := database.GetDirByID(ctx, data.DirID)
		if err != nil {
			ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgCommonErrorGetDirFailed, map[string]any{
				"Error": err.Error(),
			})))
			return dispatcher.EndGroups
		}
		dirPath = dir.Path
	} else if data.SelectedDirPath != "" {
		dirPath = data.SelectedDirPath
	}

	switch data.TaskType {
	case tasktype.TaskTypeTgfiles:
		logger.Debug("Creating Telegram file task from add callback", "user_id", userID, "msg_id", msgID, "files", len(data.Files), "as_batch", data.AsBatch, "storage", selectedStorage.Name())
		if data.AsBatch {
			return shortcut.CreateAndAddBatchTGFileTaskWithEdit(ctx, userID, selectedStorage, dirPath, data.Files, msgID, data.ConflictStrategy)
		}
		return shortcut.CreateAndAddTGFileTaskWithEdit(ctx, userID, selectedStorage, dirPath, data.Files[0], msgID, data.ConflictStrategy)
	case tasktype.TaskTypeTphpics:
		logger.Debug("Creating Telegraph task from add callback", "user_id", userID, "msg_id", msgID, "storage", selectedStorage.Name())
		return shortcut.CreateAndAddtelegraphWithEdit(ctx, userID, data.TphPageNode, data.TphDirPath, data.TphPics, selectedStorage, msgID)
	case tasktype.TaskTypeParseditem:
		logger.Debug("Creating parsed item task from add callback", "user_id", userID, "msg_id", msgID, "storage", selectedStorage.Name())
		if len(data.ParsedItem.Resources) > 1 {
			dirPath = path.Join(dirPath, fsutil.NormalizePathname(data.ParsedItem.Title))
		}
		shortcut.CreateAndAddParsedTaskWithEdit(ctx, selectedStorage, dirPath, data.ParsedItem, msgID, userID)
	case tasktype.TaskTypeDirectlinks:
		logger.Debug("Creating direct links task from add callback", "user_id", userID, "msg_id", msgID, "storage", selectedStorage.Name(), "links", len(data.DirectLinks))
		shortcut.CreateAndAddDirectTaskWithEdit(ctx, selectedStorage, dirPath, data.DirectLinks, msgID, userID)
	case tasktype.TaskTypeAria2:
		logger.Debug("Creating Aria2 task from add callback", "user_id", userID, "msg_id", msgID, "storage", selectedStorage.Name(), "uris", len(data.Aria2URIs))
		client := GetAria2Client()
		if client == nil {
			ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgAria2ErrorAria2ClientInitFailed, map[string]any{
				"Error": "aria2 client not initialized",
			})))
			return dispatcher.EndGroups
		}
		shortcut.CreateAndAddAria2TaskWithEdit(ctx, selectedStorage, dirPath, data.Aria2URIs, client, msgID, userID)
	case tasktype.TaskTypeYtdlp:
		logger.Debug("Creating yt-dlp task from add callback", "user_id", userID, "msg_id", msgID, "storage", selectedStorage.Name(), "urls", len(data.YtdlpURLs))
		shortcut.CreateAndAddYtdlpTaskWithEdit(ctx, selectedStorage, dirPath, data.YtdlpURLs, data.YtdlpFlags, msgID, userID)
	case tasktype.TaskTypeTransfer:
		logger.Debug("Creating transfer task from add callback", "user_id", userID, "msg_id", msgID, "storage", selectedStorage.Name(), "files", len(data.TransferFiles))
		return handleTransferCallback(ctx, userID, selectedStorage, dirPath, data, msgID)
	default:
		return fmt.Errorf("unexcept task type: %s", data.TaskType)
	}
	return dispatcher.EndGroups
}
