package handlers

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/mediautil"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/ruleutil"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	coretfile "github.com/krau/SaveAny-Bot/core/tasks/tfile"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/fnamest"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
	"github.com/rs/xid"
)

const (
	watchFilterMsgRegex = "msgre"
	watchFilterFrom     = "from"
)

func handleWatchCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	args := strings.Split(update.EffectiveMessage.Text, " ")
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchHelpText)), nil)
		return dispatcher.EndGroups
	}
	userChatID := update.GetUserChat().GetID()
	user, err := database.GetUserByChatID(ctx, userChatID)
	if err != nil {
		logger.Errorf("Failed to get user: %s", err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorGetUserFailed)), nil)
		return dispatcher.EndGroups
	}
	if user.DefaultStorage == "" {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorDefaultStorageNotSet)), nil)
		return dispatcher.EndGroups
	}
	chatArg := args[1]
	chatID, err := tgutil.ParseChatID(ctx, chatArg)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidIdOrUsername, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	watching, err := user.WatchingChat(ctx, chatID)
	if err != nil {
		logger.Errorf("Failed to check if user is watching chat %d: %s", chatID, err)
		return dispatcher.EndGroups
	}
	if watching {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchInfoAlreadyWatchingChat)), nil)
		return dispatcher.EndGroups
	}
	filter := ""
	if len(args) > 2 {
		filterArg := strings.Join(args[2:], " ")
		filterType, filterData, ok := strings.Cut(filterArg, ":")
		filterType = strings.TrimSpace(filterType)
		if !ok || filterType == "" || strings.TrimSpace(filterData) == "" {
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorFilterFormatInvalid)), nil)
			return dispatcher.EndGroups
		}
		switch filterType {
		case watchFilterMsgRegex:
			_, err := regexp.Compile(filterData)
			if err != nil {
				ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidRegex, map[string]any{"Error": err.Error()})), nil)
				return dispatcher.EndGroups
			}
			filter = filterType + ":" + filterData
		case watchFilterFrom:
			senderIDs, err := resolveWatchFilterSenderIDs(ctx, filterData)
			if err != nil {
				ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidIdOrUsername, map[string]any{"Error": err.Error()})), nil)
				return dispatcher.EndGroups
			}
			filter = filterType + ":" + formatWatchFilterSenderIDs(senderIDs)
		default:
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorFilterTypeUnsupported)), nil)
			return dispatcher.EndGroups
		}
	}
	if err := user.WatchChat(ctx, database.WatchChat{
		UserID: user.ID,
		ChatID: chatID,
		Filter: filter,
	}); err != nil {
		logger.Errorf("Failed to watch chat %d: %s", chatID, err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorWatchChatFailed, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchInfoWatchChatStarted, map[string]any{"Chat": chatArg})), nil)
	return dispatcher.EndGroups
}

func resolveWatchFilterSenderIDs(ctx *ext.Context, idsOrUsernames string) ([]int64, error) {
	resolveCtx := ctx
	if userCtx := userclient.GetCtx(); userCtx != nil {
		resolveCtx = userCtx
	}

	parts := strings.Split(idsOrUsernames, ",")
	senderIDs := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		idOrUsername := strings.TrimSpace(part)
		if idOrUsername == "" {
			return nil, fmt.Errorf("empty sender in from filter")
		}
		senderID, err := tgutil.ParseChatID(resolveCtx, idOrUsername)
		if err != nil {
			return nil, err
		}
		if senderID == 0 {
			return nil, fmt.Errorf("sender ID is zero")
		}
		if _, ok := seen[senderID]; ok {
			continue
		}
		seen[senderID] = struct{}{}
		senderIDs = append(senderIDs, senderID)
	}
	if len(senderIDs) == 0 {
		return nil, fmt.Errorf("no sender IDs resolved")
	}
	return senderIDs, nil
}

func formatWatchFilterSenderIDs(senderIDs []int64) string {
	parts := make([]string, 0, len(senderIDs))
	for _, senderID := range senderIDs {
		parts = append(parts, strconv.FormatInt(senderID, 10))
	}
	return strings.Join(parts, ",")
}

func handleLswatchCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	userChatID := update.GetUserChat().GetID()
	user, err := database.GetUserByChatID(ctx, userChatID)
	if err != nil {
		logger.Errorf("Failed to get user: %s", err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorGetUserFailed)), nil)
		return dispatcher.EndGroups
	}
	chats := user.WatchChats
	if len(chats) == 0 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchInfoWatchListEmpty)), nil)
		return dispatcher.EndGroups
	}
	var sb strings.Builder
	sb.WriteString(i18n.T(i18nk.BotMsgWatchInfoWatchListHeader))
	for _, chat := range chats {
		sb.WriteString("- ")
		sb.WriteString(fmt.Sprintf("%d", chat.ChatID))
		if chat.Filter != "" {
			sb.WriteString(i18n.T(i18nk.BotMsgWatchInfoWatchListFilterPrefix))
			sb.WriteString(chat.Filter)
			sb.WriteString(")")
		}
		sb.WriteString("\n")
	}
	ctx.Reply(update, ext.ReplyTextString(sb.String()), nil)
	return dispatcher.EndGroups
}

func handleUnwatchCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	args := strings.Split(update.EffectiveMessage.Text, " ")
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorUnwatchNoChatProvided)), nil)
		return dispatcher.EndGroups
	}
	userChatID := update.GetUserChat().GetID()
	user, err := database.GetUserByChatID(ctx, userChatID)
	if err != nil {
		logger.Errorf("Failed to get user: %s", err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorGetUserFailed)), nil)
		return dispatcher.EndGroups
	}
	chatArg := args[1]
	chatID, err := tgutil.ParseChatID(ctx, chatArg)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidIdOrUsername, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	if err := user.UnwatchChat(ctx, chatID); err != nil {
		logger.Errorf("Failed to unwatch chat %d: %s", chatID, err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorUnwatchChatFailed, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchInfoWatchChatStopped, map[string]any{"Chat": chatArg})), nil)
	return dispatcher.EndGroups
}

type watchMediaGroupHandler struct {
	groups map[int64]map[uint][]tfile.TGFileMessage // chatID -> userID -> files
	timers map[int64]map[uint]*time.Timer
	mu     sync.Mutex
}

var watchMediaGroupMgr = &watchMediaGroupHandler{
	groups: make(map[int64]map[uint][]tfile.TGFileMessage),
	timers: make(map[int64]map[uint]*time.Timer),
}

func (w *watchMediaGroupHandler) addFile(chatID int64, userID uint, file tfile.TGFileMessage, timeout time.Duration, callback func([]tfile.TGFileMessage)) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.groups[chatID] == nil {
		w.groups[chatID] = make(map[uint][]tfile.TGFileMessage)
	}
	if w.timers[chatID] == nil {
		w.timers[chatID] = make(map[uint]*time.Timer)
	}

	if timer, exists := w.timers[chatID][userID]; exists {
		timer.Stop()
	}

	w.groups[chatID][userID] = append(w.groups[chatID][userID], file)

	w.timers[chatID][userID] = time.AfterFunc(timeout, func() {
		w.mu.Lock()
		files := w.groups[chatID][userID]
		delete(w.groups[chatID], userID)
		delete(w.timers[chatID], userID)
		if len(w.groups[chatID]) == 0 {
			delete(w.groups, chatID)
		}
		if len(w.timers[chatID]) == 0 {
			delete(w.timers, chatID)
		}
		w.mu.Unlock()

		if len(files) > 0 {
			callback(files)
		}
	})
}

func watchFilterMatches(filter, msgText string, senderID int64) (bool, error) {
	filterType, filterData, ok := strings.Cut(filter, ":")
	if !ok || filterType == "" || filterData == "" {
		return false, fmt.Errorf("invalid filter format")
	}
	switch filterType {
	case watchFilterMsgRegex:
		ok, err := regexp.MatchString(filterData, msgText)
		if err != nil {
			return false, fmt.Errorf("invalid regex filter: %w", err)
		}
		return ok, nil
	case watchFilterFrom:
		return watchSenderIDMatches(filterData, senderID)
	default:
		return false, fmt.Errorf("unsupported filter type %s", filterType)
	}
}

func watchSenderIDMatches(filterData string, senderID int64) (bool, error) {
	for _, part := range strings.Split(filterData, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return false, fmt.Errorf("empty sender ID")
		}
		expectedSenderID, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return false, fmt.Errorf("invalid sender ID: %w", err)
		}
		if senderID != 0 && senderID == expectedSenderID {
			return true, nil
		}
	}
	return false, nil
}

func listenMediaMessageEvent(ch chan userclient.MediaMessageEvent) {
	if userclient.GetCtx() == nil {
		return
	}
	logger := log.FromContext(userclient.GetCtx())
	for event := range ch {
		logger.Debug("Received media message event", "chat_id", event.ChatID, "file_name", event.File.Name())
		ctx := event.Ctx
		file := event.File
		chats, err := database.GetWatchChatsByChatID(ctx, event.ChatID)
		if err != nil {
			logger.Errorf("Failed to get watch chats for chat ID %d: %v", event.ChatID, err)
			continue
		}
		msgText := event.File.Message().GetMessage()
		senderID := int64(0)
		if fromID, ok := event.File.Message().GetFromID(); ok {
			senderID = tgutil.ChatIdFromPeer(fromID)
		}
		for _, chat := range chats {
			if chat.Filter != "" {
				matched, err := watchFilterMatches(chat.Filter, msgText, senderID)
				if err != nil {
					logger.Warnf("Invalid or unsupported filter %q in chat %d: %v, skipping", chat.Filter, chat.ChatID, err)
					continue
				}
				if !matched {
					continue
				}
			}
			user, err := database.GetUserByID(ctx, chat.UserID)
			if err != nil {
				logger.Errorf("Failed to get user by ID %d: %v", chat.UserID, err)
				continue
			}
			if user.DefaultStorage == "" {
				logger.Warnf("User %d has no default storage set, skipping media message handling", chat.UserID)
				continue
			}
			stor, err := storage.GetStorageByUserIDAndName(ctx, user.ChatID, user.DefaultStorage)
			if err != nil {
				logger.Errorf("Failed to get storage by user ID %d and name %s: %v", user.ChatID, user.DefaultStorage, err)
				continue
			}
			// Resolve the default directory path from user.DefaultDir
			var defaultDirPath string
			if user.DefaultDir != 0 {
				dir, err := database.GetDirByID(ctx, user.DefaultDir)
				if err != nil {
					logger.Warnf("Failed to get default dir for user %d: %v, using root", user.ChatID, err)
				} else {
					defaultDirPath = dir.Path
				}
			}
			switch user.FilenameStrategy {
			case fnamest.Message.String():
				file.SetName(tgutil.GenFileNameFromMessage(*file.Message()))
			case fnamest.Template.String():
				if user.FilenameTemplate == "" {
					logger.Warnf("Empty filename template for user %d, using default filename", user.ChatID)
					break
				}
				message := file.Message()
				tmpl, err := template.New("filename").Parse(user.FilenameTemplate)
				if err != nil {
					logger.Errorf("Failed to parse filename template for user %d: %s", user.ChatID, err)
					break
				}
				data := mediautil.BuildFilenameTemplateData(message)
				var sb strings.Builder
				err = tmpl.Execute(&sb, data)
				if err != nil {
					log.FromContext(ctx).Errorf("failed to execute filename template: %s", err)
					break
				}
				file.SetName(sb.String())
			}

			// Check if this is a media group and if rules specify NEW-FOR-ALBUM
			groupID, isGroup := file.Message().GetGroupedID()
			needAlbumHandling := false
			if isGroup && groupID != 0 && user.ApplyRule && user.Rules != nil {
				_, _, matchedDirPath := ruleutil.ApplyRule(ctx, user.Rules, ruleutil.NewInput(file))
				needAlbumHandling = matchedDirPath.NeedNewForAlbum()
			}

			if needAlbumHandling {
				// For media groups with NEW-FOR-ALBUM rule, collect all files of the same group
				watchMediaGroupMgr.addFile(event.ChatID, user.ID, file, time.Duration(max(config.C().Telegram.MediaGroupTimeout, 1))*time.Second, func(files []tfile.TGFileMessage) {
					processWatchMediaGroup(ctx, user, stor, defaultDirPath, files)
				})
				continue
			}

			// Process single file or media group without album folder creation
			dirPath := defaultDirPath
			if user.ApplyRule && user.Rules != nil {
				matched, matchedStorageName, matchedDirPath := ruleutil.ApplyRule(ctx, user.Rules, ruleutil.NewInput(file))
				if !matched {
					goto startCreateTask
				}
				dirPath = matchedDirPath.String()
				if matchedStorageName.Usable() {
					stor, err = storage.GetStorageByUserIDAndName(ctx, user.ChatID, matchedStorageName.String())
					if err != nil {
						logger.Errorf("Failed to get storage by user ID and name: %s", err)
						continue
					}
				}
			}
		startCreateTask:
			storagePath := path.Join(dirPath, file.Name())
			injectCtx := tgutil.ExtWithContext(ctx.Context, ctx)
			taskid := xid.New().String()
			task, err := coretfile.NewTGFileTask(taskid, injectCtx, file, stor, storagePath, nil)
			if err != nil {
				logger.Errorf("create task failed: %s", err)
				continue
			}
			if err := core.AddTask(injectCtx, task); err != nil {
				logger.Errorf("add task failed: %s", err)
				continue
			}
			logger.Infof("Added media message task for user %d in chat %d: %s", chat.UserID, event.ChatID, file.Name())
		}
	}
}

func processWatchMediaGroup(ctx *ext.Context, user *database.User, stor storage.Storage, dirPath string, files []tfile.TGFileMessage) {
	logger := log.FromContext(ctx)
	if len(files) == 0 {
		return
	}

	useRule := user.ApplyRule && user.Rules != nil

	applyRule := func(file tfile.TGFileMessage) (string, ruleutil.MatchedDirPath) {
		if !useRule {
			return stor.Name(), ruleutil.MatchedDirPath(dirPath)
		}
		matched, storName, dirP := ruleutil.ApplyRule(ctx, user.Rules, ruleutil.NewInput(file))
		if !matched {
			return stor.Name(), ruleutil.MatchedDirPath(dirPath)
		}
		storname := storName.String()
		if !storName.Usable() {
			storname = stor.Name()
		}
		return storname, dirP
	}

	type albumFile struct {
		file    tfile.TGFileMessage
		storage storage.Storage
		dirPath string
	}
	albumFiles := make(map[int64][]albumFile)

	// Collect files by group ID
	for _, file := range files {
		storName, ruleDirPath := applyRule(file)
		fileStor := stor
		if storName != stor.Name() && storName != "" {
			var err error
			fileStor, err = storage.GetStorageByUserIDAndName(ctx, user.ChatID, storName)
			if err != nil {
				logger.Errorf("Failed to get storage by user ID and name: %s", err)
				continue
			}
		}

		groupId, isGroup := file.Message().GetGroupedID()
		if !isGroup || groupId == 0 {
			logger.Warnf("File %s is not in a group, skipping", file.Name())
			continue
		}

		// Use the effective dirPath: if rule returns NEW-FOR-ALBUM sentinel, fall back to the
		// base dirPath passed in (which is defaultDirPath from the caller).
		effectiveDirPath := string(ruleDirPath)
		if ruleDirPath.NeedNewForAlbum() {
			effectiveDirPath = dirPath
		}

		if _, ok := albumFiles[groupId]; !ok {
			albumFiles[groupId] = make([]albumFile, 0)
		}
		albumFiles[groupId] = append(albumFiles[groupId], albumFile{
			file:    file,
			storage: fileStor,
			dirPath: effectiveDirPath,
		})
	}

	// Process album files with folder creation
	injectCtx := tgutil.ExtWithContext(ctx.Context, ctx)
	totalTasks := 0
	for groupID, afiles := range albumFiles {
		if len(afiles) <= 1 {
			continue
		}

		// Use first file's name (without extension) as album folder name
		albumDir := strings.TrimSuffix(path.Base(afiles[0].file.Name()), path.Ext(afiles[0].file.Name()))
		albumStor := afiles[0].storage

		logger.Infof("Creating album folder for group %d: %s with %d files", groupID, albumDir, len(afiles))

		for _, af := range afiles {
			afstorPath := path.Join(af.dirPath, albumDir, af.file.Name())
			taskid := xid.New().String()
			task, err := coretfile.NewTGFileTask(taskid, injectCtx, af.file, albumStor, afstorPath, nil)
			if err != nil {
				logger.Errorf("create task failed for album file: %s", err)
				continue
			}
			if err := core.AddTask(injectCtx, task); err != nil {
				logger.Errorf("add task failed: %s", err)
				continue
			}
			totalTasks++
		}
	}
	logger.Infof("Added %d watch media tasks for user %d", totalTasks, user.ChatID)
}
