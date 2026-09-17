package checklists

import (
	"testing"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

func items(completed ...bool) []Item {
	out := make([]Item, len(completed))
	for i, done := range completed {
		out[i] = Item{ID: core.NewID(), Title: "item", Completed: done}
	}
	return out
}

func TestProgressOf(t *testing.T) {
	p := ProgressOf(items(true, true, false, true, false))
	if p.Completed != 3 || p.Total != 5 {
		t.Fatalf("progress = %d/%d, want 3/5", p.Completed, p.Total)
	}
	if p.Remaining() != 2 {
		t.Errorf("Remaining = %d, want 2", p.Remaining())
	}
	if p.Percent() != 60 {
		t.Errorf("Percent = %d, want 60", p.Percent())
	}
	if p.Done() {
		t.Error("a list with open items is not done")
	}
	if got := p.String(); got != "3 / 5 completed" {
		t.Errorf("String = %q", got)
	}
}

// An empty list is complete, not stuck at zero: there is nothing left to bring.
func TestEmptyProgressIsComplete(t *testing.T) {
	p := ProgressOf(nil)
	if p.Percent() != 100 || !p.Done() {
		t.Errorf("empty progress = %d%%, done=%v", p.Percent(), p.Done())
	}
}

func TestSumProgressAcrossLists(t *testing.T) {
	lists := []Checklist{
		{Progress: ProgressOf(items(true, true, true, false, false, false))},
		{Progress: ProgressOf(items(true, true))},
	}
	total := SumProgress(lists)
	if total.Completed != 5 || total.Total != 8 {
		t.Fatalf("total = %d/%d, want 5/8", total.Completed, total.Total)
	}
	if got := total.String(); got != "5 / 8 completed" {
		t.Errorf("String = %q", got)
	}
}

func TestItemMark(t *testing.T) {
	if (Item{Completed: true}).Mark() != "☑" || (Item{}).Mark() != "☐" {
		t.Error("checkbox marks are wrong")
	}
}

func TestScopeValidation(t *testing.T) {
	if !ScopeShared.Valid() || !ScopePersonal.Valid() {
		t.Error("known scopes must validate")
	}
	if Scope("secret").Valid() {
		t.Error("unknown scopes must not validate")
	}
}
