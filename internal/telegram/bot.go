package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tg-channel-archive-api/internal/store"
)

type BotReceiver struct {
	db      BotStore
	cfg     Config
	client  *http.Client
	baseURL string
}

type BotStore interface {
	UpsertChannel(ctx context.Context, peerID int64, username, title string) (int64, error)
	UpsertMessage(ctx context.Context, params store.UpsertMessageParams) error
	RecordSyncStatus(ctx context.Context, params store.SyncStatusParams) error
}

type botUpdate struct {
	UpdateID          int         `json:"update_id"`
	ChannelPost       *botMessage `json:"channel_post"`
	EditedChannelPost *botMessage `json:"edited_channel_post"`
}

type botMessage struct {
	MessageID int            `json:"message_id"`
	Date      int64          `json:"date"`
	EditDate  int64          `json:"edit_date"`
	Text      string         `json:"text"`
	Caption   string         `json:"caption"`
	Chat      botChat        `json:"chat"`
	Photo     []botPhotoSize `json:"photo"`
	Video     *botFile       `json:"video"`
	Document  *botFile       `json:"document"`
	Animation *botFile       `json:"animation"`
	Audio     *botFile       `json:"audio"`
	Voice     *botFile       `json:"voice"`
	Sticker   *botFile       `json:"sticker"`
}

type botChat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username"`
}

type botPhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int    `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type botFile struct {
	FileID string `json:"file_id"`
}

type botGetUpdatesRequest struct {
	Offset         int      `json:"offset,omitempty"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

type botGetUpdatesResponse struct {
	OK          bool        `json:"ok"`
	Result      []botUpdate `json:"result"`
	ErrorCode   int         `json:"error_code"`
	Description string      `json:"description"`
}

type botGetFileRequest struct {
	FileID string `json:"file_id"`
}

type botGetFileResponse struct {
	OK          bool       `json:"ok"`
	Result      botFileRef `json:"result"`
	ErrorCode   int        `json:"error_code"`
	Description string     `json:"description"`
}

type botFileRef struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path"`
}

func NewBotReceiver(db BotStore, cfg Config) *BotReceiver {
	return &BotReceiver{
		db:      db,
		cfg:     cfg,
		client:  &http.Client{Timeout: cfg.PollTimeout + 10*time.Second},
		baseURL: "https://api.telegram.org",
	}
}

func (r *BotReceiver) Run(ctx context.Context) {
	slog.Info("telegram bot receiver started")
	var offset int
	for {
		updates, err := r.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("telegram bot getUpdates failed", "error", err)
			sleepWithContext(ctx, retryDelay(r.cfg.PollInterval))
			continue
		}

		for _, update := range updates {
			if err := r.processUpdate(ctx, update); err != nil {
				slog.Error("telegram bot update failed", "update_id", update.UpdateID, "error", err)
				break
			}
			offset = update.UpdateID + 1
		}

		if ctx.Err() != nil {
			return
		}
	}
}

func (r *BotReceiver) WebhookHandler(w http.ResponseWriter, req *http.Request) {
	if r.cfg.BotSecret != "" && req.Header.Get("X-Telegram-Bot-Api-Secret-Token") != r.cfg.BotSecret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, 4<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	defer req.Body.Close()

	var update botUpdate
	if err := json.Unmarshal(body, &update); err != nil {
		http.Error(w, "decode update", http.StatusBadRequest)
		return
	}
	if err := r.processUpdate(req.Context(), update); err != nil {
		slog.Error("telegram bot webhook update failed", "update_id", update.UpdateID, "error", err)
		http.Error(w, "process update", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (r *BotReceiver) getUpdates(ctx context.Context, offset int) ([]botUpdate, error) {
	body, err := json.Marshal(botGetUpdatesRequest{
		Offset:         offset,
		Timeout:        int(r.cfg.PollTimeout.Seconds()),
		AllowedUpdates: []string{"channel_post", "edited_channel_post"},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/bot"+r.cfg.BotToken+"/getUpdates", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram bot api status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var decoded botGetUpdatesResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, err
	}
	if !decoded.OK {
		return nil, fmt.Errorf("telegram bot api error %d: %s", decoded.ErrorCode, decoded.Description)
	}
	return decoded.Result, nil
}

func (r *BotReceiver) getFilePath(ctx context.Context, fileID string) (string, error) {
	if fileID == "" {
		return "", nil
	}
	body, err := json.Marshal(botGetFileRequest{FileID: fileID})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/bot"+r.cfg.BotToken+"/getFile", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("telegram bot getFile status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var decoded botGetFileResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return "", err
	}
	if !decoded.OK {
		return "", fmt.Errorf("telegram bot getFile error %d: %s", decoded.ErrorCode, decoded.Description)
	}
	return decoded.Result.FilePath, nil
}

func (r *BotReceiver) processUpdate(ctx context.Context, update botUpdate) error {
	msg := update.ChannelPost
	if msg == nil {
		msg = update.EditedChannelPost
	}
	if msg == nil {
		return nil
	}
	if msg.Chat.Type != "channel" {
		return nil
	}
	if !botChannelAllowed(r.cfg.Channels, msg.Chat) {
		slog.Debug("telegram bot channel ignored", "chat_id", msg.Chat.ID, "username", msg.Chat.Username)
		return nil
	}
	return r.storeBotMessage(ctx, *msg)
}

func (r *BotReceiver) storeBotMessage(ctx context.Context, msg botMessage) error {
	startedAt := time.Now().UTC()
	channelID, err := r.db.UpsertChannel(ctx, msg.Chat.ID, msg.Chat.Username, msg.Chat.Title)
	if err != nil {
		return err
	}

	if err := r.db.UpsertMessage(ctx, store.UpsertMessageParams{
		ChannelID:     channelID,
		TelegramMsgID: msg.MessageID,
		Text:          botMessageText(msg),
		MediaType:     botMediaType(msg),
		MediaFileID:   botMediaFileID(msg),
		MediaFilePath: r.botMediaFilePath(ctx, msg),
		RawPayload:    botRawPayload(msg),
		TelegramDate:  time.Unix(msg.Date, 0).UTC(),
		EditedAt:      botEditTime(msg),
	}); err != nil {
		_ = r.db.RecordSyncStatus(ctx, store.SyncStatusParams{
			ChannelID:    channelID,
			StartedAt:    startedAt,
			FinishedAt:   time.Now().UTC(),
			Success:      false,
			Error:        err.Error(),
			MessageCount: 1,
		})
		return err
	}

	return r.db.RecordSyncStatus(ctx, store.SyncStatusParams{
		ChannelID:    channelID,
		StartedAt:    startedAt,
		FinishedAt:   time.Now().UTC(),
		Success:      true,
		MessageCount: 1,
	})
}

func (r *BotReceiver) botMediaFilePath(ctx context.Context, msg botMessage) string {
	fileID := botMediaFileID(msg)
	if fileID == "" || r.cfg.BotToken == "" {
		return ""
	}
	filePath, err := r.getFilePath(ctx, fileID)
	if err != nil {
		slog.Warn("telegram bot getFile failed", "file_id", fileID, "error", err)
		return ""
	}
	return filePath
}

func botMessageText(msg botMessage) string {
	if msg.Text != "" {
		return msg.Text
	}
	return msg.Caption
}

func botMediaType(msg botMessage) string {
	switch {
	case len(msg.Photo) > 0:
		return "photo"
	case msg.Video != nil:
		return "video"
	case msg.Document != nil:
		return "document"
	case msg.Animation != nil:
		return "animation"
	case msg.Audio != nil:
		return "audio"
	case msg.Voice != nil:
		return "voice"
	case msg.Sticker != nil:
		return "sticker"
	default:
		return ""
	}
}

func botMediaFileID(msg botMessage) string {
	if len(msg.Photo) > 0 {
		best := msg.Photo[0]
		for _, photo := range msg.Photo[1:] {
			if photo.FileSize > best.FileSize || photo.Width*photo.Height > best.Width*best.Height {
				best = photo
			}
		}
		return best.FileID
	}
	for _, file := range []*botFile{msg.Video, msg.Document, msg.Animation, msg.Audio, msg.Voice, msg.Sticker} {
		if file != nil && file.FileID != "" {
			return file.FileID
		}
	}
	return ""
}

func botRawPayload(msg botMessage) store.JSONRaw {
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	return store.JSONRaw(raw)
}

func botEditTime(msg botMessage) *time.Time {
	if msg.EditDate == 0 {
		return nil
	}
	t := time.Unix(msg.EditDate, 0).UTC()
	return &t
}

func botChannelAllowed(allowlist []string, chat botChat) bool {
	if len(allowlist) == 0 {
		return true
	}
	chatID := fmt.Sprint(chat.ID)
	username := strings.ToLower(strings.TrimPrefix(chat.Username, "@"))
	for _, allowed := range allowlist {
		normalized := strings.ToLower(strings.TrimSpace(allowed))
		normalized = strings.TrimPrefix(normalized, "@")
		if normalized == "" {
			continue
		}
		if normalized == chatID || normalized == username {
			return true
		}
	}
	return false
}

func retryDelay(value time.Duration) time.Duration {
	if value <= 0 {
		return 5 * time.Second
	}
	if value > time.Minute {
		return 5 * time.Second
	}
	return value
}

func sleepWithContext(ctx context.Context, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
