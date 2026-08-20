package handlers

import (
	"fmt"
	"path"
	"regexp"
	"sort"
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
	"github.com/krau/SaveAny-Bot/core/tasks/batchtfile"
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
	watchGroupSoft      = "soft"
)

const (
	defaultWatchSoftGroupWindowSeconds = 8
	defaultWatchSoftGroupMax           = 10
	minWatchSoftGroupWindowSeconds     = 1
	maxWatchSoftGroupWindowSeconds     = 60
	minWatchSoftGroupMax               = 2
	maxWatchSoftGroupMax               = 10
)

type watchOptions struct {
	filter             string
	groupMode          string
	groupWindowSeconds int
	groupMax           int
}

func handleWatchCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	args := strings.Fields(update.EffectiveMessage.Text)
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
	options, err := parseWatchOptions(ctx, args[2:])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidIdOrUsername, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	if err := user.WatchChat(ctx, database.WatchChat{
		UserID:             user.ID,
		ChatID:             chatID,
		Filter:             options.filter,
		GroupMode:          options.groupMode,
		GroupWindowSeconds: options.groupWindowSeconds,
		GroupMax:           options.groupMax,
	}); err != nil {
		logger.Errorf("Failed to watch chat %d: %s", chatID, err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorWatchChatFailed, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchInfoWatchChatStarted, map[string]any{"Chat": chatArg})), nil)
	return dispatcher.EndGroups
}

func parseWatchOptions(ctx *ext.Context, args []string) (watchOptions, error) {
	options := watchOptions{}
	for _, arg := range args {
		optionType, optionData, ok := strings.Cut(arg, ":")
		optionType = strings.TrimSpace(optionType)
		optionData = strings.TrimSpace(optionData)
		if !ok || optionType == "" || optionData == "" {
			return options, fmt.Errorf("invalid watch option %q", arg)
		}
		switch optionType {
		case watchFilterMsgRegex:
			if options.filter != "" {
				return options, fmt.Errorf("only one watch filter is supported")
			}
			if _, err := regexp.Compile(optionData); err != nil {
				return options, fmt.Errorf("invalid regex filter: %w", err)
			}
			options.filter = optionType + ":" + optionData
		case watchFilterFrom:
			if options.filter != "" {
				return options, fmt.Errorf("only one watch filter is supported")
			}
			senderIDs, err := resolveWatchFilterSenderIDs(ctx, optionData)
			if err != nil {
				return options, err
			}
			options.filter = optionType + ":" + formatWatchFilterSenderIDs(senderIDs)
		case "group":
			if optionData != watchGroupSoft {
				return options, fmt.Errorf("unsupported group mode %q", optionData)
			}
			options.groupMode = watchGroupSoft
		case "window":
			seconds, err := parseWatchGroupWindowSeconds(optionData)
			if err != nil {
				return options, err
			}
			options.groupWindowSeconds = seconds
		case "max":
			maxItems, err := strconv.Atoi(optionData)
			if err != nil {
				return options, fmt.Errorf("invalid group max: %w", err)
			}
			if maxItems < minWatchSoftGroupMax || maxItems > maxWatchSoftGroupMax {
				return options, fmt.Errorf("group max must be between %d and %d", minWatchSoftGroupMax, maxWatchSoftGroupMax)
			}
			options.groupMax = maxItems
		default:
			return options, fmt.Errorf("unsupported watch option %q", optionType)
		}
	}
	if options.groupMode == "" {
		if options.groupWindowSeconds != 0 || options.groupMax != 0 {
			return options, fmt.Errorf("window/max require group:soft")
		}
		return options, nil
	}
	if options.groupWindowSeconds == 0 {
		options.groupWindowSeconds = defaultWatchSoftGroupWindowSeconds
	}
	if options.groupMax == 0 {
		options.groupMax = defaultWatchSoftGroupMax
	}
	return options, nil
}

func parseWatchGroupWindowSeconds(data string) (int, error) {
	if seconds, err := strconv.Atoi(data); err == nil {
		return validateWatchGroupWindowSeconds(seconds)
	}
	duration, err := time.ParseDuration(data)
	if err != nil {
		return 0, fmt.Errorf("invalid group window: %w", err)
	}
	seconds := int(duration / time.Second)
	if time.Duration(seconds)*time.Second != duration {
		return 0, fmt.Errorf("group window must be whole seconds")
	}
	return validateWatchGroupWindowSeconds(seconds)
}

func validateWatchGroupWindowSeconds(seconds int) (int, error) {
	if seconds < minWatchSoftGroupWindowSeconds || seconds > maxWatchSoftGroupWindowSeconds {
		return 0, fmt.Errorf("group window must be between %ds and %ds", minWatchSoftGroupWindowSeconds, maxWatchSoftGroupWindowSeconds)
	}
	return seconds, nil
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
		if chat.GroupMode == watchGroupSoft {
			sb.WriteString(fmt.Sprintf(" (group:%s window:%ds max:%d)", chat.GroupMode, watchGroupWindowSeconds(&chat), watchGroupMax(&chat)))
		}
		sb.WriteString("\n")
	}
	ctx.Reply(update, ext.ReplyTextString(sb.String()), nil)
	return dispatcher.EndGroups
}

func handleUnwatchCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	args := strings.Fields(update.EffectiveMessage.Text)
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

type watchMediaGroupKey struct {
	chatID   int64
	userID   uint
	senderID int64
	groupID  int64
	soft     bool
}

type watchMediaGroupHandler struct {
	groups map[watchMediaGroupKey][]tfile.TGFileMessage
	timers map[watchMediaGroupKey]*time.Timer
	mu     sync.Mutex
}

var watchMediaGroupMgr = &watchMediaGroupHandler{
	groups: make(map[watchMediaGroupKey][]tfile.TGFileMessage),
	timers: make(map[watchMediaGroupKey]*time.Timer),
}

func (w *watchMediaGroupHandler) addFile(
	key watchMediaGroupKey,
	file tfile.TGFileMessage,
	timeout time.Duration,
	maxItems int,
	callback func([]tfile.TGFileMessage),
) {
	w.mu.Lock()

	if timer, exists := w.timers[key]; exists {
		timer.Stop()
	}

	w.groups[key] = append(w.groups[key], file)
	if maxItems > 0 && len(w.groups[key]) >= maxItems {
		files := w.groups[key]
		delete(w.groups, key)
		delete(w.timers, key)
		w.mu.Unlock()
		if len(files) > 0 {
			callback(files)
		}
		return
	}

	w.timers[key] = time.AfterFunc(timeout, func() {
		w.mu.Lock()
		files := w.groups[key]
		delete(w.groups, key)
		delete(w.timers, key)
		w.mu.Unlock()

		if len(files) > 0 {
			callback(files)
		}
	})
	w.mu.Unlock()
}

func watchGroupWindowSeconds(chat *database.WatchChat) int {
	if chat.GroupWindowSeconds > 0 {
		return chat.GroupWindowSeconds
	}
	return defaultWatchSoftGroupWindowSeconds
}

func watchGroupMax(chat *database.WatchChat) int {
	if chat.GroupMax > 0 {
		return chat.GroupMax
	}
	return defaultWatchSoftGroupMax
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

			groupID, isGroup := file.Message().GetGroupedID()
			if isGroup && groupID != 0 {
				key := watchMediaGroupKey{
					chatID:   event.ChatID,
					userID:   user.ID,
					senderID: senderID,
					groupID:  groupID,
				}
				watchMediaGroupMgr.addFile(key, file, time.Duration(max(config.C().Telegram.MediaGroupTimeout, 1))*time.Second, 0, func(files []tfile.TGFileMessage) {
					processWatchMediaGroup(ctx, user, stor, defaultDirPath, files)
				})
				continue
			}
			if chat.GroupMode == watchGroupSoft {
				key := watchMediaGroupKey{
					chatID:   event.ChatID,
					userID:   user.ID,
					senderID: senderID,
					soft:     true,
				}
				window := time.Duration(watchGroupWindowSeconds(chat)) * time.Second
				maxItems := watchGroupMax(chat)
				watchMediaGroupMgr.addFile(key, file, window, maxItems, func(files []tfile.TGFileMessage) {
					processWatchSoftMediaGroup(ctx, user, stor, defaultDirPath, event.ChatID, senderID, files)
				})
				continue
			}

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

type watchBatchOptions struct {
	sourceGroupKey       string
	requireTelegramGroup bool
}

func processWatchMediaGroup(ctx *ext.Context, user *database.User, stor storage.Storage, dirPath string, files []tfile.TGFileMessage) {
	processWatchFileBatch(ctx, user, stor, dirPath, files, watchBatchOptions{requireTelegramGroup: true})
}

func processWatchSoftMediaGroup(
	ctx *ext.Context,
	user *database.User,
	stor storage.Storage,
	dirPath string,
	sourceChatID int64,
	senderID int64,
	files []tfile.TGFileMessage,
) {
	if len(files) == 0 {
		return
	}
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Message().GetID() < files[j].Message().GetID()
	})
	groupKey := fmt.Sprintf("watch-soft:%d:%d:%d:%d", sourceChatID, user.ID, senderID, files[0].Message().GetID())
	processWatchFileBatch(ctx, user, stor, dirPath, files, watchBatchOptions{sourceGroupKey: groupKey})
}

func processWatchFileBatch(
	ctx *ext.Context,
	user *database.User,
	stor storage.Storage,
	dirPath string,
	files []tfile.TGFileMessage,
	options watchBatchOptions,
) {
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

	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Message().GetID() < files[j].Message().GetID()
	})

	albumDir := strings.TrimSuffix(path.Base(files[0].Name()), path.Ext(files[0].Name()))
	elems := make([]batchtfile.TaskElement, 0, len(files))
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
		if options.requireTelegramGroup && (!isGroup || groupId == 0) {
			logger.Warnf("File %s is not in a group, skipping", file.Name())
			continue
		}

		effectiveDirPath := string(ruleDirPath)
		if ruleDirPath.NeedNewForAlbum() {
			effectiveDirPath = path.Join(dirPath, albumDir)
		}

		elem, err := batchtfile.NewTaskElement(fileStor, path.Join(effectiveDirPath, file.Name()), file)
		if err != nil {
			logger.Errorf("create batch task element failed for watch media group: %s", err)
			continue
		}
		if options.sourceGroupKey != "" {
			caption := ""
			if file.Message() != nil {
				caption = file.Message().GetMessage()
			}
			elem.SetSourceMetadata(options.sourceGroupKey, caption, true)
		}
		elems = append(elems, *elem)
	}

	if len(elems) == 0 {
		return
	}

	injectCtx := tgutil.ExtWithContext(ctx.Context, ctx)
	taskid := xid.New().String()
	task := batchtfile.NewBatchTGFileTask(taskid, injectCtx, elems, nil, true)
	if err := core.AddTask(injectCtx, task); err != nil {
		logger.Errorf("add watch media group task failed: %s", err)
		return
	}
	logger.Infof("Added watch media group task for user %d with %d files", user.ChatID, len(elems))
}
