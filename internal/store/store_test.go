package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestDBUpsertListAndMarkDeleted(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Postgres integration test")
	}

	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	peerID := time.Now().UnixNano()
	username := "test_channel_" + time.Now().Format("20060102150405000000000")
	channelID, err := db.UpsertChannel(ctx, peerID, username, "Test Channel")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.db.ExecContext(ctx, "delete from channels where id = $1", channelID); err != nil {
			t.Logf("cleanup channel %d: %v", channelID, err)
		}
	}()

	date := time.Unix(1000, 0).UTC()
	if err := db.UpsertMessage(ctx, UpsertMessageParams{
		ChannelID:     channelID,
		TelegramMsgID: 1,
		Text:          "first",
		MediaType:     "photo",
		MediaFileID:   "file-1",
		MediaFilePath: "photos/file-1.jpg",
		RawPayload:    JSONRaw(`{"message_id":1,"photo":[{"file_id":"file-1"}]}`),
		TelegramDate:  date,
	}); err != nil {
		t.Fatal(err)
	}

	messages, err := db.ListMessages(ctx, ListMessagesParams{Channel: username, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Text != "first" {
		t.Fatalf("messages = %+v", messages)
	}
	if messages[0].MediaType != "photo" || messages[0].MediaFileID != "file-1" || messages[0].MediaFilePath != "photos/file-1.jpg" || len(messages[0].RawPayload) == 0 {
		t.Fatalf("message media = %+v", messages[0])
	}

	messages, err = db.ListMessages(ctx, ListMessagesParams{Channel: username, Query: "FIR", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Text != "first" {
		t.Fatalf("messages for text search = %+v", messages)
	}

	messages, err = db.ListMessages(ctx, ListMessagesParams{Channel: username, Query: "missing", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages for missing text search = %+v", messages)
	}

	startedAt := time.Now().UTC().Add(-time.Second)
	finishedAt := time.Now().UTC()
	if err := db.RecordSyncStatus(ctx, SyncStatusParams{
		ChannelID:    channelID,
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
		Success:      true,
		MessageCount: 1,
	}); err != nil {
		t.Fatal(err)
	}

	channels, err := db.ListChannels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found *Channel
	for i := range channels {
		if channels[i].ID == channelID {
			found = &channels[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("channel %d not found in %+v", channelID, channels)
	}
	if found.MessageCount != 1 || found.LastSyncSuccess == nil || found.LastSyncError != nil {
		t.Fatalf("channel sync summary = %+v", found)
	}

	if err := db.MarkMissingDeleted(ctx, channelID, []int{2}); err != nil {
		t.Fatal(err)
	}

	messages, err = db.ListMessages(ctx, ListMessagesParams{Channel: username, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages after delete = %+v", messages)
	}

	if err := db.UpsertMessage(ctx, UpsertMessageParams{
		ChannelID:     channelID,
		TelegramMsgID: 3,
		Text:          "third",
		TelegramDate:  date.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.MarkMissingDeleted(ctx, channelID, []int{}); err != nil {
		t.Fatal(err)
	}

	messages, err = db.ListMessages(ctx, ListMessagesParams{Channel: username, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages after empty reconciliation = %+v", messages)
	}
}
