// Package checklists is "what do I need to bring".
//
// A list is either shared (one list the group works through together) or
// personal (belongs to one member). Items on a shared list may additionally be
// assigned to somebody, which is what turns "front light" into "Peter's front
// light" without creating a second list.
package checklists

import (
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// Scope distinguishes a group list from a private one.
type Scope string

const (
	ScopeShared   Scope = "shared"
	ScopePersonal Scope = "personal"
)

func (s Scope) Valid() bool { return s == ScopeShared || s == ScopePersonal }

// Item is one line to tick off.
type Item struct {
	ID          core.ID `json:"id"           gorm:"primaryKey"`
	ChecklistID core.ID `json:"checklist_id" gorm:"not null;index:idx_checklist_items,priority:1"`
	Title       string  `json:"title"           gorm:"size:160;not null"`
	Notes       string  `json:"notes,omitempty" gorm:"size:500;not null;default:''"`
	Completed   bool    `json:"completed"       gorm:"not null;default:false"`
	CompletedBy core.ID `json:"completed_by,omitempty"`
	// CompletedByName and AssignedName are joined in for display.
	CompletedByName string     `json:"completed_by_name,omitempty" gorm:"-"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	AssignedTo      core.ID    `json:"assigned_to,omitempty" gorm:"index"`
	AssignedName    string     `json:"assigned_to_name,omitempty" gorm:"-"`
	DueAt           *time.Time `json:"due_at,omitempty"`
	Position        int        `json:"position"   gorm:"not null;default:0;index:idx_checklist_items,priority:2"`
	CreatedAt       time.Time  `json:"created_at" gorm:"not null"`
	UpdatedAt       time.Time  `json:"updated_at" gorm:"not null"`
}

func (Item) TableName() string { return "checklist_items" }

// Mark is the checkbox used in text renderings.
func (i Item) Mark() string {
	if i.Completed {
		return "☑"
	}
	return "☐"
}

// Checklist is a titled group of items.
type Checklist struct {
	ID          core.ID `json:"id"      gorm:"primaryKey"`
	TripID      core.ID `json:"trip_id" gorm:"not null;index:idx_checklists_trip,priority:1"`
	Title       string  `json:"title"                 gorm:"size:120;not null"`
	Description string  `json:"description,omitempty" gorm:"size:1000;not null;default:''"`
	Scope       Scope   `json:"scope" gorm:"size:16;not null;default:'shared'"`
	// OwnerMemberID is set for a personal list and empty for a shared one.
	OwnerMemberID core.ID   `json:"owner_member_id,omitempty"`
	OwnerName     string    `json:"owner_name,omitempty" gorm:"-"`
	Position      int       `json:"position"   gorm:"not null;default:0;index:idx_checklists_trip,priority:2"`
	CreatedBy     core.ID   `json:"created_by" gorm:"not null"`
	CreatedAt     time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt     time.Time `json:"updated_at" gorm:"not null"`

	Items    []Item   `json:"items"    gorm:"-"`
	Progress Progress `json:"progress" gorm:"-"`
}

func (Checklist) TableName() string { return "checklists" }

// Progress is "18 / 23 completed".
type Progress struct {
	Completed int `json:"completed"`
	Total     int `json:"total"`
}

// Percent is the completion ratio, 0-100, and 100 for an empty list: nothing
// to bring is not an unfinished task.
func (p Progress) Percent() int {
	if p.Total == 0 {
		return 100
	}
	return p.Completed * 100 / p.Total
}

// Done reports whether everything is ticked.
func (p Progress) Done() bool { return p.Completed >= p.Total }

// Remaining counts the open items.
func (p Progress) Remaining() int { return p.Total - p.Completed }

func (p Progress) String() string {
	return itoa(p.Completed) + " / " + itoa(p.Total) + " completed"
}

// ProgressOf counts a set of items.
func ProgressOf(items []Item) Progress {
	p := Progress{Total: len(items)}
	for _, i := range items {
		if i.Completed {
			p.Completed++
		}
	}
	return p
}

// SumProgress adds up several lists, which is what the dashboard shows.
func SumProgress(lists []Checklist) Progress {
	var total Progress
	for _, l := range lists {
		total.Completed += l.Progress.Completed
		total.Total += l.Progress.Total
	}
	return total
}

// itoa avoids pulling strconv into a file that needs nothing else from it.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Models lists the tables this module owns, for the migration runner.
func Models() []any { return []any{&Checklist{}, &Item{}} }
