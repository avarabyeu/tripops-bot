package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/auth"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/testsupport"
)

// client drives the real HTTP surface as one Telegram user.
type client struct {
	t        *testing.T
	server   *httptest.Server
	initData string
}

func newClient(t *testing.T, server *httptest.Server, telegramID int64, name string) *client {
	t.Helper()
	initData := auth.SignInitData(testsupport.BotToken, map[string]string{
		"auth_date": strconv.FormatInt(time.Now().Unix(), 10),
		"user": fmt.Sprintf(`{"id":%d,"first_name":%q,"username":%q}`,
			telegramID, name, name),
	})
	return &client{t: t, server: server, initData: initData}
}

// do sends a request and decodes the response into out when status matches.
func (c *client) do(method, path string, body any, wantStatus int, out any) {
	c.t.Helper()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encode request: %v", err)
		}
	}
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.server.URL+path, bytes.NewReader(payload))
	if err != nil {
		c.t.Fatalf("build request: %v", err)
	}
	// The Mini App authenticates every call with the launch parameters
	// Telegram signed; there is no session and no cookie.
	req.Header.Set("Authorization", "tma "+c.initData)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.server.Client().Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != wantStatus {
		var raw bytes.Buffer
		_, _ = raw.ReadFrom(resp.Body)
		c.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, wantStatus, raw.String())
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			c.t.Fatalf("decode %s %s: %v", method, path, err)
		}
	}
}

func newServer(t *testing.T) (*httptest.Server, *testsupport.App) {
	t.Helper()
	app := testsupport.NewApp(t)
	server := httptest.NewServer(app.API.Handler())
	t.Cleanup(server.Close)
	return server, app
}

// TestAPIRejectsUnauthenticatedCalls is the guarantee behind every other test
// here: nothing is reachable without valid Telegram init data.
func TestAPIRejectsUnauthenticatedCalls(t *testing.T) {
	server, _ := newServer(t)

	for _, path := range []string{"/api/v1/me", "/api/v1/trips"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without credentials = %d, want 401", path, resp.StatusCode)
		}
	}

	// A forged identity must fail the same way.
	forged := "auth_date=" + strconv.FormatInt(time.Now().Unix(), 10) +
		"&user=%7B%22id%22%3A1%2C%22first_name%22%3A%22Mallory%22%7D&hash=deadbeef"
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v1/me", nil)
	req.Header.Set("Authorization", "tma "+forged)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("forged init data = %d, want 401", resp.StatusCode)
	}
}

func TestHealthEndpoints(t *testing.T) {
	server, _ := newServer(t)

	for path, want := range map[string]int{"/healthz": 200, "/readyz": 200} {
		resp, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

// TestAPIInvitationFlow exercises the flow a real group goes through, over
// HTTP: the organiser creates a trip and a link, a friend previews it and
// joins, and only then can see anything.
func TestAPIInvitationFlow(t *testing.T) {
	server, _ := newServer(t)
	organiser := newClient(t, server, 7001, "Organiser")
	friend := newClient(t, server, 7002, "Friend")

	var me struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	organiser.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK, &me)
	if me.Name != "Organiser" {
		t.Errorf("identity resolved to %q", me.Name)
	}

	var trip struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Currency string `json:"currency"`
	}
	organiser.do(http.MethodPost, "/api/v1/trips", map[string]any{
		"title":      "Brevet Łódź 200",
		"start_date": "2026-09-23",
		"end_date":   "2026-09-24",
		"timezone":   "Europe/Warsaw",
		"currency":   "EUR",
	}, http.StatusCreated, &trip)
	if trip.ID == "" || trip.Currency != "EUR" {
		t.Fatalf("trip = %+v", trip)
	}

	// Validation is enforced server side, not just in the client.
	organiser.do(http.MethodPost, "/api/v1/trips", map[string]any{
		"title":      "",
		"start_date": "2026-09-24",
		"end_date":   "2026-09-23",
	}, http.StatusBadRequest, nil)

	// The friend is not a member yet: the trip must look like it does not exist.
	friend.do(http.MethodGet, "/api/v1/trips/"+trip.ID, nil, http.StatusNotFound, nil)

	var invite struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	organiser.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/invites",
		map[string]any{"role": "member"}, http.StatusCreated, &invite)
	if invite.Token == "" {
		t.Fatal("invite has no token")
	}

	var preview struct {
		Trip struct {
			Title string `json:"title"`
		} `json:"trip"`
		OwnerName   string `json:"owner_name"`
		MemberCount int    `json:"member_count"`
	}
	friend.do(http.MethodGet, "/api/v1/invites/"+invite.Token, nil, http.StatusOK, &preview)
	if preview.Trip.Title != "Brevet Łódź 200" || preview.OwnerName != "Organiser" {
		t.Errorf("preview = %+v", preview)
	}

	friend.do(http.MethodPost, "/api/v1/invites/"+invite.Token+"/join", nil, http.StatusOK, nil)
	friend.do(http.MethodGet, "/api/v1/trips/"+trip.ID, nil, http.StatusOK, nil)

	var members struct {
		Members []struct {
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
		} `json:"members"`
	}
	friend.do(http.MethodGet, "/api/v1/trips/"+trip.ID+"/members", nil, http.StatusOK, &members)
	if len(members.Members) != 2 {
		t.Fatalf("members = %+v", members.Members)
	}

	// A plain member may look, but not run the trip.
	friend.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/events", map[string]any{
		"title":    "Departure",
		"type":     "departure",
		"start_at": "2026-09-23T17:00:00Z",
	}, http.StatusForbidden, nil)

	var event struct {
		ID           string `json:"id"`
		Participants []struct {
			DisplayName string `json:"display_name"`
			Status      string `json:"status"`
		} `json:"participants"`
	}
	organiser.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/events", map[string]any{
		"title":    "Departure",
		"type":     "departure",
		"start_at": "2026-09-23T17:00:00Z",
	}, http.StatusCreated, &event)
	if len(event.Participants) != 2 {
		t.Errorf("a new event should include everyone, got %+v", event.Participants)
	}

	// Everyone answers for themselves.
	organiser.do(http.MethodPost,
		"/api/v1/trips/"+trip.ID+"/events/"+event.ID+"/rsvp",
		map[string]any{"status": "attending"}, http.StatusOK, nil)
	friend.do(http.MethodPost,
		"/api/v1/trips/"+trip.ID+"/events/"+event.ID+"/rsvp",
		map[string]any{"status": "maybe"}, http.StatusOK, nil)

	var dashboard struct {
		People struct {
			Active int `json:"active"`
		} `json:"people"`
		NextEvent *struct {
			Title string `json:"title"`
		} `json:"next_event"`
	}
	friend.do(http.MethodGet, "/api/v1/trips/"+trip.ID+"/dashboard", nil, http.StatusOK, &dashboard)
	if dashboard.People.Active != 2 {
		t.Errorf("dashboard people = %d, want 2", dashboard.People.Active)
	}
	if dashboard.NextEvent == nil || dashboard.NextEvent.Title != "Departure" {
		t.Errorf("dashboard next event = %+v", dashboard.NextEvent)
	}
}

// TestAPIExpensesAndSettlement covers the money path over HTTP, including the
// "Mark as settled" button the balances screen offers.
func TestAPIExpensesAndSettlement(t *testing.T) {
	server, _ := newServer(t)
	alice := newClient(t, server, 7101, "Alice")
	bob := newClient(t, server, 7102, "Bob")

	var trip struct {
		ID string `json:"id"`
	}
	alice.do(http.MethodPost, "/api/v1/trips", map[string]any{
		"title": "Weekend", "start_date": "2026-05-01", "end_date": "2026-05-03",
	}, http.StatusCreated, &trip)

	var invite struct {
		Token string `json:"token"`
	}
	alice.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/invites", map[string]any{}, http.StatusCreated, &invite)
	bob.do(http.MethodPost, "/api/v1/invites/"+invite.Token+"/join", nil, http.StatusOK, nil)

	// Alice pays 80.00 for both of them.
	alice.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/expenses", map[string]any{
		"title": "Fuel", "amount_minor": 8000, "category": "fuel",
	}, http.StatusCreated, nil)

	var balances struct {
		Total    int64 `json:"total_minor"`
		Balances []struct {
			DisplayName string `json:"display_name"`
			Amount      int64  `json:"balance_minor"`
			MemberID    string `json:"member_id"`
		} `json:"balances"`
		Transfers []struct {
			From     string `json:"from_member_id"`
			To       string `json:"to_member_id"`
			FromName string `json:"from_name"`
			ToName   string `json:"to_name"`
			Amount   int64  `json:"amount_minor"`
		} `json:"transfers"`
	}
	bob.do(http.MethodGet, "/api/v1/trips/"+trip.ID+"/balances", nil, http.StatusOK, &balances)
	if balances.Total != 8000 {
		t.Errorf("total = %d, want 8000", balances.Total)
	}
	if len(balances.Transfers) != 1 {
		t.Fatalf("transfers = %+v", balances.Transfers)
	}
	transfer := balances.Transfers[0]
	if transfer.FromName != "Bob" || transfer.ToName != "Alice" || transfer.Amount != 4000 {
		t.Errorf("transfer = %+v, want Bob → Alice 40.00", transfer)
	}

	// Bob marks his half as paid.
	var settlement struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	bob.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/settlements", map[string]any{
		"from_member_id": transfer.From,
		"to_member_id":   transfer.To,
		"amount_minor":   transfer.Amount,
	}, http.StatusCreated, &settlement)
	if settlement.Status != "settled" {
		t.Errorf("settlement status = %q", settlement.Status)
	}

	balances.Transfers = nil
	bob.do(http.MethodGet, "/api/v1/trips/"+trip.ID+"/balances", nil, http.StatusOK, &balances)
	if len(balances.Transfers) != 0 {
		t.Errorf("nothing should be outstanding, got %+v", balances.Transfers)
	}
	for _, b := range balances.Balances {
		if b.Amount != 0 {
			t.Errorf("%s is at %d after settling", b.DisplayName, b.Amount)
		}
	}
}

// TestAPIAcceptsCompactTripIDs covers the path a Telegram deep link takes.
//
// The bot builds "📱 Open in app" as `?startapp=trip_<compact id>` — the 22
// character form, because callback payloads are capped at 64 bytes — and the
// Mini App passes whatever it was launched with straight to the API. Parsing
// only the canonical form meant every trip opened from a bot message failed
// with "tripID is not a valid id".
func TestAPIAcceptsCompactTripIDs(t *testing.T) {
	server, _ := newServer(t)
	organiser := newClient(t, server, 7301, "Organiser")

	var trip struct {
		ID string `json:"id"`
	}
	organiser.do(http.MethodPost, "/api/v1/trips", map[string]any{
		"title": "Brevet", "start_date": "2026-09-23", "end_date": "2026-09-24",
	}, http.StatusCreated, &trip)

	id, err := core.ParseID(trip.ID)
	if err != nil {
		t.Fatalf("the API returned an unparseable id %q: %v", trip.ID, err)
	}
	compact := id.Compact()
	if len(compact) != 22 || compact == trip.ID {
		t.Fatalf("compact id = %q; expected the short form", compact)
	}

	// Both spellings reach the same trip.
	for name, path := range map[string]string{
		"canonical": "/api/v1/trips/" + trip.ID,
		"compact":   "/api/v1/trips/" + compact,
	} {
		t.Run(name, func(t *testing.T) {
			var got struct {
				Trip struct {
					ID    string `json:"id"`
					Title string `json:"title"`
				} `json:"trip"`
			}
			organiser.do(http.MethodGet, path, nil, http.StatusOK, &got)
			if got.Trip.Title != "Brevet" {
				t.Errorf("title = %q", got.Trip.Title)
			}
			// Whichever way it was addressed, the id handed back is canonical.
			if got.Trip.ID != trip.ID {
				t.Errorf("id came back as %q, want the canonical %q", got.Trip.ID, trip.ID)
			}
		})
	}

	// Nested routes too, since the Mini App builds every later call from the
	// id it was launched with.
	organiser.do(http.MethodGet, "/api/v1/trips/"+compact+"/dashboard", nil, http.StatusOK, nil)
	organiser.do(http.MethodGet, "/api/v1/trips/"+compact+"/members", nil, http.StatusOK, nil)

	// Nonsense is still nonsense.
	organiser.do(http.MethodGet, "/api/v1/trips/not-an-id", nil, http.StatusBadRequest, nil)
}

// TestAPIDeleteTrip: only the owner, and it takes everything with it.
func TestAPIDeleteTrip(t *testing.T) {
	server, app := newServer(t)
	owner := newClient(t, server, 7401, "Owner")
	member := newClient(t, server, 7402, "Member")

	var trip struct {
		ID string `json:"id"`
	}
	owner.do(http.MethodPost, "/api/v1/trips", map[string]any{
		"title": "Brevet", "start_date": "2026-09-23", "end_date": "2026-09-24",
	}, http.StatusCreated, &trip)

	var invite struct {
		Token string `json:"token"`
	}
	owner.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/invites", map[string]any{},
		http.StatusCreated, &invite)
	member.do(http.MethodPost, "/api/v1/invites/"+invite.Token+"/join", nil, http.StatusOK, nil)

	// Give the trip something in every corner, so the cascade is exercised.
	var event struct {
		ID string `json:"id"`
	}
	owner.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/events", map[string]any{
		"title": "Departure", "type": "departure", "start_at": "2026-09-23T17:00:00Z",
	}, http.StatusCreated, &event)
	owner.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/decisions", map[string]any{
		"title": "Where to?", "options": []string{"A", "B"},
	}, http.StatusCreated, nil)
	owner.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/expenses", map[string]any{
		"title": "Fuel", "amount_minor": 8000, "category": "fuel",
	}, http.StatusCreated, nil)
	owner.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/checklists", map[string]any{
		"title": "Bike", "scope": "shared", "items": []string{"Pump"},
	}, http.StatusCreated, nil)

	// A member is not an owner.
	member.do(http.MethodDelete, "/api/v1/trips/"+trip.ID, nil, http.StatusForbidden, nil)
	owner.do(http.MethodGet, "/api/v1/trips/"+trip.ID, nil, http.StatusOK, nil)

	owner.do(http.MethodDelete, "/api/v1/trips/"+trip.ID, nil, http.StatusNoContent, nil)

	// Gone for everyone, and gone from the list.
	owner.do(http.MethodGet, "/api/v1/trips/"+trip.ID, nil, http.StatusNotFound, nil)
	member.do(http.MethodGet, "/api/v1/trips/"+trip.ID, nil, http.StatusNotFound, nil)

	var list struct {
		Trips []struct {
			ID string `json:"id"`
		} `json:"trips"`
	}
	owner.do(http.MethodGet, "/api/v1/trips", nil, http.StatusOK, &list)
	for _, got := range list.Trips {
		if got.ID == trip.ID {
			t.Error("the deleted trip is still listed")
		}
	}

	// Nothing of it is left behind. expenses.paid_by references trip_members
	// with no delete action, so this is the assertion that the cascade order
	// actually works rather than happening to.
	for _, table := range []string{
		"trip_members", "trip_invites", "events", "event_participants",
		"decisions", "decision_options", "expenses", "expense_participants",
		"checklists", "checklist_items", "activity_log",
	} {
		var count int64
		if err := app.DB.Table(table).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%s still holds %d rows after the trip was deleted", table, count)
		}
	}
}

// TestAPIExpenseReport checks the report against the balances endpoint.
//
// The two are computed from the same shares, and the point of the test is that
// they stay that way: the report restates the balance next to the paid and
// share figures it came from, so a drift between them would show up as a
// person whose numbers do not reconcile.
func TestAPIExpenseReport(t *testing.T) {
	server, _ := newServer(t)
	alice := newClient(t, server, 7301, "Alice")
	bob := newClient(t, server, 7302, "Bob")

	var trip struct {
		ID string `json:"id"`
	}
	alice.do(http.MethodPost, "/api/v1/trips", map[string]any{
		"title": "Brevet", "start_date": "2026-05-01", "end_date": "2026-05-03",
	}, http.StatusCreated, &trip)

	var invite struct {
		Token string `json:"token"`
	}
	alice.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/invites", map[string]any{}, http.StatusCreated, &invite)
	bob.do(http.MethodPost, "/api/v1/invites/"+invite.Token+"/join", nil, http.StatusOK, nil)

	// Alice fuels the car, Bob books the room: 30.00 and 90.00, split evenly.
	alice.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/expenses", map[string]any{
		"title": "Fuel", "amount_minor": 3000, "category": "fuel",
	}, http.StatusCreated, nil)
	bob.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/expenses", map[string]any{
		"title": "Hotel", "amount_minor": 9000, "category": "accommodation",
	}, http.StatusCreated, nil)

	var report struct {
		Currency   string `json:"currency"`
		Total      int64  `json:"total_minor"`
		Count      int    `json:"count"`
		PerPerson  int64  `json:"per_person_minor"`
		ByCategory []struct {
			Category string `json:"category"`
			Total    int64  `json:"total_minor"`
			Count    int    `json:"count"`
			Percent  int    `json:"percent"`
		} `json:"by_category"`
		Members []struct {
			MemberID    string `json:"member_id"`
			DisplayName string `json:"display_name"`
			Paid        int64  `json:"paid_minor"`
			PaidCount   int    `json:"paid_count"`
			Share       int64  `json:"share_minor"`
			ShareCount  int    `json:"share_count"`
			Balance     int64  `json:"balance_minor"`
		} `json:"members"`
		Expenses []struct {
			Title string `json:"title"`
		} `json:"expenses"`
	}
	bob.do(http.MethodGet, "/api/v1/trips/"+trip.ID+"/expenses/report", nil, http.StatusOK, &report)

	if report.Total != 12000 || report.Count != 2 {
		t.Fatalf("total = %d over %d expenses, want 12000 over 2", report.Total, report.Count)
	}
	if report.PerPerson != 6000 {
		t.Errorf("per person = %d, want 6000", report.PerPerson)
	}
	if report.Currency != "EUR" {
		t.Errorf("currency = %q", report.Currency)
	}
	if len(report.Expenses) != 2 {
		t.Errorf("report carries %d expenses, want 2", len(report.Expenses))
	}

	// Accommodation is the larger of the two, so it leads the breakdown.
	if len(report.ByCategory) != 2 {
		t.Fatalf("categories = %+v", report.ByCategory)
	}
	if report.ByCategory[0].Category != "accommodation" || report.ByCategory[0].Percent != 75 {
		t.Errorf("top category = %+v, want accommodation at 75%%", report.ByCategory[0])
	}

	// Bob paid 90.00 and owes 60.00, so the group owes him 30.00; Alice the
	// mirror of that. The balances endpoint must say the same.
	var balances struct {
		Balances []struct {
			MemberID string `json:"member_id"`
			Paid     int64  `json:"paid_minor"`
			Owed     int64  `json:"owed_minor"`
			Amount   int64  `json:"balance_minor"`
		} `json:"balances"`
	}
	bob.do(http.MethodGet, "/api/v1/trips/"+trip.ID+"/balances", nil, http.StatusOK, &balances)

	if len(report.Members) != len(balances.Balances) {
		t.Fatalf("report lists %d people, balances %d", len(report.Members), len(balances.Balances))
	}
	byID := map[string]struct {
		paid, owed, balance int64
	}{}
	for _, b := range balances.Balances {
		byID[b.MemberID] = struct{ paid, owed, balance int64 }{b.Paid, b.Owed, b.Amount}
	}
	for _, m := range report.Members {
		want, ok := byID[m.MemberID]
		if !ok {
			t.Fatalf("%s is in the report but not in the balances", m.DisplayName)
		}
		if m.Paid != want.paid || m.Share != want.owed || m.Balance != want.balance {
			t.Errorf("%s: report says paid=%d share=%d balance=%d, balances say %d/%d/%d",
				m.DisplayName, m.Paid, m.Share, m.Balance, want.paid, want.owed, want.balance)
		}
		// Whatever anyone paid or owes, the arithmetic has to close.
		if m.Paid-m.Share != m.Balance {
			t.Errorf("%s: %d paid minus %d share is not %d", m.DisplayName, m.Paid, m.Share, m.Balance)
		}
		if m.PaidCount != 1 || m.ShareCount != 2 {
			t.Errorf("%s: paid %d bills and is on %d, want 1 and 2", m.DisplayName, m.PaidCount, m.ShareCount)
		}
	}
	if report.Members[0].Balance < report.Members[1].Balance {
		t.Errorf("members are not ordered creditor-first: %+v", report.Members)
	}
}

// TestAPIEditExpenseAfterSomeoneJoins covers the reason editing exists: a bill
// was split between the people who were on the trip at the time, and then
// somebody else turned up.
func TestAPIEditExpenseAfterSomeoneJoins(t *testing.T) {
	server, _ := newServer(t)
	alice := newClient(t, server, 7311, "Alice")
	bob := newClient(t, server, 7312, "Bob")
	cara := newClient(t, server, 7313, "Cara")

	var trip struct {
		ID string `json:"id"`
	}
	alice.do(http.MethodPost, "/api/v1/trips", map[string]any{
		"title": "Brevet", "start_date": "2026-05-01", "end_date": "2026-05-03",
	}, http.StatusCreated, &trip)

	var invite struct {
		Token string `json:"token"`
	}
	alice.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/invites", map[string]any{}, http.StatusCreated, &invite)
	bob.do(http.MethodPost, "/api/v1/invites/"+invite.Token+"/join", nil, http.StatusOK, nil)

	type expenseBody struct {
		ID           string `json:"id"`
		Amount       int64  `json:"amount_minor"`
		Title        string `json:"title"`
		Participants []struct {
			MemberID string `json:"member_id"`
			Share    int64  `json:"share_minor"`
		} `json:"participants"`
	}
	var expense expenseBody
	alice.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/expenses", map[string]any{
		"title": "Hotel", "amount_minor": 9000, "category": "accommodation",
	}, http.StatusCreated, &expense)
	if len(expense.Participants) != 2 {
		t.Fatalf("split between %d people, want 2", len(expense.Participants))
	}

	// Cara joins after the room was booked.
	cara.do(http.MethodPost, "/api/v1/invites/"+invite.Token+"/join", nil, http.StatusOK, nil)
	var roster struct {
		Members []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"members"`
	}
	alice.do(http.MethodGet, "/api/v1/trips/"+trip.ID+"/members", nil, http.StatusOK, &roster)
	if len(roster.Members) != 3 {
		t.Fatalf("members = %+v", roster.Members)
	}
	all := make([]map[string]any, 0, len(roster.Members))
	for _, m := range roster.Members {
		all = append(all, map[string]any{"member_id": m.ID})
	}

	// Alice re-splits it three ways.
	var updated expenseBody
	alice.do(http.MethodPatch, "/api/v1/trips/"+trip.ID+"/expenses/"+expense.ID, map[string]any{
		"participants": all,
	}, http.StatusOK, &updated)
	if len(updated.Participants) != 3 {
		t.Fatalf("still split between %d people", len(updated.Participants))
	}
	var sum int64
	for _, p := range updated.Participants {
		if p.Share != 3000 {
			t.Errorf("share = %d, want 3000", p.Share)
		}
		sum += p.Share
	}
	if sum != updated.Amount {
		t.Errorf("shares add up to %d but the expense is %d", sum, updated.Amount)
	}

	// Bob did not record it and is not an organiser, so it is not his to edit
	// or to remove.
	bob.do(http.MethodPatch, "/api/v1/trips/"+trip.ID+"/expenses/"+expense.ID, map[string]any{
		"title": "Not Bob's to rename",
	}, http.StatusForbidden, nil)
	bob.do(http.MethodDelete, "/api/v1/trips/"+trip.ID+"/expenses/"+expense.ID, nil, http.StatusForbidden, nil)

	// Alice deletes it, and the ledger empties with it.
	alice.do(http.MethodDelete, "/api/v1/trips/"+trip.ID+"/expenses/"+expense.ID, nil, http.StatusNoContent, nil)
	var report struct {
		Total int64 `json:"total_minor"`
		Count int   `json:"count"`
	}
	alice.do(http.MethodGet, "/api/v1/trips/"+trip.ID+"/expenses/report", nil, http.StatusOK, &report)
	if report.Total != 0 || report.Count != 0 {
		t.Errorf("report after deleting the only expense = %+v", report)
	}
}

// TestAPIExpenseEditPermissions pins who may change a recorded expense: the
// person who recorded it, and the trip owner. Not organisers — admin is the
// "runs the trip" role, and the ledger is where quietly changing somebody
// else's numbers is worth withholding from it.
func TestAPIExpenseEditPermissions(t *testing.T) {
	server, _ := newServer(t)
	alice := newClient(t, server, 7401, "Alice") // owner
	bob := newClient(t, server, 7402, "Bob")     // records the expense
	cara := newClient(t, server, 7403, "Cara")   // promoted to organiser

	var trip struct {
		ID string `json:"id"`
	}
	alice.do(http.MethodPost, "/api/v1/trips", map[string]any{
		"title": "Brevet", "start_date": "2026-05-01", "end_date": "2026-05-03",
	}, http.StatusCreated, &trip)

	var invite struct {
		Token string `json:"token"`
	}
	alice.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/invites", map[string]any{}, http.StatusCreated, &invite)
	bob.do(http.MethodPost, "/api/v1/invites/"+invite.Token+"/join", nil, http.StatusOK, nil)
	cara.do(http.MethodPost, "/api/v1/invites/"+invite.Token+"/join", nil, http.StatusOK, nil)

	var roster struct {
		Members []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
		} `json:"members"`
	}
	alice.do(http.MethodGet, "/api/v1/trips/"+trip.ID+"/members", nil, http.StatusOK, &roster)
	var caraID string
	for _, m := range roster.Members {
		if m.DisplayName == "Cara" {
			caraID = m.ID
		}
	}
	if caraID == "" {
		t.Fatalf("cara is not on the trip: %+v", roster.Members)
	}
	alice.do(http.MethodPatch, "/api/v1/trips/"+trip.ID+"/members/"+caraID,
		map[string]any{"role": "admin"}, http.StatusOK, nil)

	var expense struct {
		ID string `json:"id"`
	}
	bob.do(http.MethodPost, "/api/v1/trips/"+trip.ID+"/expenses", map[string]any{
		"title": "Hotel", "amount_minor": 9000, "category": "accommodation",
	}, http.StatusCreated, &expense)
	path := "/api/v1/trips/" + trip.ID + "/expenses/" + expense.ID

	// Cara runs the trip, but this is Bob's receipt.
	cara.do(http.MethodPatch, path, map[string]any{"title": "Not Cara's to rename"},
		http.StatusForbidden, nil)
	cara.do(http.MethodDelete, path, nil, http.StatusForbidden, nil)

	// Bob recorded it, so he can correct it.
	bob.do(http.MethodPatch, path, map[string]any{"title": "Hotel (2 nights)"}, http.StatusOK, nil)

	// Alice neither recorded nor paid for it, but she owns the trip, so the
	// ledger is not left stuck if Bob goes quiet.
	alice.do(http.MethodPatch, path, map[string]any{"amount_minor": 12000}, http.StatusOK, nil)
	alice.do(http.MethodDelete, path, nil, http.StatusNoContent, nil)
}
