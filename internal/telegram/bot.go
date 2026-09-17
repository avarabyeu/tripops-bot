package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/accommodation"
	"github.com/avarabyeu/tripops-bot/internal/auth"
	"github.com/avarabyeu/tripops-bot/internal/checklists"
	"github.com/avarabyeu/tripops-bot/internal/config"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/dashboard"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/logistics"
	"github.com/avarabyeu/tripops-bot/internal/trips"
	"github.com/avarabyeu/tripops-bot/internal/users"
)

// Deps are the domain services the bot drives. It holds services, never
// repositories: the bot must go through the same rules as the REST API.
type Deps struct {
	Users         *users.Service
	Trips         *trips.Service
	Events        *events.Service
	Decisions     *decisions.Service
	Logistics     *logistics.Service
	Accommodation *accommodation.Service
	Checklists    *checklists.Service
	Expenses      *expenses.Service
	Dashboard     *dashboard.Service
}

type Bot struct {
	cfg    config.Config
	log    *slog.Logger
	api    *Client
	deps   Deps
	drafts *draftStore
}

func NewBot(cfg config.Config, log *slog.Logger, client *Client, deps Deps) *Bot {
	return &Bot{cfg: cfg, log: log, api: client, deps: deps, drafts: newDraftStore()}
}

// Commands are registered with Telegram on start-up so they appear in the menu.
func Commands() []BotCommand {
	return []BotCommand{
		{Command: "start", Description: "Start / open a trip"},
		{Command: "trips", Description: "Your trips"},
		{Command: "newtrip", Description: "Create a trip"},
		{Command: "help", Description: "How TripOps works"},
	}
}

// HandleUpdate is the single entry point for both polling and webhook.
func (b *Bot) HandleUpdate(ctx context.Context, u Update) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("panic handling telegram update", "panic", r, "update_id", u.UpdateID)
		}
	}()
	switch {
	case u.Message != nil:
		b.onMessage(ctx, u.Message)
	case u.CallbackQuery != nil:
		b.onCallback(ctx, u.CallbackQuery)
	}
}

// ---------------------------------------------------------------- messages --

func (b *Bot) onMessage(ctx context.Context, msg *Message) {
	if msg.From == nil || msg.From.IsBot || msg.Chat.Type != "private" {
		return
	}
	user, err := b.identify(ctx, msg.From, msg.Chat.ID)
	if err != nil {
		b.log.Error("identify user", "err", err)
		return
	}
	text := strings.TrimSpace(msg.Text)

	if strings.HasPrefix(text, "/") {
		command, arg, _ := strings.Cut(text, " ")
		command = strings.ToLower(strings.TrimSuffix(command, "@"+b.cfg.BotUsername))
		arg = strings.TrimSpace(arg)
		b.drafts.clear(msg.Chat.ID)

		switch command {
		case "/start":
			b.onStart(ctx, user, msg.Chat.ID, arg)
		case "/help":
			b.send(ctx, msg.Chat.ID, helpView())
		case "/trips":
			b.showTrips(ctx, user, msg.Chat.ID)
		case "/newtrip":
			b.startDraft(ctx, msg.Chat.ID)
		case "/cancel":
			b.reply(ctx, msg.Chat.ID, "Cancelled.")
		default:
			b.reply(ctx, msg.Chat.ID, "I do not know that command. Try /help.")
		}
		return
	}

	// Anything else is either an answer to a draft question or small talk.
	if draft, ok := b.drafts.get(msg.Chat.ID); ok {
		b.continueDraft(ctx, user, msg.Chat.ID, draft, text)
		return
	}
	b.showTrips(ctx, user, msg.Chat.ID)
}

// onStart handles both a bare /start and a deep link payload.
func (b *Bot) onStart(ctx context.Context, user users.User, chatID int64, payload string) {
	if token, ok := strings.CutPrefix(payload, trips.InvitePrefix); ok {
		preview, err := b.deps.Trips.PreviewInvite(ctx, token, user.ID)
		if err != nil {
			b.replyError(ctx, chatID, err)
			return
		}
		b.send(ctx, chatID, inviteView(preview, token))
		return
	}
	if compact, ok := strings.CutPrefix(payload, "trip_"); ok {
		if id, err := core.ParseCompactID(compact); err == nil {
			b.showTrip(ctx, user, chatID, 0, id)
			return
		}
	}
	list, err := b.deps.Trips.List(ctx, user.ID, false)
	if err != nil {
		b.replyError(ctx, chatID, err)
		return
	}
	b.send(ctx, chatID, welcomeView(b.cfg.MiniAppURL, len(list) > 0))
}

// --------------------------------------------------------------- callbacks --

func (b *Bot) onCallback(ctx context.Context, cq *CallbackQuery) {
	user, err := b.identify(ctx, &cq.From, chatIDOf(cq))
	if err != nil {
		b.log.Error("identify user", "err", err)
		return
	}
	chatID := chatIDOf(cq)
	messageID := 0
	if cq.Message != nil {
		messageID = cq.Message.MessageID
	}

	answer := ""
	parts := strings.Split(cq.Data, ":")
	switch parts[0] {
	case "menu":
		b.edit(ctx, chatID, messageID, welcomeView(b.cfg.MiniAppURL, true))

	case "trips":
		b.showTripsEdit(ctx, user, chatID, messageID)

	case "new":
		b.startDraft(ctx, chatID)

	case "t": // t:<trip>[:section]
		tripID, err := core.ParseCompactID(at(parts, 1))
		if err != nil {
			answer = "Unknown trip"
			break
		}
		answer = b.showSection(ctx, user, chatID, messageID, tripID, at(parts, 2))

	case "jn": // jn:<token>
		answer = b.joinTrip(ctx, user, chatID, messageID, at(parts, 1))

	case "inv": // inv:<trip>
		answer = b.createInvite(ctx, user, chatID, messageID, at(parts, 1))

	case "ev": // ev:<event>:<a|n|m>
		answer = b.answerEvent(ctx, user, chatID, messageID, at(parts, 1), at(parts, 2))

	case "dv": // dv:<decision>:<option>
		answer = b.castVote(ctx, user, chatID, messageID, at(parts, 1), at(parts, 2))

	case "vj", "vl": // join / leave a vehicle
		answer = b.changeSeat(ctx, user, chatID, messageID, parts[0], at(parts, 1))

	case "ag": // ag:<accommodation>:c
		answer = b.confirmBed(ctx, user, chatID, messageID, at(parts, 1))

	case "ci": // ci:<item>
		answer = b.completeItem(ctx, user, chatID, messageID, at(parts, 1))

	case "sm": // sm:<from>:<to>:<amount>
		answer = b.markSettled(ctx, user, chatID, messageID, parts)

	default:
		answer = ""
	}
	_ = b.api.AnswerCallbackQuery(ctx, cq.ID, answer, false)
}

// showSection renders one screen of a trip, returning the toast text.
func (b *Bot) showSection(ctx context.Context, user users.User, chatID int64, messageID int, tripID core.ID, section string) string {
	access, err := b.deps.Trips.Access(ctx, tripID, user.ID)
	if err != nil {
		return userMessage(err)
	}
	ctx = auth.WithPrincipal(ctx, auth.Principal{User: user})

	var v view
	switch section {
	case "", "menu":
		data, err := b.deps.Dashboard.Build(ctx, access)
		if err != nil {
			return userMessage(err)
		}
		v = tripMenuView(data, b.cfg.MiniAppURL)
	case "tl":
		list, err := b.deps.Events.List(ctx, access)
		if err != nil {
			return userMessage(err)
		}
		v = timelineView(access.Trip, list, access.Member.ID)
	case "pp":
		members, err := b.deps.Trips.Members(ctx, access)
		if err != nil {
			return userMessage(err)
		}
		inviteURL := ""
		if access.IsManager() {
			if invites, err := b.deps.Trips.Invites(ctx, access); err == nil && len(invites) > 0 {
				inviteURL = invites[0].URL
			}
		}
		v = peopleView(access.Trip, members, inviteURL)
	case "lg":
		list, err := b.deps.Logistics.List(ctx, access)
		if err != nil {
			return userMessage(err)
		}
		v = logisticsView(access.Trip, list, access.Member.ID)
	case "ac":
		list, err := b.deps.Accommodation.List(ctx, access)
		if err != nil {
			return userMessage(err)
		}
		v = accommodationView(access.Trip, list, access.Member.ID)
	case "dc":
		list, err := b.deps.Decisions.List(ctx, access)
		if err != nil {
			return userMessage(err)
		}
		v = decisionsView(access.Trip, list)
	case "cl":
		lists, err := b.deps.Checklists.Lists(ctx, access)
		if err != nil {
			return userMessage(err)
		}
		v = checklistView(access.Trip, lists)
	case "ex":
		list, err := b.deps.Expenses.List(ctx, access)
		if err != nil {
			return userMessage(err)
		}
		report, err := b.deps.Expenses.Balances(ctx, access)
		if err != nil {
			return userMessage(err)
		}
		v = expensesView(access.Trip, list, report)
	case "bal":
		report, err := b.deps.Expenses.Balances(ctx, access)
		if err != nil {
			return userMessage(err)
		}
		v = balancesView(access.Trip, report, access.Member.ID)
	case "st":
		v = settingsView(access.Trip, access.Member, b.cfg.MiniAppURL)
	default:
		return "Unknown screen"
	}

	if messageID > 0 {
		b.edit(ctx, chatID, messageID, v)
	} else {
		b.send(ctx, chatID, v)
	}
	return ""
}

// ------------------------------------------------------------------ actions --

func (b *Bot) joinTrip(ctx context.Context, user users.User, chatID int64, messageID int, token string) string {
	trip, _, err := b.deps.Trips.Join(ctx, user, token)
	if err != nil {
		return userMessage(err)
	}
	b.showSection(ctx, user, chatID, messageID, trip.ID, "")
	return "You are in 🎉"
}

func (b *Bot) createInvite(ctx context.Context, user users.User, chatID int64, messageID int, compact string) string {
	tripID, err := core.ParseCompactID(compact)
	if err != nil {
		return "Unknown trip"
	}
	access, err := b.deps.Trips.Access(ctx, tripID, user.ID)
	if err != nil {
		return userMessage(err)
	}
	if _, err := b.deps.Trips.CreateInvite(ctx, access, trips.InviteInput{Role: core.RoleMember}); err != nil {
		return userMessage(err)
	}
	b.showSection(ctx, user, chatID, messageID, tripID, "pp")
	return "Invite link ready"
}

func (b *Bot) answerEvent(ctx context.Context, user users.User, chatID int64, messageID int, compact, answer string) string {
	eventID, err := core.ParseCompactID(compact)
	if err != nil {
		return "Unknown event"
	}
	status := events.RSVPMaybe
	switch answer {
	case "a":
		status = events.RSVPAttending
	case "n":
		status = events.RSVPNotAttending
	}
	tripID, err := b.tripOfEvent(ctx, user, eventID)
	if err != nil {
		return userMessage(err)
	}
	access, err := b.deps.Trips.Access(ctx, tripID, user.ID)
	if err != nil {
		return userMessage(err)
	}
	if _, err := b.deps.Events.SetRSVP(ctx, access, eventID, access.Member.ID, status); err != nil {
		return userMessage(err)
	}
	b.showSection(ctx, user, chatID, messageID, tripID, "tl")
	return "Answer saved"
}

func (b *Bot) castVote(ctx context.Context, user users.User, chatID int64, messageID int, decisionCompact, optionCompact string) string {
	decisionID, err := core.ParseCompactID(decisionCompact)
	if err != nil {
		return "Unknown decision"
	}
	optionID, err := core.ParseCompactID(optionCompact)
	if err != nil {
		return "Unknown option"
	}
	tripID, err := b.tripOfDecision(ctx, decisionID)
	if err != nil {
		return userMessage(err)
	}
	access, err := b.deps.Trips.Access(ctx, tripID, user.ID)
	if err != nil {
		return userMessage(err)
	}
	if _, err := b.deps.Decisions.Vote(ctx, access, decisionID, optionID); err != nil {
		return userMessage(err)
	}
	b.showSection(ctx, user, chatID, messageID, tripID, "dc")
	return "Vote counted"
}

func (b *Bot) changeSeat(ctx context.Context, user users.User, chatID int64, messageID int, action, compact string) string {
	vehicleID, err := core.ParseCompactID(compact)
	if err != nil {
		return "Unknown vehicle"
	}
	tripID, err := b.tripOfVehicle(ctx, vehicleID)
	if err != nil {
		return userMessage(err)
	}
	access, err := b.deps.Trips.Access(ctx, tripID, user.ID)
	if err != nil {
		return userMessage(err)
	}
	if action == "vj" {
		_, err = b.deps.Logistics.Join(ctx, access, vehicleID)
	} else {
		_, err = b.deps.Logistics.Leave(ctx, access, vehicleID)
	}
	if err != nil {
		return userMessage(err)
	}
	b.showSection(ctx, user, chatID, messageID, tripID, "lg")
	return "Seating updated"
}

func (b *Bot) confirmBed(ctx context.Context, user users.User, chatID int64, messageID int, compact string) string {
	placeID, err := core.ParseCompactID(compact)
	if err != nil {
		return "Unknown place"
	}
	tripID, err := b.tripOfAccommodation(ctx, placeID)
	if err != nil {
		return userMessage(err)
	}
	access, err := b.deps.Trips.Access(ctx, tripID, user.ID)
	if err != nil {
		return userMessage(err)
	}
	if _, err := b.deps.Accommodation.SetGuestStatus(ctx, access, placeID, access.Member.ID,
		accommodation.GuestConfirmed); err != nil {
		return userMessage(err)
	}
	b.showSection(ctx, user, chatID, messageID, tripID, "ac")
	return "Confirmed"
}

func (b *Bot) completeItem(ctx context.Context, user users.User, chatID int64, messageID int, compact string) string {
	itemID, err := core.ParseCompactID(compact)
	if err != nil {
		return "Unknown item"
	}
	tripID, _, _, err := b.deps.Checklists.Repo().ItemTrip(ctx, itemID)
	if err != nil {
		return userMessage(err)
	}
	access, err := b.deps.Trips.Access(ctx, tripID, user.ID)
	if err != nil {
		return userMessage(err)
	}
	done := true
	if _, err := b.deps.Checklists.UpdateItem(ctx, access, itemID,
		checklists.ItemPatch{Completed: &done}); err != nil {
		return userMessage(err)
	}
	b.showSection(ctx, user, chatID, messageID, tripID, "cl")
	return "Ticked off"
}

func (b *Bot) markSettled(ctx context.Context, user users.User, chatID int64, messageID int, parts []string) string {
	if len(parts) < 4 {
		return "Unknown transfer"
	}
	from, err1 := core.ParseCompactID(parts[1])
	to, err2 := core.ParseCompactID(parts[2])
	amount, err3 := strconv.ParseInt(parts[3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || amount <= 0 {
		return "Unknown transfer"
	}
	tripID, err := b.tripOfMember(ctx, from)
	if err != nil {
		return userMessage(err)
	}
	access, err := b.deps.Trips.Access(ctx, tripID, user.ID)
	if err != nil {
		return userMessage(err)
	}
	if _, err := b.deps.Expenses.RecordSettlement(ctx, access, expenses.SettlementInput{
		From: from, To: to, Amount: core.Money(amount),
	}); err != nil {
		return userMessage(err)
	}
	b.showSection(ctx, user, chatID, messageID, tripID, "bal")
	return "Marked as settled"
}

// ------------------------------------------------------------------- trips --

func (b *Bot) showTrips(ctx context.Context, user users.User, chatID int64) {
	list, err := b.deps.Trips.List(ctx, user.ID, false)
	if err != nil {
		b.replyError(ctx, chatID, err)
		return
	}
	b.send(ctx, chatID, tripListView(list))
}

func (b *Bot) showTripsEdit(ctx context.Context, user users.User, chatID int64, messageID int) {
	list, err := b.deps.Trips.List(ctx, user.ID, false)
	if err != nil {
		b.replyError(ctx, chatID, err)
		return
	}
	b.edit(ctx, chatID, messageID, tripListView(list))
}

func (b *Bot) showTrip(ctx context.Context, user users.User, chatID int64, messageID int, tripID core.ID) {
	if msg := b.showSection(ctx, user, chatID, messageID, tripID, ""); msg != "" {
		b.reply(ctx, chatID, msg)
	}
}

// ------------------------------------------------------------------ lookups --

// These small lookups exist because a callback payload carries only the object
// id; the trip it belongs to has to be resolved before access can be checked.

func (b *Bot) tripOfEvent(ctx context.Context, user users.User, eventID core.ID) (core.ID, error) {
	event, err := b.deps.Events.Repo().ByID(ctx, eventID)
	if err != nil {
		return core.Nil, err
	}
	return event.TripID, nil
}

func (b *Bot) tripOfDecision(ctx context.Context, decisionID core.ID) (core.ID, error) {
	decision, err := b.deps.Decisions.Repo().ByID(ctx, decisionID)
	if err != nil {
		return core.Nil, err
	}
	return decision.TripID, nil
}

func (b *Bot) tripOfVehicle(ctx context.Context, vehicleID core.ID) (core.ID, error) {
	vehicle, err := b.deps.Logistics.Repo().ByID(ctx, vehicleID)
	if err != nil {
		return core.Nil, err
	}
	return vehicle.TripID, nil
}

func (b *Bot) tripOfAccommodation(ctx context.Context, placeID core.ID) (core.ID, error) {
	place, err := b.deps.Accommodation.Repo().ByID(ctx, placeID)
	if err != nil {
		return core.Nil, err
	}
	return place.TripID, nil
}

func (b *Bot) tripOfMember(ctx context.Context, memberID core.ID) (core.ID, error) {
	member, err := b.deps.Trips.MemberByAnyTrip(ctx, memberID)
	if err != nil {
		return core.Nil, err
	}
	return member.TripID, nil
}

// ------------------------------------------------------------------ plumbing --

func (b *Bot) identify(ctx context.Context, from *User, chatID int64) (users.User, error) {
	return b.deps.Users.EnsureUser(ctx, users.Identity{
		TelegramID:   from.ID,
		Username:     from.Username,
		FirstName:    from.FirstName,
		LastName:     from.LastName,
		LanguageCode: from.LanguageCode,
		IsPremium:    from.IsPremium,
		ChatID:       chatID,
	})
}

func (b *Bot) send(ctx context.Context, chatID int64, v view) {
	if _, err := b.api.SendMessage(ctx, SendMessageRequest{
		ChatID: chatID, Text: v.text, ReplyMarkup: v.keyboard,
	}); err != nil {
		b.log.Warn("send message failed", "chat_id", chatID, "err", err)
	}
}

func (b *Bot) edit(ctx context.Context, chatID int64, messageID int, v view) {
	if messageID == 0 {
		b.send(ctx, chatID, v)
		return
	}
	if err := b.api.EditMessageText(ctx, EditMessageTextRequest{
		ChatID: chatID, MessageID: messageID, Text: v.text, ReplyMarkup: v.keyboard,
	}); err != nil {
		b.log.Warn("edit message failed", "chat_id", chatID, "err", err)
		b.send(ctx, chatID, v)
	}
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	b.send(ctx, chatID, view{text: EscapeHTML(text)})
}

func (b *Bot) replyError(ctx context.Context, chatID int64, err error) {
	b.log.Warn("bot action failed", "err", err)
	b.reply(ctx, chatID, userMessage(err))
}

// userMessage turns a domain error into something worth reading. Internal
// errors never leak their detail.
func userMessage(err error) string {
	if err == nil {
		return ""
	}
	var domain *core.Error
	if errors.As(err, &domain) && domain.Code != core.CodeInternal {
		return domain.Message
	}
	return "Something went wrong. Please try again."
}

func chatIDOf(cq *CallbackQuery) int64 {
	if cq.Message != nil {
		return cq.Message.Chat.ID
	}
	return cq.From.ID
}

func at(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return ""
}

// ------------------------------------------------------------------ drafts --

// draft is the two-question trip creation flow. Creating a trip is the only
// place the bot needs free text; everything else is buttons.
type draft struct {
	step    string
	title   string
	touched time.Time
}

type draftStore struct {
	mu     sync.Mutex
	drafts map[int64]draft
}

func newDraftStore() *draftStore { return &draftStore{drafts: map[int64]draft{}} }

const draftTTL = 15 * time.Minute

func (s *draftStore) get(chatID int64) (draft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drafts[chatID]
	if !ok || time.Since(d.touched) > draftTTL {
		delete(s.drafts, chatID)
		return draft{}, false
	}
	return d, true
}

func (s *draftStore) set(chatID int64, d draft) {
	d.touched = time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drafts[chatID] = d
}

func (s *draftStore) clear(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.drafts, chatID)
}

func (b *Bot) startDraft(ctx context.Context, chatID int64) {
	b.drafts.set(chatID, draft{step: "title"})
	b.send(ctx, chatID, view{
		text: bold("New trip") + "\n\nWhat is it called?\n\n" + italic("For example: Brevet Łódź 200"),
	})
}

func (b *Bot) continueDraft(ctx context.Context, user users.User, chatID int64, d draft, text string) {
	switch d.step {
	case "title":
		if len([]rune(text)) < 2 {
			b.reply(ctx, chatID, "That is a bit short. What is the trip called?")
			return
		}
		b.drafts.set(chatID, draft{step: "dates", title: text})
		b.send(ctx, chatID, view{
			text: bold("When?") + "\n\nSend the dates, for example:\n" +
				"<code>23.09</code> — a single day\n" +
				"<code>23.09 - 24.09</code> — a range\n" +
				"<code>2026-09-23 2026-09-24</code>",
		})

	case "dates":
		start, end, err := parseDateRange(text, time.Now().UTC())
		if err != nil {
			b.reply(ctx, chatID, "I could not read those dates. Try 23.09 or 23.09 - 24.09.")
			return
		}
		trip, err := b.deps.Trips.Create(ctx, user, trips.CreateInput{
			Title:     d.title,
			StartDate: start,
			EndDate:   end,
			Timezone:  b.cfg.DefaultTimezone,
			Currency:  b.cfg.DefaultCurrency,
		})
		if err != nil {
			b.replyError(ctx, chatID, err)
			return
		}
		b.drafts.clear(chatID)

		access, err := b.deps.Trips.Access(ctx, trip.ID, user.ID)
		if err != nil {
			b.replyError(ctx, chatID, err)
			return
		}
		invite, err := b.deps.Trips.CreateInvite(ctx, access, trips.InviteInput{Role: core.RoleMember})
		if err != nil {
			b.log.Warn("create invite after trip", "err", err)
		}

		lines := []string{
			fmt.Sprintf("%s %s", "🎉", bold(esc(trip.Title))),
			dateRange(trip),
			"",
			"Your trip is ready. Share this link with the group:",
		}
		if invite.URL != "" {
			lines = append(lines, esc(invite.URL))
		}
		kb := rows(row(button("Open trip", "t:"+trip.ID.Compact())))
		if invite.URL != "" {
			kb.InlineKeyboard = append([][]InlineKeyboardButton{
				row(urlButton("📤 Share invite", "https://t.me/share/url?url="+invite.URL)),
			}, kb.InlineKeyboard...)
		}
		b.send(ctx, chatID, view{text: strings.Join(lines, "\n"), keyboard: kb})
	}
}
