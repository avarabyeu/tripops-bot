// Package telegram is the Telegram adapter: the bot, its keyboards, and the
// worker that delivers queued notifications.
//
// Nothing in here contains business rules. Handlers translate a tap into a
// domain call and translate the result back into a message, which is what
// keeps the bot and the REST API from drifting apart.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal Telegram Bot API client covering exactly the methods
// this product uses. A full SDK would add a dependency and a lot of surface
// for no benefit: the bot sends messages, edits them, and answers taps.
type Client struct {
	token string
	http  *http.Client
	base  string
}

func NewClient(token string) *Client {
	return &Client{
		token: token,
		http:  &http.Client{Timeout: 70 * time.Second}, // long polling needs headroom
		base:  "https://api.telegram.org",
	}
}

// APIError is a non-ok response from Telegram.
type APIError struct {
	Method      string
	Code        int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %s failed (%d): %s", e.Method, e.Code, e.Description)
}

// Blocked reports whether the user has blocked the bot or never started it, in
// which case retrying delivery is pointless.
func (e *APIError) Blocked() bool {
	d := strings.ToLower(e.Description)
	return e.Code == 403 ||
		strings.Contains(d, "chat not found") ||
		strings.Contains(d, "bot was blocked") ||
		strings.Contains(d, "user is deactivated")
}

func (c *Client) call(ctx context.Context, method string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: encode %s: %w", method, err)
	}
	url := fmt.Sprintf("%s/bot%s/%s", c.base, c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: build %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: call %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("telegram: decode %s: %w", method, err)
	}
	if !envelope.OK {
		return &APIError{Method: method, Code: envelope.ErrorCode, Description: envelope.Description}
	}
	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("telegram: decode %s result: %w", method, err)
		}
	}
	return nil
}

// ------------------------------------------------------------------- types --

type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
	IsPremium    bool   `json:"is_premium"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int64  `json:"file_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
}

type Message struct {
	MessageID int         `json:"message_id"`
	From      *User       `json:"from"`
	Chat      Chat        `json:"chat"`
	Date      int64       `json:"date"`
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Photo     []PhotoSize `json:"photo"`
	Document  *Document   `json:"document"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int            `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// InlineKeyboardButton is a tap target. Exactly one action field is set.
type InlineKeyboardButton struct {
	Text         string      `json:"text"`
	CallbackData string      `json:"callback_data,omitempty"`
	URL          string      `json:"url,omitempty"`
	WebApp       *WebAppInfo `json:"web_app,omitempty"`
}

type WebAppInfo struct {
	URL string `json:"url"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type SendMessageRequest struct {
	ChatID      int64                 `json:"chat_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	// LinkPreview is off everywhere: trip messages are dense and a preview card
	// pushes the actual content off screen.
	LinkPreviewOptions *linkPreviewOptions `json:"link_preview_options,omitempty"`
}

type linkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

type EditMessageTextRequest struct {
	ChatID             int64                 `json:"chat_id"`
	MessageID          int                   `json:"message_id"`
	Text               string                `json:"text"`
	ParseMode          string                `json:"parse_mode,omitempty"`
	ReplyMarkup        *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	LinkPreviewOptions *linkPreviewOptions   `json:"link_preview_options,omitempty"`
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// ----------------------------------------------------------------- methods --

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var me User
	err := c.call(ctx, "getMe", map[string]any{}, &me)
	return me, err
}

func (c *Client) SendMessage(ctx context.Context, req SendMessageRequest) (Message, error) {
	req.ParseMode = "HTML"
	req.LinkPreviewOptions = &linkPreviewOptions{IsDisabled: true}
	var msg Message
	err := c.call(ctx, "sendMessage", req, &msg)
	return msg, err
}

func (c *Client) EditMessageText(ctx context.Context, req EditMessageTextRequest) error {
	req.ParseMode = "HTML"
	req.LinkPreviewOptions = &linkPreviewOptions{IsDisabled: true}
	err := c.call(ctx, "editMessageText", req, nil)
	// Tapping the same button twice produces an identical message; Telegram
	// treats that as an error and it is not one.
	var apiErr *APIError
	if err != nil && asAPIError(err, &apiErr) && strings.Contains(apiErr.Description, "message is not modified") {
		return nil
	}
	return err
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string, alert bool) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": id,
		"text":              text,
		"show_alert":        alert,
	}, nil)
}

func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	return c.call(ctx, "setMyCommands", map[string]any{"commands": commands}, nil)
}

// GetUpdates is long polling, used in development where no public URL exists.
func (c *Client) GetUpdates(ctx context.Context, offset, timeout int) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeout,
		"allowed_updates": []string{"message", "callback_query"},
	}, &updates)
	return updates, err
}

func (c *Client) SetWebhook(ctx context.Context, url, secret string) error {
	return c.call(ctx, "setWebhook", map[string]any{
		"url":             url,
		"secret_token":    secret,
		"allowed_updates": []string{"message", "callback_query"},
	}, nil)
}

func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
}

// asAPIError unwraps a Telegram API error from anywhere in the error chain.
func asAPIError(err error, target **APIError) bool {
	return errors.As(err, target)
}

// EscapeHTML escapes the three characters Telegram's HTML parse mode cares
// about. Every piece of user text that goes into a message passes through it.
func EscapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
