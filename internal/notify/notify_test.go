package notify

import "testing"

// The defaults are a product decision: anything that needs a reply from the
// user is on, purely informational money updates are off.
func TestDefaultPreferences(t *testing.T) {
	p := DefaultPreferences()
	if !p.TripUpdates || !p.Decisions || !p.Reminders || !p.Checklist {
		t.Error("actionable categories should default to on")
	}
	if p.Expenses {
		t.Error("expense notifications should default to off")
	}
}

func TestPreferencesAllows(t *testing.T) {
	p := Preferences{TripUpdates: true, Decisions: false, Reminders: true, Checklist: false, Expenses: true}
	cases := map[Category]bool{
		CategoryTripUpdates: true,
		CategoryDecisions:   false,
		CategoryReminders:   true,
		CategoryChecklist:   false,
		CategoryExpenses:    true,
		Category("gossip"):  false,
	}
	for category, want := range cases {
		if got := p.Allows(category); got != want {
			t.Errorf("Allows(%s) = %v, want %v", category, got, want)
		}
	}
}

func TestCategoryValidation(t *testing.T) {
	for _, c := range []Category{
		CategoryTripUpdates, CategoryDecisions, CategoryReminders,
		CategoryChecklist, CategoryExpenses,
	} {
		if !c.Valid() {
			t.Errorf("%s should be valid", c)
		}
	}
	if Category("marketing").Valid() {
		t.Error("unknown categories must not validate")
	}
}
