package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tg-channel-archive-api/internal/store"
)

type fakeBotStore struct {
	channelID      int64
	channelPeerID  int64
	channelUser    string
	channelTitle   string
	message        store.UpsertMessageParams
	status         store.SyncStatusParams
	upsertChannels int
	upsertMessages int
	recordStatuses int
}

func (f *fakeBotStore) UpsertChannel(_ context.Context, peerID int64, username, title string) (int64, error) {
	f.upsertChannels++
	f.channelPeerID = peerID
	f.channelUser = username
	f.channelTitle = title
	if f.channelID == 0 {
		f.channelID = 42
	}
	return f.channelID, nil
}

func (f *fakeBotStore) UpsertMessage(_ context.Context, params store.UpsertMessageParams) error {
	f.upsertMessages++
	f.message = params
	return nil
}

func (f *fakeBotStore) RecordSyncStatus(_ context.Context, params store.SyncStatusParams) error {
	f.recordStatuses++
	f.status = params
	return nil
}

func TestBotReceiverGetUpdates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/bot123:abc/getUpdates" {
			t.Fatalf("path = %s", r.URL.Path)
		}

		var req botGetUpdatesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Offset != 10 || req.Timeout != 1 {
			t.Fatalf("request = %+v", req)
		}
		if len(req.AllowedUpdates) != 2 || req.AllowedUpdates[0] != "channel_post" || req.AllowedUpdates[1] != "edited_channel_post" {
			t.Fatalf("allowed updates = %#v", req.AllowedUpdates)
		}

		_ = json.NewEncoder(w).Encode(botGetUpdatesResponse{
			OK: true,
			Result: []botUpdate{
				{
					UpdateID: 10,
					ChannelPost: &botMessage{
						MessageID: 11,
						Date:      100,
						Text:      "hello",
						Chat: botChat{
							ID:       -1001,
							Type:     "channel",
							Title:    "Test",
							Username: "test",
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	receiver := NewBotReceiver(nil, Config{
		BotToken:    "123:abc",
		PollTimeout: time.Second,
	})
	receiver.baseURL = server.URL

	updates, err := receiver.getUpdates(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].ChannelPost == nil || updates[0].ChannelPost.Text != "hello" {
		t.Fatalf("updates = %+v", updates)
	}
}

func TestBotReceiverGetFilePath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123:abc/getFile" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var req botGetFileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.FileID != "file-id" {
			t.Fatalf("file id = %q", req.FileID)
		}
		_ = json.NewEncoder(w).Encode(botGetFileResponse{
			OK:     true,
			Result: botFileRef{FileID: "file-id", FilePath: "photos/file.jpg"},
		})
	}))
	defer server.Close()

	receiver := NewBotReceiver(nil, Config{
		BotToken:    "123:abc",
		PollTimeout: time.Second,
	})
	receiver.baseURL = server.URL

	filePath, err := receiver.getFilePath(context.Background(), "file-id")
	if err != nil {
		t.Fatal(err)
	}
	if filePath != "photos/file.jpg" {
		t.Fatalf("file path = %q", filePath)
	}
}

func TestBotReceiverWebhookRejectsInvalidSecret(t *testing.T) {
	receiver := NewBotReceiver(nil, Config{BotSecret: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	receiver.WebhookHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestBotReceiverWebhookRejectsInvalidJSON(t *testing.T) {
	receiver := NewBotReceiver(nil, Config{BotSecret: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", bytes.NewReader([]byte(`not-json`)))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	rec := httptest.NewRecorder()
	receiver.WebhookHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestBotReceiverWebhookIgnoresUnsupportedUpdate(t *testing.T) {
	receiver := NewBotReceiver(nil, Config{BotSecret: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", bytes.NewReader([]byte(`{"update_id":1}`)))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	rec := httptest.NewRecorder()
	receiver.WebhookHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestBotReceiverProcessUpdateStoresChannelPost(t *testing.T) {
	db := &fakeBotStore{channelID: 99}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(botGetFileResponse{
			OK:     true,
			Result: botFileRef{FilePath: "photos/large.jpg"},
		})
	}))
	defer server.Close()

	receiver := NewBotReceiver(db, Config{Channels: []string{"test"}, BotToken: "123:abc"})
	receiver.baseURL = server.URL

	err := receiver.processUpdate(context.Background(), botUpdate{
		UpdateID: 1,
		ChannelPost: &botMessage{
			MessageID: 7,
			Date:      123,
			Caption:   "hello",
			Chat: botChat{
				ID:       -100123,
				Type:     "channel",
				Title:    "Test Channel",
				Username: "test",
			},
			Photo: []botPhotoSize{
				{FileID: "small", Width: 100, Height: 100, FileSize: 1000},
				{FileID: "large", Width: 1000, Height: 1000, FileSize: 100},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if db.upsertChannels != 1 || db.upsertMessages != 1 || db.recordStatuses != 1 {
		t.Fatalf("calls: channels=%d messages=%d statuses=%d", db.upsertChannels, db.upsertMessages, db.recordStatuses)
	}
	if db.channelPeerID != -100123 || db.channelUser != "test" || db.channelTitle != "Test Channel" {
		t.Fatalf("channel = peer:%d username:%q title:%q", db.channelPeerID, db.channelUser, db.channelTitle)
	}
	if db.message.ChannelID != 99 || db.message.TelegramMsgID != 7 || db.message.Text != "hello" || db.message.TelegramDate.Unix() != 123 {
		t.Fatalf("message = %+v", db.message)
	}
	if db.message.MediaType != "photo" || db.message.MediaFileID != "large" || db.message.MediaFilePath != "photos/large.jpg" || len(db.message.RawPayload) == 0 {
		t.Fatalf("message media = %+v", db.message)
	}
	if !db.status.Success || db.status.ChannelID != 99 || db.status.MessageCount != 1 {
		t.Fatalf("status = %+v", db.status)
	}
}

func TestBotReceiverProcessUpdateSkipsDisallowedChannel(t *testing.T) {
	db := &fakeBotStore{}
	receiver := NewBotReceiver(db, Config{Channels: []string{"allowed"}})

	err := receiver.processUpdate(context.Background(), botUpdate{
		UpdateID: 1,
		ChannelPost: &botMessage{
			MessageID: 7,
			Date:      123,
			Text:      "hello",
			Chat: botChat{
				ID:       -100123,
				Type:     "channel",
				Title:    "Other Channel",
				Username: "other",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if db.upsertChannels != 0 || db.upsertMessages != 0 || db.recordStatuses != 0 {
		t.Fatalf("unexpected calls: channels=%d messages=%d statuses=%d", db.upsertChannels, db.upsertMessages, db.recordStatuses)
	}
}

func TestBotMessageTextAndEditTime(t *testing.T) {
	if got := botMessageText(botMessage{Caption: "caption"}); got != "caption" {
		t.Fatalf("caption text = %q", got)
	}
	if got := botEditTime(botMessage{}); got != nil {
		t.Fatalf("empty edit time = %v", got)
	}
	if got := botEditTime(botMessage{EditDate: 123}); got == nil || got.Unix() != 123 {
		t.Fatalf("edit time = %v", got)
	}
}

func TestBotMediaMetadata(t *testing.T) {
	photo := botMessage{
		Photo: []botPhotoSize{
			{FileID: "small", Width: 10, Height: 10, FileSize: 100},
			{FileID: "large", Width: 20, Height: 20, FileSize: 50},
		},
	}
	if got := botMediaType(photo); got != "photo" {
		t.Fatalf("photo media type = %q", got)
	}
	if got := botMediaFileID(photo); got != "large" {
		t.Fatalf("photo file id = %q", got)
	}

	video := botMessage{Video: &botFile{FileID: "video-file"}}
	if got := botMediaType(video); got != "video" {
		t.Fatalf("video media type = %q", got)
	}
	if got := botMediaFileID(video); got != "video-file" {
		t.Fatalf("video file id = %q", got)
	}
	if raw := botRawPayload(video); len(raw) == 0 {
		t.Fatal("expected raw payload")
	}
}

func TestBotChannelAllowed(t *testing.T) {
	chat := botChat{
		ID:       -100123,
		Type:     "channel",
		Username: "ExampleChannel",
	}

	tests := []struct {
		name      string
		allowlist []string
		want      bool
	}{
		{name: "empty allows all", allowlist: nil, want: true},
		{name: "username", allowlist: []string{"examplechannel"}, want: true},
		{name: "at username", allowlist: []string{"@ExampleChannel"}, want: true},
		{name: "chat id", allowlist: []string{"-100123"}, want: true},
		{name: "different", allowlist: []string{"other"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := botChannelAllowed(tt.allowlist, chat); got != tt.want {
				t.Fatalf("allowed = %v, want %v", got, tt.want)
			}
		})
	}
}
