package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/wacrawl/internal/store"
)

//go:embed static/*
var assets embed.FS

const (
	snippetStartMarker = "\ue000"
	snippetEndMarker   = "\ue001"

	// maxInlineMediaBytes caps how large an archived image may be before the
	// viewer falls back to the metadata card instead of inlining bytes.
	maxInlineMediaBytes = 25 << 20
)

type Config struct {
	Port   int
	Output io.Writer
}

type handler struct {
	store       *store.Store
	token       string
	allowedHost string
}

type statusResponse struct {
	Chats          int       `json:"chats"`
	UnreadChats    int       `json:"unread_chats"`
	UnreadMessages int       `json:"unread_messages"`
	Contacts       int       `json:"contacts"`
	Groups         int       `json:"groups"`
	Messages       int       `json:"messages"`
	MediaMessages  int       `json:"media_messages"`
	OldestMessage  time.Time `json:"oldest_message,omitzero"`
	NewestMessage  time.Time `json:"newest_message,omitzero"`
	LastImportAt   time.Time `json:"last_import_at,omitzero"`
}

type chatResponse struct {
	JID           string    `json:"jid"`
	Kind          string    `json:"kind"`
	Name          string    `json:"name,omitempty"`
	LastMessageAt time.Time `json:"last_message_at,omitzero"`
	UnreadCount   int       `json:"unread_count"`
	Archived      bool      `json:"archived"`
	MessageCount  int       `json:"message_count"`
}

type messageResponse struct {
	SourcePK    int64     `json:"source_pk"`
	ChatJID     string    `json:"chat_jid"`
	ChatName    string    `json:"chat_name,omitempty"`
	MessageID   string    `json:"message_id"`
	SenderJID   string    `json:"sender_jid,omitempty"`
	SenderName  string    `json:"sender_name,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	FromMe      bool      `json:"from_me"`
	Text        string    `json:"text,omitempty"`
	MessageType string    `json:"message_type,omitempty"`
	MediaType   string    `json:"media_type,omitempty"`
	MediaTitle  string    `json:"media_title,omitempty"`
	MediaSize   int64     `json:"media_size,omitempty"`
	Starred     bool      `json:"starred,omitempty"`
	Snippet     string    `json:"snippet,omitempty"`
}

func Serve(ctx context.Context, archive *store.Store, cfg Config) error {
	if cfg.Port < 0 || cfg.Port > 65535 {
		return errors.New("web port must be between 0 and 65535")
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("listen for web viewer: %w", err)
	}
	defer func() { _ = listener.Close() }()

	token, err := randomToken()
	if err != nil {
		return err
	}
	host := listener.Addr().String()
	url := "http://" + host + "/#" + token
	output := cfg.Output
	if output == nil {
		output = io.Discard
	}
	_, _ = fmt.Fprintf(output, "wacrawl web viewer\n%s\n\nLocal and read-only. Keep this URL private; Ctrl-C stops the server.\n", url)

	server := &http.Server{
		Handler:           NewHandler(archive, token, host),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
	}
	stopWatcher := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		case <-stopWatcher:
		}
	}()

	err = server.Serve(listener)
	close(stopWatcher)
	<-watcherDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve web viewer: %w", err)
	}
	return nil
}

func NewHandler(archive *store.Store, token, allowedHost string) http.Handler {
	return &handler{store: archive, token: token, allowedHost: allowedHost}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	if r.Host != h.allowedHost {
		http.Error(w, "unexpected host", http.StatusMisdirectedRequest)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	switch r.URL.Path {
	case "/":
		h.serveAsset(w, "static/index.html", "text/html; charset=utf-8")
		return
	case "/app.css":
		h.serveAsset(w, "static/app.css", "text/css; charset=utf-8")
		return
	case "/app.js":
		h.serveAsset(w, "static/app.js", "text/javascript; charset=utf-8")
		return
	case "/favicon.svg", "/favicon.ico":
		h.serveAsset(w, "static/favicon.svg", "image/svg+xml")
		return
	}

	if !strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "authorization required", http.StatusUnauthorized)
		return
	}

	switch r.URL.Path {
	case "/api/status":
		h.serveStatus(w, r)
	case "/api/chats":
		h.serveChats(w, r)
	case "/api/messages":
		h.serveMessages(w, r)
	case "/api/search":
		h.serveSearch(w, r)
	case "/api/media":
		h.serveMedia(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *handler) serveAsset(w http.ResponseWriter, name, contentType string) {
	body, err := assets.ReadFile(name)
	if err != nil {
		http.Error(w, "asset unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *handler) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	candidate := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if len(candidate) != len(h.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(h.token)) == 1
}

func (h *handler) serveStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.store.Status(r.Context())
	if err != nil {
		writeArchiveError(w)
		return
	}
	writeJSON(w, statusResponse{
		Chats:          status.Chats,
		UnreadChats:    status.UnreadChats,
		UnreadMessages: status.UnreadMessages,
		Contacts:       status.Contacts,
		Groups:         status.Groups,
		Messages:       status.Messages,
		MediaMessages:  status.MediaMessages,
		OldestMessage:  status.OldestMessage,
		NewestMessage:  status.NewestMessage,
		LastImportAt:   status.LastImportAt,
	})
}

func (h *handler) serveChats(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 100)
	if !ok {
		return
	}
	chats, err := h.store.ListChats(r.Context(), limit)
	if err != nil {
		writeArchiveError(w)
		return
	}
	out := make([]chatResponse, 0, len(chats))
	for _, chat := range chats {
		out = append(out, chatResponse{
			JID:           chat.JID,
			Kind:          chat.Kind,
			Name:          chat.Name,
			LastMessageAt: chat.LastMessageAt,
			UnreadCount:   chat.UnreadCount,
			Archived:      chat.Archived,
			MessageCount:  chat.MessageCount,
		})
	}
	writeJSON(w, out)
}

func (h *handler) serveMessages(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 100)
	if !ok {
		return
	}
	before, beforePK, ok := parseBefore(w, r)
	if !ok {
		return
	}
	messages, err := h.store.Messages(r.Context(), store.MessageFilter{
		ChatJID:  strings.TrimSpace(r.URL.Query().Get("chat")),
		Limit:    limit,
		Before:   before,
		BeforePK: beforePK,
	})
	if err != nil {
		writeArchiveError(w)
		return
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	writeJSON(w, messagesForWeb(messages))
}

func (h *handler) serveSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		http.Error(w, "search query required", http.StatusBadRequest)
		return
	}
	limit, ok := parseLimit(w, r, 100)
	if !ok {
		return
	}
	messages, err := h.store.Search(r.Context(), store.MessageFilter{
		Query: query,
		Limit: limit,
		// Private-use markers cannot collide with real message text, so the
		// client can turn them into highlight nodes without guessing.
		SnippetStart: snippetStartMarker,
		SnippetEnd:   snippetEndMarker,
	})
	if err != nil {
		http.Error(w, "invalid search query", http.StatusBadRequest)
		return
	}
	writeJSON(w, messagesForWeb(messages))
}

func messagesForWeb(messages []store.Message) []messageResponse {
	out := make([]messageResponse, 0, len(messages))
	for _, message := range messages {
		out = append(out, messageResponse{
			SourcePK:    message.SourcePK,
			ChatJID:     message.ChatJID,
			ChatName:    message.ChatName,
			MessageID:   message.MessageID,
			SenderJID:   message.SenderJID,
			SenderName:  message.SenderName,
			Timestamp:   message.Timestamp,
			FromMe:      message.FromMe,
			Text:        message.Text,
			MessageType: message.MessageType,
			MediaType:   message.MediaType,
			MediaTitle:  message.MediaTitle,
			MediaSize:   message.MediaSize,
			Starred:     message.Starred,
			Snippet:     message.Snippet,
		})
	}
	return out
}

// serveMedia streams archived image bytes for one message so the viewer can
// render photos, stickers, and GIFs inline. It reads local files referenced by
// our own importer, serves only content that sniffs as an image, and never
// reveals the underlying path.
func (h *handler) serveMedia(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("pk"))
	sourcePK, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || sourcePK <= 0 {
		http.Error(w, "pk must be a positive integer", http.StatusBadRequest)
		return
	}
	message, err := h.store.MessageBySourcePK(r.Context(), sourcePK)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeArchiveError(w)
		return
	}
	path := strings.TrimSpace(message.MediaPath)
	if path == "" || !inlineImageMessage(message) {
		http.Error(w, "no inline preview", http.StatusNotFound)
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	if info.Size() > maxInlineMediaBytes {
		http.Error(w, "media too large for inline preview", http.StatusRequestEntityTooLarge)
		return
	}
	file, err := os.Open(path) // #nosec G304 -- media_path was written by our own importer, not request input.
	if err != nil {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	defer func() { _ = file.Close() }()

	sniff := make([]byte, 512)
	n, err := io.ReadFull(file, sniff)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	sniff = sniff[:n]
	contentType := http.DetectContentType(sniff)
	if !strings.HasPrefix(contentType, "image/") {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(sniff); err != nil {
		return
	}
	_, _ = io.Copy(w, file)
}

func inlineImageMessage(message store.Message) bool {
	hint := strings.ToLower(message.MediaType + " " + message.MessageType)
	for _, kind := range []string{"image", "photo", "sticker", "gif"} {
		if strings.Contains(hint, kind) {
			return true
		}
	}
	return false
}

func parseBefore(w http.ResponseWriter, r *http.Request) (*time.Time, int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("before"))
	rawPK := strings.TrimSpace(r.URL.Query().Get("before_pk"))
	if raw == "" {
		if rawPK != "" {
			http.Error(w, "before_pk requires before", http.StatusBadRequest)
			return nil, 0, false
		}
		return nil, 0, true
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 {
		http.Error(w, "before must be a positive unix timestamp", http.StatusBadRequest)
		return nil, 0, false
	}
	var beforePK int64
	if rawPK != "" {
		beforePK, err = strconv.ParseInt(rawPK, 10, 64)
		if err != nil || beforePK <= 0 {
			http.Error(w, "before_pk must be a positive integer", http.StatusBadRequest)
			return nil, 0, false
		}
	}
	before := time.Unix(seconds, 0).UTC()
	return &before, beforePK, true
}

func parseLimit(w http.ResponseWriter, r *http.Request, fallback int) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 500 {
		http.Error(w, "limit must be between 1 and 500", http.StatusBadRequest)
		return 0, false
	}
	return limit, true
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate web viewer token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		writeArchiveError(w)
	}
}

func writeArchiveError(w http.ResponseWriter) {
	http.Error(w, "archive unavailable", http.StatusInternalServerError)
}
