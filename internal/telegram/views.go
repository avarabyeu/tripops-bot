package telegram

import (
	"fmt"
	"strings"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/accommodation"
	"github.com/avarabyeu/tripops-bot/internal/checklists"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/dashboard"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/logistics"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// view is a rendered screen: text plus the buttons under it.
type view struct {
	text     string
	keyboard *InlineKeyboardMarkup
}

func rows(rows ...[]InlineKeyboardButton) *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{InlineKeyboard: rows}
}

func row(buttons ...InlineKeyboardButton) []InlineKeyboardButton { return buttons }

func button(text, data string) InlineKeyboardButton {
	return InlineKeyboardButton{Text: text, CallbackData: data}
}

func webAppButton(text, url string) InlineKeyboardButton {
	return InlineKeyboardButton{Text: text, WebApp: &WebAppInfo{URL: url}}
}

func urlButton(text, url string) InlineKeyboardButton {
	return InlineKeyboardButton{Text: text, URL: url}
}

// bold and italic wrap already-escaped text.
func bold(s string) string   { return "<b>" + s + "</b>" }
func italic(s string) string { return "<i>" + s + "</i>" }

// esc is the shorthand used by every renderer for user supplied text.
func esc(s string) string { return EscapeHTML(s) }

// ------------------------------------------------------------ entry points --

func welcomeView(miniAppURL string, hasTrips bool) view {
	text := strings.Join([]string{
		bold("TripOps") + " — the shared state of your trip, inside Telegram.",
		"",
		"Keep people, timeline, decisions, logistics, checklists and expenses in one place.",
		"",
		italic("Start by creating a trip, then share the invite link with your group."),
	}, "\n")

	first := row(button("➕ Create trip", "new"))
	if hasTrips {
		first = row(button("➕ Create trip", "new"), button("🧳 My trips", "trips"))
	}
	kb := rows(first)
	if miniAppURL != "" {
		kb.InlineKeyboard = append(kb.InlineKeyboard, row(webAppButton("📱 Open app", miniAppURL)))
	}
	return view{text: text, keyboard: kb}
}

func helpView() view {
	text := strings.Join([]string{
		bold("TripOps commands"),
		"",
		"/trips — your trips",
		"/newtrip — create a trip",
		"/help — this message",
		"",
		bold("How it works"),
		"A trip holds everyone and everything: the timeline, who is coming to what, group decisions, cars and beds, shared checklists and the money.",
		"",
		"Tap a trip to see its sections. Everything you can do here you can also do in the Mini App, which is nicer for editing.",
	}, "\n")
	return view{text: text, keyboard: rows(row(button("🧳 My trips", "trips")))}
}

func tripListView(list []trips.TripSummary) view {
	if len(list) == 0 {
		return view{
			text: bold("No trips yet") + "\n\nA trip is the container for everything: people, plans, money.\n\n" +
				italic("Create one and invite your group."),
			keyboard: rows(row(button("➕ Create trip", "new"))),
		}
	}
	lines := []string{bold("Your trips"), ""}
	kb := rows()
	for _, t := range list {
		lines = append(lines, fmt.Sprintf("%s %s · %s", statusIcon(t.Status), esc(t.Title), dateRange(t.Trip)))
		label := fmt.Sprintf("%s %s", statusIcon(t.Status), t.Title)
		kb.InlineKeyboard = append(kb.InlineKeyboard, row(button(truncate(label, 60), "t:"+t.ID.Compact())))
	}
	kb.InlineKeyboard = append(kb.InlineKeyboard, row(button("➕ Create trip", "new")))
	return view{text: strings.Join(lines, "\n"), keyboard: kb}
}

// tripMenuView is the hub the spec describes: one tap to every section.
func tripMenuView(v dashboard.View, miniAppURL string) view {
	trip := v.Trip
	cid := trip.ID.Compact()

	lines := []string{
		bold(strings.ToUpper(esc(trip.Title))),
		dateRange(trip),
		"",
		fmt.Sprintf("👥 %d of %d confirmed", v.People.Active, v.People.Total),
	}
	if v.Transport.Vehicles > 0 {
		lines = append(lines, fmt.Sprintf("🚗 %d vehicle(s), %d of %d seats taken",
			v.Transport.Vehicles, v.Transport.SeatsUsed, v.Transport.Seats))
	}
	if v.Accommodation.Places > 0 {
		lines = append(lines, fmt.Sprintf("🏠 %d / %d confirmed", v.Accommodation.Confirmed, v.Accommodation.Total))
	}
	if v.NextEvent != nil {
		lines = append(lines, "", bold("📅 Next"),
			fmt.Sprintf("%s %s · %s", v.NextEvent.Icon(), esc(v.NextEvent.Title),
				v.NextEvent.StartAt.In(trip.Location()).Format("2 Jan · 15:04")))
	}
	if v.Expenses.Count > 0 {
		line := fmt.Sprintf("💰 %s total", v.Expenses.Total.Format(v.Expenses.Currency))
		if v.Expenses.MyBalance != 0 {
			line += fmt.Sprintf(" · you %s", signedMoney(v.Expenses.MyBalance, v.Expenses.Currency))
		}
		lines = append(lines, "", line)
	}
	if v.Decisions.Open > 0 {
		lines = append(lines, fmt.Sprintf("🗳 %d decision(s) pending", v.Decisions.Open))
	}
	if v.Checklist.Total > 0 {
		lines = append(lines, fmt.Sprintf("🎒 %s", v.Checklist.String()))
	}
	if len(v.Attention) > 0 {
		lines = append(lines, "", bold("⚠️ Attention"))
		for i, item := range v.Attention {
			if i == 3 {
				lines = append(lines, italic(fmt.Sprintf("…and %d more", len(v.Attention)-3)))
				break
			}
			lines = append(lines, "• "+esc(item.Title))
		}
	}

	kb := rows(
		row(button("📅 Timeline", "t:"+cid+":tl"), button("👥 People", "t:"+cid+":pp")),
		row(button("🚗 Logistics", "t:"+cid+":lg"), button("🏠 Stay", "t:"+cid+":ac")),
		row(button("💰 Expenses", "t:"+cid+":ex"), button("🗳 Decisions", "t:"+cid+":dc")),
		row(button("🎒 Checklist", "t:"+cid+":cl"), button("⚙️ Settings", "t:"+cid+":st")),
	)
	if miniAppURL != "" {
		kb.InlineKeyboard = append(kb.InlineKeyboard,
			row(webAppButton("📱 Open in app", miniAppURL+"?startapp=trip_"+cid)))
	}
	kb.InlineKeyboard = append(kb.InlineKeyboard, row(button("‹ My trips", "trips")))
	return view{text: strings.Join(lines, "\n"), keyboard: kb}
}

// ---------------------------------------------------------------- sections --

func timelineView(trip trips.Trip, list []events.Event, me core.ID) view {
	cid := trip.ID.Compact()
	if len(list) == 0 {
		return section(cid, bold("📅 TIMELINE")+"\n\n"+
			"Nothing scheduled yet.\n\n"+
			italic("A timeline answers the only question people ask in the group chat: when are we leaving?"), nil)
	}

	loc := trip.Location()
	lines := []string{bold("📅 TIMELINE"), ""}
	var currentDay string
	var buttons [][]InlineKeyboardButton
	now := time.Now()

	for _, e := range list {
		day := e.StartAt.In(loc).Format("2 Jan")
		if day != currentDay {
			if currentDay != "" {
				lines = append(lines, "")
			}
			lines = append(lines, bold(strings.ToUpper(day)))
			currentDay = day
		}
		line := fmt.Sprintf("%s  %s %s", e.StartAt.In(loc).Format("15:04"), e.Icon(), esc(e.Title))
		if e.LocationName != "" {
			line += " · " + italic(esc(e.LocationName))
		}
		if e.Undecided > 0 {
			line += fmt.Sprintf(" (%d undecided)", e.Undecided)
		}
		lines = append(lines, line)

		// Only offer an answer for events that have not happened yet.
		if e.StartAt.After(now) && e.RSVPOf(me) == events.RSVPUndecided {
			buttons = append(buttons, row(
				button("☑ "+truncate(e.Title, 18), "ev:"+e.ID.Compact()+":a"),
				button("☐", "ev:"+e.ID.Compact()+":n"),
				button("?", "ev:"+e.ID.Compact()+":m"),
			))
		}
	}
	if len(buttons) > 0 {
		lines = append(lines, "", italic("Tap to answer:"))
	}
	return section(cid, strings.Join(lines, "\n"), buttons)
}

func peopleView(trip trips.Trip, members []trips.Member, inviteURL string) view {
	cid := trip.ID.Compact()
	lines := []string{bold("👥 PEOPLE"), ""}
	active := 0
	for _, m := range members {
		if m.Active() {
			active++
		}
	}
	lines = append(lines, fmt.Sprintf("%d of %d confirmed", active, len(members)), "")
	for _, m := range members {
		mark := "☑"
		if !m.Active() {
			mark = "⏳"
		}
		line := fmt.Sprintf("%s %s", mark, esc(m.DisplayName))
		if m.Role != core.RoleMember {
			line += " " + italic("("+string(m.Role)+")")
		}
		lines = append(lines, line)
	}
	var buttons [][]InlineKeyboardButton
	if inviteURL != "" {
		lines = append(lines, "", bold("Invite link"), esc(inviteURL))
		buttons = append(buttons, row(urlButton("📤 Share invite",
			"https://t.me/share/url?url="+inviteURL)))
	} else {
		buttons = append(buttons, row(button("🔗 Create invite link", "inv:"+cid)))
	}
	return section(cid, strings.Join(lines, "\n"), buttons)
}

func logisticsView(trip trips.Trip, vehicles []logistics.Vehicle, me core.ID) view {
	cid := trip.ID.Compact()
	if len(vehicles) == 0 {
		return section(cid, bold("🚗 LOGISTICS")+"\n\n"+
			"No vehicles yet.\n\n"+
			italic("Add the cars and who drives them so nobody is left without a seat."), nil)
	}
	lines := []string{bold("🚗 LOGISTICS"), ""}
	var buttons [][]InlineKeyboardButton
	for _, v := range vehicles {
		lines = append(lines, bold(v.Type.Icon()+" "+esc(v.Name)))
		if v.DriverName != "" {
			lines = append(lines, "Driver: "+esc(v.DriverName))
		}
		lines = append(lines, fmt.Sprintf("Seats: %d / %d", v.SeatsUsed, v.Capacity))
		for _, p := range v.Passengers {
			if p.MemberID == v.DriverMemberID {
				continue
			}
			lines = append(lines, "· "+esc(p.DisplayName))
		}
		lines = append(lines, "")

		riding := v.DriverMemberID == me
		for _, p := range v.Passengers {
			if p.MemberID == me {
				riding = true
			}
		}
		switch {
		case riding:
			buttons = append(buttons, row(button("✖ Leave "+truncate(v.Name, 20), "vl:"+v.ID.Compact())))
		case v.SeatsLeft > 0:
			buttons = append(buttons, row(button("➕ Join "+truncate(v.Name, 20), "vj:"+v.ID.Compact())))
		}
	}
	return section(cid, strings.TrimRight(strings.Join(lines, "\n"), "\n"), buttons)
}

func accommodationView(trip trips.Trip, places []accommodation.Accommodation, me core.ID) view {
	cid := trip.ID.Compact()
	if len(places) == 0 {
		return section(cid, bold("🏠 ACCOMMODATION")+"\n\n"+
			"Nothing booked yet.\n\n"+
			italic("Add where you are sleeping and who is in which place."), nil)
	}
	loc := trip.Location()
	lines := []string{bold("🏠 ACCOMMODATION"), ""}
	var buttons [][]InlineKeyboardButton
	for _, p := range places {
		lines = append(lines, bold(esc(p.Name)))
		if p.CheckIn != nil && p.CheckOut != nil {
			lines = append(lines, fmt.Sprintf("%s → %s",
				p.CheckIn.In(loc).Format("2 Jan 15:04"), p.CheckOut.In(loc).Format("2 Jan 15:04")))
		}
		if p.Address != "" {
			lines = append(lines, italic(esc(p.Address)))
		}
		if p.Capacity > 0 {
			lines = append(lines, fmt.Sprintf("Capacity: %d · %d confirmed", p.Capacity, p.Confirmed))
		}
		for _, g := range p.Guests {
			lines = append(lines, g.Status.Mark()+" "+esc(g.DisplayName))
			if g.MemberID == me && g.Status != accommodation.GuestConfirmed {
				buttons = append(buttons, row(button("☑ Confirm "+truncate(p.Name, 18), "ag:"+p.ID.Compact()+":c")))
			}
		}
		if p.URL != "" {
			buttons = append(buttons, row(urlButton("🔗 "+truncate(p.Name, 24), p.URL)))
		}
		lines = append(lines, "")
	}
	return section(cid, strings.TrimRight(strings.Join(lines, "\n"), "\n"), buttons)
}

func decisionsView(trip trips.Trip, list []decisions.Decision) view {
	cid := trip.ID.Compact()
	open := make([]decisions.Decision, 0, len(list))
	for _, d := range list {
		if d.Status == decisions.StatusOpen {
			open = append(open, d)
		}
	}
	if len(list) == 0 {
		return section(cid, bold("🗳 DECISIONS")+"\n\n"+
			"No decisions yet.\n\n"+
			"Use decisions to let the group choose:\n• departure time\n• accommodation\n• route\n• restaurant", nil)
	}

	loc := trip.Location()
	lines := []string{bold("🗳 DECISIONS"), ""}
	var buttons [][]InlineKeyboardButton
	for _, d := range list {
		switch d.Status {
		case decisions.StatusResolved:
			chosen, _ := d.ResolvedOption()
			lines = append(lines, "✅ "+bold(esc(d.Title)), "Decided: "+esc(chosen.Label), "")
			continue
		case decisions.StatusCancelled:
			continue
		}
		lines = append(lines, bold(esc(d.Title)))
		for _, o := range d.Options {
			mark := "○"
			if d.MyVote == o.ID {
				mark = "●"
			}
			lines = append(lines, fmt.Sprintf("%s %s — %d", mark, esc(o.Label), o.Votes))
		}
		if d.Deadline != nil {
			lines = append(lines, italic("Closes "+d.Deadline.In(loc).Format("2 Jan 15:04")))
		}
		lines = append(lines, fmt.Sprintf("%d of %d voted", d.TotalVotes, d.Eligible), "")

		if d.Status == decisions.StatusOpen && d.MyVote.IsZero() {
			for _, o := range d.Options {
				buttons = append(buttons, row(button("🗳 "+truncate(o.Label, 28),
					"dv:"+d.ID.Compact()+":"+o.ID.Compact())))
			}
		}
	}
	_ = open
	return section(cid, strings.TrimRight(strings.Join(lines, "\n"), "\n"), buttons)
}

func checklistView(trip trips.Trip, lists []checklists.Checklist) view {
	cid := trip.ID.Compact()
	if len(lists) == 0 {
		return section(cid, bold("🎒 CHECKLIST")+"\n\n"+
			"No checklists yet.\n\n"+
			italic("A shared list means nobody arrives without a rear light."), nil)
	}
	lines := []string{bold("🎒 CHECKLIST"), ""}
	var buttons [][]InlineKeyboardButton
	for _, l := range lists {
		lines = append(lines, bold(esc(l.Title))+" · "+l.Progress.String())
		for _, item := range l.Items {
			line := item.Mark() + " " + esc(item.Title)
			if item.AssignedName != "" {
				line += " " + italic("("+esc(item.AssignedName)+")")
			}
			lines = append(lines, line)
			if !item.Completed && len(buttons) < 8 {
				buttons = append(buttons, row(button("☑ "+truncate(item.Title, 28), "ci:"+item.ID.Compact())))
			}
		}
		lines = append(lines, "")
	}
	total := checklists.SumProgress(lists)
	lines = append(lines, bold(total.String()))
	return section(cid, strings.Join(lines, "\n"), buttons)
}

func expensesView(trip trips.Trip, list []expenses.Expense, report expenses.BalanceReport) view {
	cid := trip.ID.Compact()
	if len(list) == 0 {
		return section(cid, bold("💰 EXPENSES")+"\n\n"+
			"Nothing recorded yet.\n\n"+
			italic("Add what people paid for and TripOps works out who owes whom."), nil)
	}
	lines := []string{bold("💰 EXPENSES"), ""}
	for i, e := range list {
		if i == 8 {
			lines = append(lines, italic(fmt.Sprintf("…and %d more", len(list)-8)))
			break
		}
		lines = append(lines, fmt.Sprintf("%s %s — %s %s", e.Category.Icon(), esc(e.Title),
			e.Formatted(), italic("by "+esc(e.PaidByName))))
	}
	lines = append(lines, "", bold("Total: "+report.Total.Format(report.Currency)))
	return section(cid, strings.Join(lines, "\n"),
		[][]InlineKeyboardButton{row(button("⚖️ Balances", "t:"+cid+":bal"))})
}

func balancesView(trip trips.Trip, report expenses.BalanceReport, me core.ID) view {
	cid := trip.ID.Compact()
	lines := []string{bold("⚖️ BALANCE"), ""}
	any := false
	for _, b := range report.Balances {
		if b.Amount == 0 {
			continue
		}
		any = true
		lines = append(lines, fmt.Sprintf("%-14s %s", esc(b.DisplayName),
			signedMoney(b.Amount, report.Currency)))
	}
	if !any {
		lines = append(lines, "Everything is settled. 🎉")
	}

	var buttons [][]InlineKeyboardButton
	if len(report.Transfers) > 0 {
		lines = append(lines, "", bold("Suggested transfers"))
		for _, t := range report.Transfers {
			lines = append(lines, fmt.Sprintf("%s owes %s %s",
				esc(t.FromName), esc(t.ToName), t.Amount.Format(t.Currency)))
			if t.From == me || t.To == me {
				buttons = append(buttons, row(button(
					fmt.Sprintf("✅ %s → %s %s", truncate(t.FromName, 10), truncate(t.ToName, 10),
						t.Amount.Format(t.Currency)),
					"sm:"+t.From.Compact()+":"+t.To.Compact()+":"+itoa(int64(t.Amount)))))
			}
		}
		lines = append(lines, "", italic("TripOps does not move money. Mark a transfer once it has happened."))
	}
	return section(cid, strings.Join(lines, "\n"), buttons)
}

func settingsView(trip trips.Trip, member trips.Member, miniAppURL string) view {
	cid := trip.ID.Compact()
	lines := []string{
		bold("⚙️ SETTINGS"), "",
		bold(esc(trip.Title)),
		dateRange(trip),
		"Timezone: " + esc(trip.Timezone),
		"Currency: " + esc(trip.Currency),
		"Status: " + string(trip.Status),
		"Your role: " + string(member.Role),
		"",
		italic("Editing the trip, people and content happens in the Mini App."),
	}
	var buttons [][]InlineKeyboardButton
	if miniAppURL != "" {
		buttons = append(buttons, row(webAppButton("📱 Open in app", miniAppURL+"?startapp=trip_"+cid)))
	}
	return section(cid, strings.Join(lines, "\n"), buttons)
}

func inviteView(preview trips.InvitePreview, token string) view {
	lines := []string{
		bold("You are invited to"),
		bold(strings.ToUpper(esc(preview.Trip.Title))),
		dateRange(preview.Trip),
		"",
		fmt.Sprintf("👥 %d people going", preview.MemberCount),
	}
	if preview.OwnerName != "" {
		lines = append(lines, "Organised by "+esc(preview.OwnerName))
	}
	if preview.Trip.Description != "" {
		lines = append(lines, "", esc(preview.Trip.Description))
	}
	if preview.AlreadyJoined {
		lines = append(lines, "", italic("You are already on this trip."))
		return view{
			text:     strings.Join(lines, "\n"),
			keyboard: rows(row(button("Open trip", "t:"+preview.Trip.ID.Compact()))),
		}
	}
	return view{
		text:     strings.Join(lines, "\n"),
		keyboard: rows(row(button("✅ Join trip", "jn:"+token), button("‹ Not now", "menu"))),
	}
}

// ----------------------------------------------------------------- helpers --

// section wraps a screen with the standard "back to trip" navigation.
func section(tripCompactID, text string, extra [][]InlineKeyboardButton) view {
	kb := rows()
	kb.InlineKeyboard = append(kb.InlineKeyboard, extra...)
	kb.InlineKeyboard = append(kb.InlineKeyboard, row(button("‹ Back", "t:"+tripCompactID)))
	return view{text: text, keyboard: kb}
}

func statusIcon(s core.TripStatus) string {
	switch s {
	case core.TripActive:
		return "🟢"
	case core.TripCompleted:
		return "✅"
	case core.TripArchived:
		return "📦"
	default:
		return "🗓"
	}
}

// dateRange renders "23–24 September 2026", collapsing a single day.
func dateRange(t trips.Trip) string {
	start := t.StartDate.In(time.UTC)
	end := t.EndDate.In(time.UTC)
	switch {
	case t.StartDate == t.EndDate:
		return start.Format("2 January 2006")
	case start.Month() == end.Month() && start.Year() == end.Year():
		return fmt.Sprintf("%d–%s", start.Day(), end.Format("2 January 2006"))
	default:
		return fmt.Sprintf("%s – %s", start.Format("2 Jan"), end.Format("2 Jan 2006"))
	}
}

// signedMoney renders a balance with an explicit sign, which is the whole
// point of a balance line.
func signedMoney(m core.Money, currency string) string {
	if m > 0 {
		return "+" + m.Format(currency)
	}
	return m.Format(currency)
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func itoa(n int64) string { return fmt.Sprintf("%d", n) }
