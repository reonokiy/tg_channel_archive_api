package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/time/rate"

	"tg-channel-archive-api/internal/store"
)

type Config struct {
	RateLimitRPS   float64
	RateLimitBurst int
	CORSOrigin     string
	DefaultLimit   int
	MaxLimit       int
	MediaBaseURL   string
	TrustProxy     bool
}

type MessageStore interface {
	Ping(ctx context.Context) error
	ListChannels(ctx context.Context) ([]store.Channel, error)
	ListMessages(ctx context.Context, params store.ListMessagesParams) ([]store.Message, error)
	LatestMessage(ctx context.Context, channel string) (*store.Message, error)
	GetMessageMedia(ctx context.Context, channel string, telegramMsgID int) (*store.MessageMedia, error)
}

type Server struct {
	db       MessageStore
	cfg      Config
	clients  map[string]*rate.Limiter
	mu       sync.Mutex
	lastSeen map[string]time.Time
}

func NewServer(db MessageStore, cfg Config) *Server {
	return &Server{
		db:       db,
		cfg:      cfg,
		clients:  make(map[string]*rate.Limiter),
		lastSeen: make(map[string]time.Time),
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", s.health)
	r.Get("/api/channels", s.withRateLimit(s.listChannels))
	r.Get("/api/messages", s.withRateLimit(s.listMessages))
	r.Get("/api/messages/latest", s.withRateLimit(s.latestMessage))
	r.Get("/api/media", s.withRateLimit(s.proxyMedia))
	return s.withCORS(r)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unhealthy",
			"error":  "database unavailable",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.db.ListChannels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	limit := s.cfg.DefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(parsed, s.cfg.MaxLimit)
	}

	before, ok := parseOptionalTime(w, r.URL.Query().Get("before"), "before")
	if !ok {
		return
	}
	after, ok := parseOptionalTime(w, r.URL.Query().Get("after"), "after")
	if !ok {
		return
	}

	messages, err := s.db.ListMessages(r.Context(), store.ListMessagesParams{
		Channel: strings.TrimSpace(r.URL.Query().Get("channel")),
		Query:   strings.TrimSpace(r.URL.Query().Get("q")),
		Before:  before,
		After:   after,
		Limit:   limit + 1,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list messages")
		return
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	var nextBefore string
	if hasMore && len(messages) > 0 {
		nextBefore = messages[len(messages)-1].TelegramDate.Format(time.RFC3339Nano)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"messages":    messages,
		"limit":       limit,
		"has_more":    hasMore,
		"next_before": nextBefore,
	})
}

func (s *Server) latestMessage(w http.ResponseWriter, r *http.Request) {
	message, err := s.db.LatestMessage(r.Context(), strings.TrimSpace(r.URL.Query().Get("channel")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load latest message")
		return
	}
	if message == nil {
		writeError(w, http.StatusNotFound, "message not found")
		return
	}
	writeJSON(w, http.StatusOK, message)
}

func (s *Server) proxyMedia(w http.ResponseWriter, r *http.Request) {
	if s.cfg.MediaBaseURL == "" {
		writeError(w, http.StatusNotFound, "media proxy is disabled")
		return
	}
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	rawMessageID := strings.TrimSpace(r.URL.Query().Get("message_id"))
	if channel == "" || rawMessageID == "" {
		writeError(w, http.StatusBadRequest, "channel and message_id are required")
		return
	}
	messageID, err := strconv.Atoi(rawMessageID)
	if err != nil || messageID <= 0 {
		writeError(w, http.StatusBadRequest, "message_id must be a positive integer")
		return
	}

	media, err := s.db.GetMessageMedia(r.Context(), channel, messageID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load media")
		return
	}
	if media == nil || media.MediaFilePath == "" {
		writeError(w, http.StatusNotFound, "media not found")
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.cfg.MediaBaseURL+"/"+media.MediaFilePath, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create media request")
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch media")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeError(w, http.StatusBadGateway, "failed to fetch media")
		return
	}

	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
		w.Header().Set("Content-Length", contentLength)
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) withRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limiter := s.limiterFor(s.clientIP(r))
		if !limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next(w, r)
	}
}

func (s *Server) limiterFor(ip string) *rate.Limiter {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.clients) > 10000 {
		for key, seen := range s.lastSeen {
			if now.Sub(seen) > 10*time.Minute {
				delete(s.clients, key)
				delete(s.lastSeen, key)
			}
		}
	}

	limiter := s.clients[ip]
	if limiter == nil {
		limiter = rate.NewLimiter(rate.Limit(s.cfg.RateLimitRPS), s.cfg.RateLimitBurst)
		s.clients[ip] = limiter
	}
	s.lastSeen[ip] = now
	return limiter
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.CORSOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", s.cfg.CORSOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			return strings.TrimSpace(strings.Split(forwarded, ",")[0])
		}
	}
	if realIP := r.Header.Get("X-Real-IP"); s.cfg.TrustProxy && realIP != "" {
		return strings.TrimSpace(realIP)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func parseOptionalTime(w http.ResponseWriter, value, name string) (*time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		writeError(w, http.StatusBadRequest, name+" must be RFC3339 time")
		return nil, false
	}
	return &parsed, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
