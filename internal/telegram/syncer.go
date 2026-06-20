package telegram

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"tg-channel-archive-api/internal/store"
)

type Config struct {
	Enabled      bool
	Source       string
	APIID        int
	APIHash      string
	Phone        string
	Password     string
	BotToken     string
	BotMode      string
	BotSecret    string
	BotWebhook   string
	SessionFile  string
	Channels     []string
	PollInterval time.Duration
	PollTimeout  time.Duration
	BatchLimit   int
}

type Syncer struct {
	db  *store.DB
	cfg Config
}

func NewSyncer(db *store.DB, cfg Config) *Syncer {
	return &Syncer{db: db, cfg: cfg}
}

func (s *Syncer) Run(ctx context.Context) {
	storage := &session.FileStorage{Path: s.cfg.SessionFile}
	client := telegram.NewClient(s.cfg.APIID, s.cfg.APIHash, telegram.Options{SessionStorage: storage})

	for {
		err := client.Run(ctx, func(ctx context.Context) error {
			if err := client.Auth().IfNecessary(ctx, s.authFlow()); err != nil {
				return fmt.Errorf("telegram auth: %w", err)
			}

			api := client.API()
			if err := s.syncOnce(ctx, api); err != nil {
				slog.Error("telegram sync failed", "error", err)
			}

			ticker := time.NewTicker(s.cfg.PollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					if err := s.syncOnce(ctx, api); err != nil {
						slog.Error("telegram sync failed", "error", err)
					}
				}
			}
		})
		if ctx.Err() != nil {
			return
		}
		slog.Error("telegram client disconnected", "error", err)
		time.Sleep(10 * time.Second)
	}
}

func (s *Syncer) authFlow() auth.Flow {
	codePrompt := auth.CodeAuthenticatorFunc(func(_ context.Context, _ *tg.AuthSentCode) (string, error) {
		fmt.Print("Telegram code: ")
		code, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(code), nil
	})
	if s.cfg.Password != "" {
		return auth.NewFlow(auth.Constant(s.cfg.Phone, s.cfg.Password, codePrompt), auth.SendCodeOptions{})
	}
	return auth.NewFlow(auth.CodeOnly(s.cfg.Phone, codePrompt), auth.SendCodeOptions{})
}

func (s *Syncer) syncOnce(ctx context.Context, api *tg.Client) error {
	var syncErr error
	for _, channel := range s.cfg.Channels {
		if err := s.syncChannel(ctx, api, channel); err != nil {
			wrapped := fmt.Errorf("sync channel %q: %w", channel, err)
			slog.Error("telegram channel sync failed", "channel", channel, "error", wrapped)
			syncErr = wrapped
		}
	}
	return syncErr
}

func (s *Syncer) syncChannel(ctx context.Context, api *tg.Client, channelRef string) error {
	startedAt := time.Now().UTC()
	peer, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: strings.TrimPrefix(channelRef, "@"),
	})
	if err != nil {
		return err
	}

	channel, input, err := channelFromResolved(peer)
	if err != nil {
		return err
	}

	channelID, err := s.db.UpsertChannel(ctx, channel.ID, channel.Username, channel.Title)
	if err != nil {
		return err
	}

	var allIDs []int
	offsetID := 0
	for {
		history, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     input,
			OffsetID: offsetID,
			Limit:    s.cfg.BatchLimit,
		})
		if err != nil {
			return err
		}

		messages := extractMessages(history)
		if len(messages) == 0 {
			break
		}

		for _, msg := range messages {
			allIDs = append(allIDs, msg.ID)
			if err := s.db.UpsertMessage(ctx, toStoreMessage(channelID, msg)); err != nil {
				return err
			}
			offsetID = msg.ID
		}

		if len(messages) < s.cfg.BatchLimit {
			break
		}
	}

	if err := s.db.MarkMissingDeleted(ctx, channelID, allIDs); err != nil {
		_ = s.recordSyncStatus(ctx, channelID, startedAt, len(allIDs), err)
		return err
	}
	if err := s.recordSyncStatus(ctx, channelID, startedAt, len(allIDs), nil); err != nil {
		return err
	}
	slog.Info("telegram channel synced", "channel", channelRef, "messages", len(allIDs))
	return nil
}

func (s *Syncer) recordSyncStatus(ctx context.Context, channelID int64, startedAt time.Time, messageCount int, syncErr error) error {
	params := store.SyncStatusParams{
		ChannelID:    channelID,
		StartedAt:    startedAt,
		FinishedAt:   time.Now().UTC(),
		Success:      syncErr == nil,
		MessageCount: messageCount,
	}
	if syncErr != nil {
		params.Error = syncErr.Error()
	}
	return s.db.RecordSyncStatus(ctx, params)
}

func channelFromResolved(resolved *tg.ContactsResolvedPeer) (*tg.Channel, tg.InputPeerClass, error) {
	for _, chat := range resolved.Chats {
		if channel, ok := chat.(*tg.Channel); ok {
			return channel, &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}, nil
		}
	}
	return nil, nil, fmt.Errorf("resolved peer is not a channel")
}

func extractMessages(history tg.MessagesMessagesClass) []*tg.Message {
	var classes []tg.MessageClass
	switch result := history.(type) {
	case *tg.MessagesMessages:
		classes = result.Messages
	case *tg.MessagesMessagesSlice:
		classes = result.Messages
	case *tg.MessagesChannelMessages:
		classes = result.Messages
	default:
		return nil
	}

	messages := make([]*tg.Message, 0, len(classes))
	for _, class := range classes {
		if message, ok := class.(*tg.Message); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

func toStoreMessage(channelID int64, msg *tg.Message) store.UpsertMessageParams {
	var editedAt *time.Time
	if msg.EditDate > 0 {
		t := time.Unix(int64(msg.EditDate), 0).UTC()
		editedAt = &t
	}

	var replyCount *int
	if replies, ok := msg.GetReplies(); ok {
		value := replies.Replies
		replyCount = &value
	}

	return store.UpsertMessageParams{
		ChannelID:     channelID,
		TelegramMsgID: msg.ID,
		Text:          msg.Message,
		Views:         intPtr(msg.Views),
		Forwards:      intPtr(msg.Forwards),
		ReplyCount:    replyCount,
		TelegramDate:  time.Unix(int64(msg.Date), 0).UTC(),
		EditedAt:      editedAt,
	}
}

func intPtr(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}
