// Package auth verifies Telegram identities. There are exactly two ways into
// this system: a Mini App request carrying signed initData, and a Bot update
// delivered by Telegram itself. Both end up as an authenticated users.User.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/users"
)

// InitData is the verified payload of a Telegram Mini App launch.
type InitData struct {
	User       users.Identity
	AuthDate   time.Time
	QueryID    string
	StartParam string
	ChatType   string
	Raw        string
}

// tgUser mirrors the JSON Telegram puts in the `user` field of initData.
type tgUser struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
	IsPremium    bool   `json:"is_premium"`
	PhotoURL     string `json:"photo_url"`
}

// ValidateInitData verifies the HMAC signature Telegram puts on Mini App
// launch parameters and returns the payload it authenticates.
//
// The algorithm is fixed by Telegram:
//
//	secret        = HMAC_SHA256(key: "WebAppData", data: bot_token)
//	expected_hash = HMAC_SHA256(key: secret,       data: data_check_string)
//
// where data_check_string is every received field *except `hash`*, sorted by
// key, rendered as "key=value" and joined with "\n".
//
// Only `hash` is excluded. `signature` — the Ed25519 field Telegram adds for
// third parties who do not hold the bot token — is part of what `hash` covers,
// and dropping it makes every launch from a client that sends one fail. It is
// the separate third-party check, not this one, that excludes both.
//
// Never skip this: the `user` field is attacker controlled until the hash
// checks out.
func ValidateInitData(raw, botToken string, ttl time.Duration, now time.Time) (InitData, error) {
	if strings.TrimSpace(raw) == "" {
		return InitData{}, core.Unauthorized("missing Telegram init data")
	}
	if botToken == "" {
		return InitData{}, core.Internal(fmt.Errorf("auth: bot token not configured"))
	}

	values, err := url.ParseQuery(raw)
	if err != nil {
		return InitData{}, core.Unauthorized("malformed Telegram init data")
	}
	hash := values.Get("hash")
	if hash == "" {
		return InitData{}, core.Unauthorized("Telegram init data is not signed")
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		if key == "hash" {
			continue
		}
		keys = append(keys, key)
	}
	// Sorted by key, not by the rendered "key=value" pair: a key that is a
	// prefix of another would otherwise order on the character following it.
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+values[key][0])
	}

	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	expected := hmacSHA256(secret, []byte(strings.Join(pairs, "\n")))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(expected)), []byte(strings.ToLower(hash))) != 1 {
		return InitData{}, core.Unauthorized("Telegram init data signature is invalid")
	}

	authDateRaw := values.Get("auth_date")
	seconds, err := strconv.ParseInt(authDateRaw, 10, 64)
	if err != nil {
		return InitData{}, core.Unauthorized("Telegram init data has no auth date")
	}
	authDate := time.Unix(seconds, 0).UTC()
	if ttl > 0 && now.Sub(authDate) > ttl {
		return InitData{}, core.Unauthorized("Telegram session has expired, please reopen the app")
	}

	var tu tgUser
	if err := json.Unmarshal([]byte(values.Get("user")), &tu); err != nil || tu.ID == 0 {
		return InitData{}, core.Unauthorized("Telegram init data has no user")
	}
	if tu.IsBot {
		return InitData{}, core.Forbidden("bots cannot use this app")
	}

	return InitData{
		User: users.Identity{
			TelegramID:   tu.ID,
			Username:     tu.Username,
			FirstName:    tu.FirstName,
			LastName:     tu.LastName,
			LanguageCode: tu.LanguageCode,
			PhotoURL:     tu.PhotoURL,
			IsPremium:    tu.IsPremium,
		},
		AuthDate:   authDate,
		QueryID:    values.Get("query_id"),
		StartParam: values.Get("start_param"),
		ChatType:   values.Get("chat_type"),
		Raw:        raw,
	}, nil
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// SignInitData produces valid initData for a set of fields, the way Telegram
// would. It exists so tests (and the local dev harness) can exercise the real
// validation path instead of bypassing it.
//
// It signs everything except `hash`, which is what makes it a faithful stand-in
// — including a `signature` field when one is passed.
func SignInitData(botToken string, fields map[string]string) string {
	pairs := make([]string, 0, len(fields))
	for k, v := range fields {
		if k == "hash" {
			continue
		}
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	hash := hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(pairs, "\n"))))

	q := url.Values{}
	for k, v := range fields {
		q.Set(k, v)
	}
	q.Set("hash", hash)
	return q.Encode()
}
