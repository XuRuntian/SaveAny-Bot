package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/retry"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/duke-git/lancet/v2/validator"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/utils/dlutil"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/consts/tglimit"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/rs/xid"
	"golang.org/x/time/rate"
)

const (
	// https://core.telegram.org/api/config#upload-max-fileparts-default
	DefaultSplitSize         = 4000 * 524288 // 4000 * 512 KB
	MaxUploadFileSize        = 4000 * 524288 // 4000 * 512 KB
	PremiumMaxUploadFileSize = 8000 * 524288 // 8000 * 512 KB

	// Telegram accepts up to 10 items, but smaller groups reduce the time that
	// uploaded media references must remain valid before the album is sent.
	reliableAlbumItems = 5
)

var errAlbumGroupingRequired = errors.New("file cannot be sent without breaking the requested Telegram album")

type Telegram struct {
	config  storconfig.TelegramStorageConfig
	limiter *rate.Limiter
}

type preparedMedia struct {
	peer     tg.InputPeerClass
	uploader *uploader.Uploader
	media    message.MultiMediaOption
	caption  []message.StyledTextOption
}

type batchMediaItem struct {
	item          storagetypes.BatchItem
	index         int
	chatID        int64
	albumEligible bool
	useSingleSave bool
}

func (t *Telegram) Init(ctx context.Context, cfg storconfig.StorageConfig) error {
	telegramConfig, ok := cfg.(*storconfig.TelegramStorageConfig)
	if !ok {
		return fmt.Errorf("failed to cast telegram config")
	}
	if err := telegramConfig.Validate(); err != nil {
		return err
	}
	t.config = *telegramConfig
	if t.config.RateLimit <= 0 || t.config.RateBurst <= 0 {
		t.config.RateLimit = 2
		t.config.RateBurst = 1
	}
	t.limiter = rate.NewLimiter(rate.Every(time.Duration(t.config.RateLimit)*time.Second), t.config.RateBurst)
	return nil
}

func (t *Telegram) Type() storenum.StorageType {
	return storenum.Telegram
}

func (t *Telegram) Name() string {
	return t.config.Name
}

// Exists always reports false: Telegram offers no reliable way to query
// whether a file already exists in a chat, so conflict policies do not apply
// to this backend.
func (t *Telegram) Exists(ctx context.Context, storagePath string) bool {
	return false
}

func (t *Telegram) Save(ctx context.Context, r io.Reader, storagePath string) error {
	return t.save(ctx, r, storagePath, nil)
}

// SaveWithProgress saves a file while reporting Telegram-confirmed upload
// progress after each uploaded part.
func (t *Telegram) SaveWithProgress(
	ctx context.Context,
	r io.Reader,
	storagePath string,
	onProgress func(uploaded, total int64),
) error {
	size := contentLength(ctx)
	return t.save(ctx, r, storagePath, newUploadProgress(size, onProgress))
}

func (t *Telegram) save(ctx context.Context, r io.Reader, storagePath string, progress *uploadProgress) error {
	storagePath = path.Clean(storagePath)
	captionOverride := sourceCaptionOverride(ctx)
	tctx := tgutil.ExtFromContext(ctx)
	if tctx == nil {
		return fmt.Errorf("failed to get telegram context")
	}
	size := contentLength(ctx)
	maxUploadSize := maxUploadFileSize(tctx)
	if t.config.SkipLarge && size > maxUploadSize {
		log.FromContext(ctx).Warnf("Skipping file larger than Telegram limit (%d bytes): %d bytes", maxUploadSize, size)
		return nil
	}
	splitSize := t.splitSize(maxUploadSize)
	if size > splitSize {
		filename, chatID := t.target(tctx, storagePath)
		if filename == "" {
			if rs, ok := r.(io.ReadSeeker); ok {
				mtype, err := mimetype.DetectReader(rs)
				if err != nil {
					return fmt.Errorf("failed to detect mimetype: %w", err)
				}
				filename = xid.New().String() + mtype.Extension()
				if _, err := rs.Seek(0, io.SeekStart); err != nil {
					return fmt.Errorf("failed to seek reader: %w", err)
				}
			}
		}
		upler := t.newUploader(tctx, size, progress)
		peer := tryGetInputPeer(tctx, chatID)
		if peer == nil || peer.Zero() {
			return fmt.Errorf("failed to get input peer for chat ID %d", chatID)
		}
		if err := t.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limit failed: %w", err)
		}
		if t.config.SplitLargeVideo {
			rs, ok := r.(io.ReadSeeker)
			if ok {
				mtype, detectErr := mimetype.DetectReader(rs)
				if _, seekErr := rs.Seek(0, io.SeekStart); seekErr != nil {
					return fmt.Errorf("failed to seek large file after mimetype detection: %w", seekErr)
				}
				if detectErr != nil {
					log.FromContext(ctx).Warnf("Failed to detect large file type, falling back to ZIP split: %s", detectErr)
				} else if strings.HasPrefix(mtype.String(), "video/") {
					parts, cleanup, splitErr := createLosslessVideoParts(
						ctx,
						rs,
						filename,
						size,
						splitSize,
					)
					if splitErr != nil {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						log.FromContext(ctx).Warnf("Lossless video split failed, falling back to ZIP split: %s", splitErr)
					} else {
						defer cleanup()
						log.FromContext(ctx).Infof("Uploading oversized video as %d lossless-playable parts", len(parts))
						for _, part := range parts {
							log.FromContext(ctx).Infof("Prepared lossless video part %s (%d bytes)", part.Name, part.Size)
						}
						return t.uploadLosslessVideoParts(ctx, tctx, storagePath, parts, captionOverride, progress)
					}
					if _, seekErr := rs.Seek(0, io.SeekStart); seekErr != nil {
						return fmt.Errorf("failed to seek large video before ZIP fallback: %w", seekErr)
					}
				}
			}
		}
		return t.splitUpload(tctx, r, filename, upler, peer, size, splitSize, progress)
	}

	if err := t.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit failed: %w", err)
	}
	prepared, err := t.prepareMedia(ctx, tctx, r, storagePath, size, captionOverride, progress)
	if err != nil {
		return err
	}
	_, err = tctx.Sender.
		WithUploader(prepared.uploader).
		To(prepared.peer).
		Media(ctx, prepared.media)
	return err
}

func contentLength(ctx context.Context) int64 {
	if length := ctx.Value(ctxkey.ContentLength); length != nil {
		if size, ok := length.(int64); ok {
			return size
		}
	}
	return -1
}

func sourceCaptionOverride(ctx context.Context) *string {
	caption, ok := storagetypes.SourceCaptionFromContext(ctx)
	if !ok {
		return nil
	}
	return &caption
}

func maxUploadFileSize(tctx *ext.Context) int64 {
	if tctx != nil && tctx.Self != nil && !tctx.Self.GetBot() && tctx.Self.GetPremium() {
		return PremiumMaxUploadFileSize
	}
	return MaxUploadFileSize
}

func (t *Telegram) splitSize(maxUploadSize int64) int64 {
	splitSize := t.config.SplitSizeMB * 1024 * 1024
	if splitSize <= 0 {
		return maxUploadSize
	}
	return min(splitSize, maxUploadSize)
}

func (t *Telegram) target(tctx *ext.Context, storagePath string) (string, int64) {
	// 去除前导斜杠并分隔路径, 当 len(parts):
	// ==0, 存储到配置文件中的 chat_id, 随机文件名
	// ==1, 视作只有文件名, 存储到配置文件中的 chat_id
	// >=2, parts[0]: 视作要存储到的 chat_id, 最后一项为 filename
	parts := slice.Compact(strings.Split(strings.TrimPrefix(storagePath, "/"), "/"))
	filename := ""
	chatID := t.config.ChatID
	if len(parts) >= 1 {
		filename = parts[len(parts)-1]
	}
	if len(parts) >= 2 && validator.IsAlphaNumeric(parts[0]) {
		cid, err := tgutil.ParseChatID(tctx, parts[0])
		if err != nil {
			log.FromContext(tctx).Warnf("Failed to parse chat ID from path, using configured chat_id: %s", err)
			cid = chatID
		}
		chatID = cid
	}
	return filename, chatID
}

func (t *Telegram) newUploader(tctx *ext.Context, size int64, progress *uploadProgress) *uploader.Uploader {
	upler := uploader.NewUploader(tctx.Raw).
		WithPartSize(tglimit.MaxUploadPartSize).
		WithThreads(dlutil.BestThreads(size, config.C().Threads))
	if progress != nil {
		upler = upler.WithProgress(progress)
	}
	return upler
}

func mediaCaption(filename string, override *string) []message.StyledTextOption {
	if override == nil {
		return []message.StyledTextOption{styling.Plain(filename)}
	}
	if *override == "" {
		return nil
	}
	return []message.StyledTextOption{styling.Plain(*override)}
}

func (t *Telegram) prepareMedia(
	ctx context.Context,
	tctx *ext.Context,
	r io.Reader,
	storagePath string,
	size int64,
	captionOverride *string,
	progress *uploadProgress,
) (*preparedMedia, error) {
	storagePath = path.Clean(storagePath)
	filename, chatID := t.target(tctx, storagePath)
	upler := t.newUploader(tctx, size, progress)
	peer := tryGetInputPeer(tctx, chatID)
	if peer == nil || peer.Zero() {
		return nil, fmt.Errorf("failed to get input peer for chat ID %d", chatID)
	}

	rs, seekable := r.(io.ReadSeeker)
	var mtype *mimetype.MIME
	if seekable {
		var err error
		mtype, err = mimetype.DetectReader(rs)
		if err != nil {
			return nil, fmt.Errorf("failed to detect mimetype: %w", err)
		}
		if filename == "" {
			filename = xid.New().String() + mtype.Extension()
		}
		if _, err := rs.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("failed to seek reader: %w", err)
		}
	}

	var file tg.InputFileClass
	var err error
	if size <= 0 {
		file, err = upler.FromReader(ctx, filename, r)
	} else {
		file, err = upler.Upload(ctx, uploader.NewUpload(filename, r, size))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to upload file to telegram: %w", err)
	}
	caption := mediaCaption(filename, captionOverride)
	forceFile := t.config.ForceFile
	if mtype != nil && strings.HasPrefix(mtype.String(), "image/") && size >= tglimit.MaxPhotoSize {
		forceFile = true
	}
	doc := message.UploadedDocument(file, caption...).
		Filename(filename).
		ForceFile(forceFile)
	if mtype != nil {
		doc = doc.MIME(mtype.String())
	}
	var media message.MultiMediaOption = doc
	if mtype != nil && rs != nil {
		switch mtypeStr := mtype.String(); {
		case strings.HasPrefix(mtypeStr, "video/"):
			media = doc.Video().SupportsStreaming()
			thumb, err := extractThumbFrame(rs)
			if err == nil {
				thumb, err := upler.FromBytes(ctx, "thumb.jpg", thumb)
				if err == nil {
					doc = doc.Thumb(thumb)
				}
			}
			rs.Seek(0, io.SeekStart)
			switch mtypeStr {
			case "video/mp4":
				info, err := getMP4Meta(rs)
				if err != nil {
					// Fallback to ffprobe if gomedia fails (e.g., malformed MP4)
					rs.Seek(0, io.SeekStart)
					info, err = getVideoMetadata(rs)
				}
				if err == nil {
					media = doc.Video().
						Duration(time.Duration(info.Duration)*time.Second).
						Resolution(info.Width, info.Height).
						SupportsStreaming()
				}
			default:
				info, err := getVideoMetadata(rs)
				if err == nil {
					media = doc.Video().
						Duration(time.Duration(info.Duration)*time.Second).
						Resolution(info.Width, info.Height).
						SupportsStreaming()
				}
			}
		case strings.HasPrefix(mtypeStr, "audio/"):
			media = doc.Audio().Title(filename)
		case strings.HasPrefix(mtypeStr, "image/") && !strings.HasSuffix(mtypeStr, "webp"):
			media = message.UploadedPhoto(file, caption...)
		}
	}
	return &preparedMedia{
		peer:     peer,
		uploader: upler,
		media:    media,
		caption:  caption,
	}, nil
}

// SaveBatch preserves each source photo/video group as a Telegram album.
func (t *Telegram) SaveBatch(ctx context.Context, items []storagetypes.BatchItem) error {
	return t.saveBatch(ctx, items, nil)
}

// SaveBatchWithProgress preserves source media groups while reporting native
// Telegram upload progress for each input item.
func (t *Telegram) SaveBatchWithProgress(
	ctx context.Context,
	items []storagetypes.BatchItem,
	onProgress func(index int, uploaded, total int64),
) error {
	return t.saveBatch(ctx, items, onProgress)
}

func (t *Telegram) saveBatch(
	ctx context.Context,
	items []storagetypes.BatchItem,
	onProgress func(index int, uploaded, total int64),
) error {
	tctx := tgutil.ExtFromContext(ctx)
	if tctx == nil {
		return fmt.Errorf("failed to get telegram context")
	}

	inspected := make([]batchMediaItem, 0, len(items))
	var failures []storagetypes.BatchSaveFailure
	for index, item := range items {
		mediaItem, err := t.inspectBatchItem(tctx, item)
		if err != nil {
			if item.RequireGroup {
				failures = append(failures, batchFailure(index, item, err))
				continue
			}
			return err
		}
		mediaItem.index = index
		inspected = append(inspected, mediaItem)
	}
	groups, rejected := planMediaGroups(inspected)
	for _, item := range rejected {
		failures = append(failures, batchFailure(item.index, item.item, errAlbumGroupingRequired))
		log.FromContext(ctx).Warnf(
			"Skipping item that cannot preserve requested Telegram album: index=%d file=%q",
			item.index,
			path.Base(item.item.StoragePath),
		)
	}
	for _, group := range groups {
		if err := t.saveMediaGroup(ctx, tctx, group, onProgress); err != nil {
			var partial *storagetypes.BatchSaveError
			if !errors.As(err, &partial) {
				return err
			}
			failures = append(failures, partial.Failures...)
		}
	}
	if len(failures) > 0 {
		return &storagetypes.BatchSaveError{Failures: failures}
	}
	return nil
}

func (t *Telegram) inspectBatchItem(tctx *ext.Context, item storagetypes.BatchItem) (batchMediaItem, error) {
	_, chatID := t.target(tctx, path.Clean(item.StoragePath))
	result := batchMediaItem{item: item, chatID: chatID}
	maxUploadSize := maxUploadFileSize(tctx)
	if (t.config.SkipLarge && item.Size > maxUploadSize) ||
		item.Size > t.splitSize(maxUploadSize) {
		result.useSingleSave = true
		return result, nil
	}
	if _, err := item.Reader.Seek(0, io.SeekStart); err != nil {
		return result, fmt.Errorf("failed to seek batch item before mimetype detection: %w", err)
	}
	mtype, err := mimetype.DetectReader(item.Reader)
	if err != nil {
		return result, fmt.Errorf("failed to detect batch item mimetype: %w", err)
	}
	if _, err := item.Reader.Seek(0, io.SeekStart); err != nil {
		return result, fmt.Errorf("failed to seek batch item: %w", err)
	}
	mtypeStr := mtype.String()
	forceFile := t.config.ForceFile || strings.HasPrefix(mtypeStr, "image/") && item.Size >= tglimit.MaxPhotoSize
	result.albumEligible = !forceFile && (strings.HasPrefix(mtypeStr, "video/") ||
		strings.HasPrefix(mtypeStr, "image/") && mtypeStr != "image/webp" && mtypeStr != "image/gif")
	return result, nil
}

func planMediaGroups(items []batchMediaItem) ([][]batchMediaItem, []batchMediaItem) {
	groups := make([][]batchMediaItem, 0, len(items))
	rejected := make([]batchMediaItem, 0)
	for i := 0; i < len(items); {
		item := items[i]
		if item.item.RequireGroup && item.item.SourceGroupKey != "" {
			end := i + 1
			for end < len(items) && items[end].item.RequireGroup &&
				items[end].chatID == item.chatID &&
				items[end].item.SourceGroupKey == item.item.SourceGroupKey {
				end++
			}
			eligible := make([]batchMediaItem, 0, end-i)
			for _, candidate := range items[i:end] {
				if candidate.useSingleSave || !candidate.albumEligible {
					rejected = append(rejected, candidate)
					continue
				}
				eligible = append(eligible, candidate)
			}
			for len(eligible) > reliableAlbumItems {
				size := reliableAlbumItems
				if len(eligible)-size == 1 {
					size--
				}
				groups = append(groups, eligible[:size])
				eligible = eligible[size:]
			}
			if len(eligible) == 1 {
				rejected = append(rejected, eligible[0])
			} else if len(eligible) > 1 {
				groups = append(groups, eligible)
			}
			i = end
			continue
		}
		if item.useSingleSave || !item.albumEligible || item.item.SourceGroupKey == "" {
			groups = append(groups, items[i:i+1])
			i++
			continue
		}
		end := i + 1
		for end < len(items) && end-i < min(reliableAlbumItems, tglimit.MaxAlbumItems) {
			next := items[end]
			if next.useSingleSave || !next.albumEligible || next.chatID != item.chatID || next.item.SourceGroupKey != item.item.SourceGroupKey {
				break
			}
			end++
		}
		groups = append(groups, items[i:end])
		i = end
	}
	return groups, rejected
}

func batchItemUploadProgress(
	mediaItem batchMediaItem,
	onProgress func(index int, uploaded, total int64),
) *uploadProgress {
	if onProgress == nil {
		return nil
	}
	return newUploadProgress(mediaItem.item.Size, func(uploaded, total int64) {
		onProgress(mediaItem.index, uploaded, total)
	})
}

func (t *Telegram) saveMediaGroup(
	ctx context.Context,
	tctx *ext.Context,
	group []batchMediaItem,
	onProgress func(index int, uploaded, total int64),
) error {
	if len(group) == 1 {
		if group[0].item.RequireGroup {
			return &storagetypes.BatchSaveError{Failures: batchFailures(group, errAlbumGroupingRequired)}
		}
		err := t.saveBatchItemWithRetry(ctx, group[0], onProgress)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isSkippableBatchItemError(err) {
			return err
		}
		failure := storagetypes.BatchSaveFailure{
			Index: group[0].index,
			Path:  group[0].item.StoragePath,
			Err:   err,
		}
		log.FromContext(ctx).Errorf(
			"Skipping Telegram batch item after individual upload failed: index=%d file=%q error=%v",
			failure.Index,
			path.Base(failure.Path),
			failure.Err,
		)
		return &storagetypes.BatchSaveError{Failures: []storagetypes.BatchSaveFailure{failure}}
	}
	return t.saveAlbumGroup(ctx, tctx, group, onProgress)
}

func (t *Telegram) saveBatchItemWithRetry(
	ctx context.Context,
	mediaItem batchMediaItem,
	onProgress func(index int, uploaded, total int64),
) error {
	retryLimit := max(config.C().Retry, 1)
	var lastErr error
	for attempt := 1; attempt <= retryLimit; attempt++ {
		lastErr = t.saveBatchItemOnce(ctx, mediaItem, onProgress)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if isSkippableBatchItemError(lastErr) {
			return lastErr
		}
		if attempt < retryLimit {
			timer := time.NewTimer(retry.DefaultRetryLinearInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("failed to save batch item after %d attempts: %w", retryLimit, lastErr)
}

func (t *Telegram) saveAlbumGroup(
	ctx context.Context,
	tctx *ext.Context,
	group []batchMediaItem,
	onProgress func(index int, uploaded, total int64),
) error {
	logger := log.FromContext(ctx).WithPrefix("telegram_album")
	requireGroup := group[0].item.RequireGroup
	logger.Infof(
		"Preparing Telegram album: items=%d total_bytes=%d files=%s",
		len(group),
		albumTotalBytes(group),
		strings.Join(albumItemNames(group), ", "),
	)

	stableMedia := make([]message.MultiMediaOption, 0, len(group))
	stableItems := make([]batchMediaItem, 0, len(group))
	failures := make([]storagetypes.BatchSaveFailure, 0)
	var peer tg.InputPeerClass
	for _, mediaItem := range group {
		var stable message.MultiMediaOption
		var itemPeer tg.InputPeerClass
		var mediaType string
		var referenceBytes int
		err := retry.Retry(func() error {
			item := mediaItem.item
			if _, err := item.Reader.Seek(0, io.SeekStart); err != nil {
				return fmt.Errorf("failed to seek batch item: %w", err)
			}
			var captionOverride *string
			if item.PreserveCaption {
				captionOverride = &item.Caption
			}
			progress := batchItemUploadProgress(mediaItem, onProgress)
			prepared, err := t.prepareMedia(ctx, tctx, item.Reader, item.StoragePath, item.Size, captionOverride, progress)
			if err != nil {
				return err
			}

			uploaded, err := tctx.Sender.
				WithUploader(prepared.uploader).
				To(prepared.peer).
				UploadMedia(ctx, prepared.media)
			if err != nil {
				return fmt.Errorf("failed to stabilize uploaded media: %w", err)
			}
			inputMedia, err := uploadedMessageMediaToInput(uploaded)
			if err != nil {
				return err
			}
			itemPeer = prepared.peer
			mediaType = fmt.Sprintf("%T", inputMedia)
			referenceBytes = inputMediaReferenceLength(inputMedia)
			stable = message.ForceMulti(message.Media(inputMedia, prepared.caption...))
			return nil
		}, retry.Context(ctx), retry.RetryTimes(uint(config.C().Retry)))
		if err != nil {
			if requireGroup && isSkippableBatchItemError(err) {
				failure := batchFailure(mediaItem.index, mediaItem.item, err)
				failures = append(failures, failure)
				logger.Errorf(
					"Skipping invalid Telegram album item: index=%d file=%q error=%v",
					failure.Index,
					path.Base(failure.Path),
					failure.Err,
				)
				continue
			}
			return fmt.Errorf("failed to prepare batch item %q: %w", mediaItem.item.StoragePath, err)
		}
		if peer == nil {
			peer = itemPeer
		}
		logger.Debugf(
			"Prepared Telegram album item: file=%q media_type=%s reference_bytes=%d",
			path.Base(mediaItem.item.StoragePath),
			mediaType,
			referenceBytes,
		)
		stableMedia = append(stableMedia, stable)
		stableItems = append(stableItems, mediaItem)
	}
	if requireGroup && len(stableMedia) < 2 {
		failures = append(failures, batchFailures(stableItems, errAlbumGroupingRequired)...)
		return &storagetypes.BatchSaveError{Failures: failures}
	}

	err := t.sendPreparedAlbum(ctx, tctx, peer, stableMedia)
	if err == nil {
		if len(failures) > 0 {
			return &storagetypes.BatchSaveError{Failures: failures}
		}
		return nil
	}
	if !isRecoverableAlbumMediaError(err) {
		return err
	}

	if splitAt, ok := albumRecoverySplit(len(stableItems), requireGroup); ok {
		logger.Warnf(
			"Telegram rejected album media; retrying as groups of %d and %d items: %v",
			splitAt,
			len(stableItems)-splitAt,
			err,
		)
		for _, subgroup := range [][]batchMediaItem{stableItems[:splitAt], stableItems[splitAt:]} {
			if splitErr := t.saveMediaGroup(ctx, tctx, subgroup, onProgress); splitErr != nil {
				var partial *storagetypes.BatchSaveError
				if !errors.As(splitErr, &partial) {
					return fmt.Errorf("failed to recover album subgroup: %w", splitErr)
				}
				failures = append(failures, partial.Failures...)
			}
		}
		if len(failures) > 0 {
			return &storagetypes.BatchSaveError{Failures: failures}
		}
		return nil
	}
	if requireGroup {
		failures = append(failures, batchFailures(stableItems, err)...)
		return &storagetypes.BatchSaveError{Failures: failures}
	}
	return err
}

func (t *Telegram) sendPreparedAlbum(
	ctx context.Context,
	tctx *ext.Context,
	peer tg.InputPeerClass,
	media []message.MultiMediaOption,
) error {
	retryLimit := max(config.C().Retry, 1)
	var lastErr error
	for attempt := 1; attempt <= retryLimit; attempt++ {
		if err := t.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limit failed: %w", err)
		}
		builder := tctx.Sender.To(peer)
		if _, err := builder.Album(ctx, media[0], media[1:]...); err == nil {
			return nil
		} else {
			lastErr = fmt.Errorf("failed to send media album: %w", err)
			if isRecoverableAlbumMediaError(err) {
				return lastErr
			}
		}

		if attempt < retryLimit {
			timer := time.NewTimer(retry.DefaultRetryLinearInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("failed to send media album after %d attempts: %w", retryLimit, lastErr)
}

func albumRecoverySplit(count int, requireGroup bool) (int, bool) {
	if requireGroup && count < 4 {
		return 0, false
	}
	if count < 2 {
		return 0, false
	}
	return count / 2, true
}

func batchFailure(index int, item storagetypes.BatchItem, err error) storagetypes.BatchSaveFailure {
	return storagetypes.BatchSaveFailure{Index: index, Path: item.StoragePath, Err: err}
}

func batchFailures(group []batchMediaItem, err error) []storagetypes.BatchSaveFailure {
	failures := make([]storagetypes.BatchSaveFailure, 0, len(group))
	for _, item := range group {
		failures = append(failures, batchFailure(item.index, item.item, err))
	}
	return failures
}

func isRecoverableAlbumMediaError(err error) bool {
	return tg.IsMediaEmpty(err) ||
		tg.IsMediaFileInvalid(err) ||
		tg.IsMediaGroupedInvalid(err) ||
		tg.IsMediaInvalid(err) ||
		tg.IsMediaTypeInvalid(err)
}

func isSkippableBatchItemError(err error) bool {
	return isRecoverableAlbumMediaError(err) ||
		tg.IsDocumentInvalid(err) ||
		tg.IsFileContentTypeInvalid(err) ||
		tg.IsFileEmtpy(err) ||
		tg.IsPhotoContentTypeInvalid(err) ||
		tg.IsPhotoExtInvalid(err) ||
		tg.IsPhotoInvalid(err) ||
		tg.IsPhotoInvalidDimensions(err) ||
		tg.IsVideoContentTypeInvalid(err) ||
		tg.IsVideoFileInvalid(err)
}

func albumItemNames(group []batchMediaItem) []string {
	names := make([]string, 0, len(group))
	for _, mediaItem := range group {
		names = append(names, path.Base(mediaItem.item.StoragePath))
	}
	return names
}

func albumTotalBytes(group []batchMediaItem) int64 {
	var total int64
	for _, mediaItem := range group {
		total += max(mediaItem.item.Size, 0)
	}
	return total
}

func inputMediaReferenceLength(media tg.InputMediaClass) int {
	switch value := media.(type) {
	case *tg.InputMediaPhoto:
		if photo, ok := value.ID.(*tg.InputPhoto); ok {
			return len(photo.FileReference)
		}
	case *tg.InputMediaDocument:
		if document, ok := value.ID.(*tg.InputDocument); ok {
			return len(document.FileReference)
		}
	}
	return 0
}

func (t *Telegram) saveBatchItemOnce(
	ctx context.Context,
	mediaItem batchMediaItem,
	onProgress func(index int, uploaded, total int64),
) error {
	item := mediaItem.item
	if _, err := item.Reader.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek batch item: %w", err)
	}
	itemCtx := context.WithValue(ctx, ctxkey.ContentLength, item.Size)
	if item.PreserveCaption {
		itemCtx = storagetypes.WithSourceCaption(itemCtx, item.Caption)
	}
	if onProgress == nil {
		return t.Save(itemCtx, item.Reader, item.StoragePath)
	}
	return t.SaveWithProgress(itemCtx, item.Reader, item.StoragePath, func(uploaded, total int64) {
		onProgress(mediaItem.index, uploaded, total)
	})
}

func uploadedMessageMediaToInput(media tg.MessageMediaClass) (tg.InputMediaClass, error) {
	switch value := media.(type) {
	case *tg.MessageMediaPhoto:
		if value.Photo == nil {
			return nil, fmt.Errorf("uploaded photo is empty")
		}
		photo, ok := value.Photo.AsNotEmpty()
		if !ok {
			return nil, fmt.Errorf("uploaded photo is empty")
		}
		return &tg.InputMediaPhoto{ID: photo.AsInput(), TTLSeconds: value.TTLSeconds}, nil
	case *tg.MessageMediaDocument:
		if value.Document == nil {
			return nil, fmt.Errorf("uploaded document is empty")
		}
		document, ok := value.Document.AsNotEmpty()
		if !ok {
			return nil, fmt.Errorf("uploaded document is empty")
		}
		return &tg.InputMediaDocument{ID: document.AsInput(), TTLSeconds: value.TTLSeconds}, nil
	default:
		return nil, fmt.Errorf("unsupported uploaded media type %T", media)
	}
}

func (t *Telegram) CannotStream() string {
	return "Telegram storage must use a ReaderSeeker"
}

func (t *Telegram) splitUpload(
	ctx *ext.Context,
	r io.Reader,
	filename string,
	upler *uploader.Uploader,
	peer tg.InputPeerClass,
	fileSize, splitSize int64,
	progress *uploadProgress,
) error {
	tempId := xid.New().String()
	outputBase := filepath.Join(config.C().Temp.BasePath, tempId, strings.Split(filename, ".")[0])
	defer func() {
		// cleanup temp files
		if err := os.RemoveAll(filepath.Join(config.C().Temp.BasePath, tempId)); err != nil {
			log.FromContext(ctx).Warnf("Failed to cleanup temp split files: %s", err)
		}
	}()
	if err := CreateSplitZip(ctx, r, fileSize, filename, outputBase, splitSize); err != nil {
		return fmt.Errorf("failed to create split zip: %w", err)
	}
	matched, err := filepath.Glob(outputBase + ".z*")
	if err != nil {
		return fmt.Errorf("failed to glob split files: %w", err)
	}
	inputFiles := make([]tg.InputFileClass, 0, len(matched))
	if progress != nil {
		var uploadSize int64
		for _, partPath := range matched {
			partInfo, err := os.Stat(partPath)
			if err != nil {
				return fmt.Errorf("failed to stat split part %s: %w", partPath, err)
			}
			uploadSize += partInfo.Size()
		}
		progress.reset(uploadSize)
	}
	for _, partPath := range matched {
		// 串行上传, 不然容易被tg风控
		err = func() error {
			partFile, err := os.Open(partPath)
			if err != nil {
				return fmt.Errorf("failed to open split part %s: %w", partPath, err)
			}
			defer partFile.Close()
			partInfo, err := partFile.Stat()
			if err != nil {
				return fmt.Errorf("failed to stat split part %s: %w", partPath, err)
			}
			partFileSize := partInfo.Size()
			partName := filepath.Base(partPath)
			partInputFile, err := upler.Upload(ctx, uploader.NewUpload(partName, partFile, partFileSize))
			if err != nil {
				return fmt.Errorf("failed to upload split part %s: %w", partPath, err)
			}
			inputFiles = append(inputFiles, partInputFile)
			return nil
		}()
		if err != nil {
			return fmt.Errorf("failed to upload split part %s: %w", partPath, err)
		}
	}
	if len(inputFiles) == 1 {
		// only one part, send as normal file
		// shoud not happen as we already check fileSize > splitSize
		doc := message.UploadedDocument(inputFiles[0]).
			Filename(filepath.Base(matched[0])).
			ForceFile(true).
			MIME("application/zip")
		_, err = ctx.Sender.
			WithUploader(upler).
			To(peer).
			Media(ctx, doc)
		return err
	}

	multiMedia := make([]message.MultiMediaOption, 0, len(inputFiles))
	for i, inputFile := range inputFiles {
		doc := message.UploadedDocument(inputFile).
			Filename(filepath.Base(matched[i])).
			MIME("application/zip")
		multiMedia = append(multiMedia, doc)
	}

	sender := ctx.Sender

	if len(multiMedia) <= tglimit.MaxAlbumItems {
		_, err = sender.WithUploader(upler).
			To(peer).
			Album(ctx, multiMedia[0], multiMedia[1:]...)
		return err
	}

	// more than MaxAlbumItems parts, send in batches, each batch up to MaxAlbumItems parts
	for i := 0; i < len(multiMedia); i += tglimit.MaxAlbumItems {
		end := min(i+tglimit.MaxAlbumItems, len(multiMedia))
		batch := multiMedia[i:end]
		_, err = sender.WithUploader(upler).
			To(peer).
			Album(ctx, batch[0], batch[1:]...)
		if err != nil {
			return fmt.Errorf("failed to send album batch: %w", err)
		}
	}
	return nil

}
