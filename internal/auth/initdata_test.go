package auth

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

const testToken = "123456:TEST-BOT-TOKEN"

func signedInitData(t *testing.T, token string, authDate time.Time, extra map[string]string) string {
	t.Helper()
	fields := map[string]string{
		"auth_date": strconv.FormatInt(authDate.Unix(), 10),
		"query_id":  "AAAA",
		"user":      `{"id":42,"first_name":"AV","last_name":"V","username":"av","language_code":"en"}`,
	}
	for k, v := range extra {
		fields[k] = v
	}
	return SignInitData(token, fields)
}

func TestValidateInitDataAcceptsGenuineData(t *testing.T) {
	now := time.Now()
	raw := signedInitData(t, testToken, now, nil)

	data, err := ValidateInitData(raw, testToken, time.Hour, now)
	if err != nil {
		t.Fatalf("genuine init data was rejected: %v", err)
	}
	if data.User.TelegramID != 42 {
		t.Errorf("telegram id = %d, want 42", data.User.TelegramID)
	}
	if data.User.Username != "av" || data.User.FirstName != "AV" {
		t.Errorf("profile = %+v", data.User)
	}
	if data.QueryID != "AAAA" {
		t.Errorf("query id = %q", data.QueryID)
	}
}

// The whole point of the check: the user field is attacker controlled until
// the hash verifies.
func TestValidateInitDataRejectsTamperedUser(t *testing.T) {
	now := time.Now()
	raw := signedInitData(t, testToken, now, nil)
	tampered := strings.Replace(raw, "%2242%22", "%229999%22", 1)
	if tampered == raw {
		// The encoding differs; fall back to a blunt substitution.
		tampered = strings.Replace(raw, "42", "99", 1)
	}

	if _, err := ValidateInitData(tampered, testToken, time.Hour, now); err == nil {
		t.Fatal("tampered init data was accepted")
	} else if core.CodeOf(err) != core.CodeUnauthorized {
		t.Errorf("code = %s, want unauthorized", core.CodeOf(err))
	}
}

func TestValidateInitDataRejectsWrongToken(t *testing.T) {
	now := time.Now()
	raw := signedInitData(t, testToken, now, nil)

	if _, err := ValidateInitData(raw, "999:SOMEONE-ELSES-TOKEN", time.Hour, now); err == nil {
		t.Fatal("init data signed with another bot's token was accepted")
	}
}

func TestValidateInitDataRejectsStaleData(t *testing.T) {
	now := time.Now()
	raw := signedInitData(t, testToken, now.Add(-48*time.Hour), nil)

	if _, err := ValidateInitData(raw, testToken, 24*time.Hour, now); err == nil {
		t.Fatal("expired init data was accepted")
	}
	// A zero TTL means "do not expire", for deployments that prefer to rely on
	// Telegram's own session handling.
	if _, err := ValidateInitData(raw, testToken, 0, now); err != nil {
		t.Errorf("TTL 0 should disable the age check: %v", err)
	}
}

func TestValidateInitDataRejectsMalformedInput(t *testing.T) {
	now := time.Now()
	cases := map[string]string{
		"empty":       "",
		"no hash":     "auth_date=1&user=%7B%22id%22%3A1%7D",
		"no user":     SignInitData(testToken, map[string]string{"auth_date": "1"}),
		"broken user": SignInitData(testToken, map[string]string{"auth_date": "1", "user": "not json"}),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateInitData(raw, testToken, 0, now); err == nil {
				t.Error("expected rejection")
			}
		})
	}
}

// Telegram adds `signature` next to `hash` for third parties who do not hold
// the bot token. It is part of what `hash` covers, so it belongs in the
// data-check-string — excluding it made every launch from a client that sends
// one fail with "signature is invalid", which is what shipped.
//
// Only the separate third-party Ed25519 check excludes both fields.
func TestValidateInitDataCoversTheSignatureField(t *testing.T) {
	now := time.Now()
	raw := signedInitData(t, testToken, now, map[string]string{
		"signature": "TgqCFa8kRHTHYJ4qm7sDOQ5eHGJkcCFa8kRHTHYJ4qm",
	})

	data, err := ValidateInitData(raw, testToken, time.Hour, now)
	if err != nil {
		t.Fatalf("init data carrying a signature was rejected: %v", err)
	}
	if data.User.TelegramID != 42 {
		t.Errorf("telegram id = %d, want 42", data.User.TelegramID)
	}
}

// The other half of that: because `signature` is covered, one bolted on after
// the fact does not verify. Anything else would let a field be added to signed
// launch parameters unnoticed.
func TestValidateInitDataRejectsAppendedSignature(t *testing.T) {
	now := time.Now()
	raw := signedInitData(t, testToken, now, nil) + "&signature=bolted-on"

	if _, err := ValidateInitData(raw, testToken, time.Hour, now); err == nil {
		t.Fatal("a field appended after signing was accepted")
	}
}

// Real launch parameters carry more than the three fields the other tests use;
// the check string has to be built from all of them, in key order.
func TestValidateInitDataWithAFullPayload(t *testing.T) {
	now := time.Now()
	raw := signedInitData(t, testToken, now, map[string]string{
		"chat_instance":  "-3788475317572404878",
		"chat_type":      "sender",
		"signature":      "1fk5C1Xx8Xy3pAhWQ0wUvuGJ8DlKxTqPfnTLBPBlUJk",
		"start_param":    "inv_abc123",
		"can_send_after": "3600",
	})

	data, err := ValidateInitData(raw, testToken, time.Hour, now)
	if err != nil {
		t.Fatalf("a full payload was rejected: %v", err)
	}
	if data.StartParam != "inv_abc123" {
		t.Errorf("start_param = %q, want the deep link payload", data.StartParam)
	}
	if data.ChatType != "sender" {
		t.Errorf("chat_type = %q", data.ChatType)
	}
}

func TestValidateInitDataRejectsBots(t *testing.T) {
	now := time.Now()
	raw := signedInitData(t, testToken, now, map[string]string{
		"user": `{"id":42,"is_bot":true,"first_name":"Spammer"}`,
	})

	if _, err := ValidateInitData(raw, testToken, time.Hour, now); err == nil {
		t.Fatal("a bot identity was accepted")
	} else if core.CodeOf(err) != core.CodeForbidden {
		t.Errorf("code = %s, want forbidden", core.CodeOf(err))
	}
}

func TestValidateInitDataRequiresConfiguredToken(t *testing.T) {
	now := time.Now()
	raw := signedInitData(t, testToken, now, nil)

	if _, err := ValidateInitData(raw, "", time.Hour, now); err == nil {
		t.Fatal("validation without a bot token must fail closed")
	}
}
