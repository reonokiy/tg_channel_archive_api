package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tg-channel-archive-api/internal/store"
)

type fakeStore struct {
	channels     []store.Channel
	messages     []store.Message
	media        *store.MessageMedia
	latest       *store.Message
	listParams   store.ListMessagesParams
	listErr      error
	latestErr    error
	pingErr      error
	latestCalled bool
}

func (f *fakeStore) Ping(_ context.Context) error {
	return f.pingErr
}

func (f *fakeStore) ListChannels(_ context.Context) ([]store.Channel, error) {
	return f.channels, f.listErr
}

func (f *fakeStore) ListMessages(_ context.Context, params store.ListMessagesParams) ([]store.Message, error) {
	f.listParams = params
	return f.messages, f.listErr
}

func (f *fakeStore) LatestMessage(_ context.Context, channel string) (*store.Message, error) {
	f.latestCalled = true
	if f.latest != nil {
		f.latest.ChannelUsername = channel
	}
	return f.latest, f.latestErr
}

func (f *fakeStore) GetMessageMedia(_ context.Context, _ string, _ int) (*store.MessageMedia, error) {
	return f.media, f.listErr
}

func TestHealth(t *testing.T) {
	server := NewServer(&fakeStore{}, Config{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHealthDatabaseUnavailable(t *testing.T) {
	server := NewServer(&fakeStore{pingErr: errors.New("down")}, Config{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestListMessages(t *testing.T) {
	db := &fakeStore{
		messages: []store.Message{
			{
				ID:            1,
				TelegramMsgID: 10,
				Text:          "hello",
				TelegramDate:  time.Unix(100, 0).UTC(),
			},
			{
				ID:            2,
				TelegramMsgID: 9,
				Text:          "older",
				TelegramDate:  time.Unix(90, 0).UTC(),
			},
		},
	}
	server := NewServer(db, Config{
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		CORSOrigin:     "*",
		DefaultLimit:   20,
		MaxLimit:       50,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/messages?channel=test&q=hello&limit=999", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control = %q", rec.Header().Get("Cache-Control"))
	}
	if db.listParams.Channel != "test" {
		t.Fatalf("channel = %q", db.listParams.Channel)
	}
	if db.listParams.Query != "hello" {
		t.Fatalf("query = %q", db.listParams.Query)
	}
	if db.listParams.Limit != 51 {
		t.Fatalf("limit = %d", db.listParams.Limit)
	}

	var body struct {
		Limit      int             `json:"limit"`
		HasMore    bool            `json:"has_more"`
		NextBefore string          `json:"next_before"`
		Messages   []store.Message `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Limit != 50 || len(body.Messages) != 2 || body.Messages[0].Text != "hello" {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.HasMore || body.NextBefore != "" {
		t.Fatalf("unexpected pagination: %+v", body)
	}
}

func TestListMessagesPaginationMetadata(t *testing.T) {
	db := &fakeStore{
		messages: []store.Message{
			{ID: 1, TelegramMsgID: 3, Text: "new", TelegramDate: time.Unix(300, 0).UTC()},
			{ID: 2, TelegramMsgID: 2, Text: "middle", TelegramDate: time.Unix(200, 0).UTC()},
			{ID: 3, TelegramMsgID: 1, Text: "old", TelegramDate: time.Unix(100, 0).UTC()},
		},
	}
	server := NewServer(db, Config{
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		DefaultLimit:   20,
		MaxLimit:       50,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/messages?limit=2", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Limit      int             `json:"limit"`
		HasMore    bool            `json:"has_more"`
		NextBefore string          `json:"next_before"`
		Messages   []store.Message `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if db.listParams.Limit != 3 {
		t.Fatalf("store limit = %d", db.listParams.Limit)
	}
	if body.Limit != 2 || !body.HasMore || len(body.Messages) != 2 {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.NextBefore != time.Unix(200, 0).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("next_before = %q", body.NextBefore)
	}
}

func TestListChannels(t *testing.T) {
	db := &fakeStore{
		channels: []store.Channel{
			{ID: 1, PeerID: 100, Username: "test", Title: "Test", MessageCount: 3},
		},
	}
	server := NewServer(db, Config{
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		DefaultLimit:   20,
		MaxLimit:       50,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Channels []store.Channel `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Channels) != 1 || body.Channels[0].MessageCount != 3 {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestListMessagesRejectsInvalidTime(t *testing.T) {
	server := NewServer(&fakeStore{}, Config{
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		DefaultLimit:   20,
		MaxLimit:       50,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/messages?before=nope", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestLatestMessageNotFound(t *testing.T) {
	server := NewServer(&fakeStore{}, Config{
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		DefaultLimit:   20,
		MaxLimit:       50,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/messages/latest?channel=test", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestProxyMedia(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/photos/file.jpg" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("image-data"))
	}))
	defer upstream.Close()

	server := NewServer(&fakeStore{
		media: &store.MessageMedia{MediaType: "photo", MediaFilePath: "photos/file.jpg"},
	}, Config{
		RateLimitRPS:     100,
		RateLimitBurst:   100,
		DefaultLimit:     20,
		MaxLimit:         50,
		MediaBaseURL:     upstream.URL,
		MediaCache:       true,
		MediaCacheHeader: "public, max-age=60",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/media?channel=test&message_id=10", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("cache control = %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Body.String() != "image-data" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestProxyMediaNoStoreWhenCacheDisabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("image-data"))
	}))
	defer upstream.Close()

	server := NewServer(&fakeStore{
		media: &store.MessageMedia{MediaType: "photo", MediaFilePath: "photos/file.jpg"},
	}, Config{
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		DefaultLimit:   20,
		MaxLimit:       50,
		MediaBaseURL:   upstream.URL,
		MediaCache:     false,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/media?channel=test&message_id=10", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control = %q", rec.Header().Get("Cache-Control"))
	}
}

func TestProxyMediaRequiresParams(t *testing.T) {
	server := NewServer(&fakeStore{}, Config{
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		DefaultLimit:   20,
		MaxLimit:       50,
		MediaBaseURL:   "http://example.invalid",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/media", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRateLimit(t *testing.T) {
	server := NewServer(&fakeStore{}, Config{
		RateLimitRPS:   1,
		RateLimitBurst: 1,
		DefaultLimit:   20,
		MaxLimit:       50,
	})

	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodGet, "/api/messages", nil)
		req.RemoteAddr = "203.0.113.10:1234"
		rec := httptest.NewRecorder()
		server.Routes().ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d status = %d, want %d", i, rec.Code, want)
		}
	}
}

func TestRateLimitIgnoresForwardedForByDefault(t *testing.T) {
	server := NewServer(&fakeStore{}, Config{
		RateLimitRPS:   1,
		RateLimitBurst: 1,
		DefaultLimit:   20,
		MaxLimit:       50,
	})

	for i, forwardedFor := range []string{"198.51.100.1", "198.51.100.2"} {
		req := httptest.NewRequest(http.MethodGet, "/api/messages", nil)
		req.RemoteAddr = "203.0.113.10:1234"
		req.Header.Set("X-Forwarded-For", forwardedFor)
		rec := httptest.NewRecorder()
		server.Routes().ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 1 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("request %d status = %d, want %d", i, rec.Code, want)
		}
	}
}

func TestRateLimitTrustsForwardedForWhenConfigured(t *testing.T) {
	server := NewServer(&fakeStore{}, Config{
		RateLimitRPS:   1,
		RateLimitBurst: 1,
		DefaultLimit:   20,
		MaxLimit:       50,
		TrustProxy:     true,
	})

	for i, forwardedFor := range []string{"198.51.100.1", "198.51.100.2"} {
		req := httptest.NewRequest(http.MethodGet, "/api/messages", nil)
		req.RemoteAddr = "203.0.113.10:1234"
		req.Header.Set("X-Forwarded-For", forwardedFor)
		rec := httptest.NewRecorder()
		server.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d", i, rec.Code)
		}
	}
}

func TestStoreErrorsReturnInternalServerError(t *testing.T) {
	server := NewServer(&fakeStore{listErr: errors.New("boom")}, Config{
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		DefaultLimit:   20,
		MaxLimit:       50,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/messages", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
