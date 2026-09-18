package api

import (
	"net/http"
	"strings"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/httpx"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// meResponse is what the Mini App loads on start-up.
type meResponse struct {
	ID         core.ID `json:"id"`
	TelegramID int64   `json:"telegram_id"`
	Name       string  `json:"name"`
	Username   string  `json:"username,omitempty"`
	PhotoURL   string  `json:"photo_url,omitempty"`
	// StartParam carries a deep link payload through the Mini App launch, so
	// opening an invite link inside Telegram lands on the right screen.
	StartParam string `json:"start_param,omitempty"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, meResponse{
		ID:         p.User.ID,
		TelegramID: p.User.TelegramID,
		Name:       p.User.DisplayName(),
		Username:   p.User.Username,
		PhotoURL:   p.User.PhotoURL,
		StartParam: p.StartParam,
	})
}

func (s *Server) handleGetPreferences(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	prefs, err := s.svc.Notify.Preferences(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, prefs)
}

func (s *Server) handleUpdatePreferences(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	current, err := s.svc.Notify.Preferences(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	var body struct {
		TripUpdates *bool `json:"trip_updates"`
		Decisions   *bool `json:"decisions"`
		Reminders   *bool `json:"reminders"`
		Checklist   *bool `json:"checklist"`
		Expenses    *bool `json:"expenses"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.TripUpdates != nil {
		current.TripUpdates = *body.TripUpdates
	}
	if body.Decisions != nil {
		current.Decisions = *body.Decisions
	}
	if body.Reminders != nil {
		current.Reminders = *body.Reminders
	}
	if body.Checklist != nil {
		current.Checklist = *body.Checklist
	}
	if body.Expenses != nil {
		current.Expenses = *body.Expenses
	}
	saved, err := s.svc.Notify.SavePreferences(r.Context(), p.User.ID, current)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, saved)
}

func (s *Server) handleListTrips(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	list, err := s.svc.Trips.List(r.Context(), p.User.ID, httpx.QueryBool(r, "include_archived", false))
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"trips": list})
}

func (s *Server) handleCreateTrip(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	var in trips.CreateInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	trip, err := s.svc.Trips.Create(r.Context(), p.User, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, trip)
}

func (s *Server) handleGetTrip(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"trip": access.Trip,
		"me":   access.Member,
	})
}

func (s *Server) handleUpdateTrip(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var in trips.UpdateInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	trip, err := s.svc.Trips.Update(r.Context(), access, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, trip)
}

func (s *Server) handleDeleteTrip(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	if err := s.svc.Trips.Delete(r.Context(), access); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	view, err := s.svc.Dashboard.Build(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, view)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	entries, err := s.svc.Activity.List(r.Context(), access.Trip.ID, httpx.QueryInt(r, "limit", 50, 1, 200))
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"activity": entries})
}

// ----------------------------------------------------------------- members --

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	members, err := s.svc.Trips.Members(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"members": members})
}

func (s *Server) handleUpdateMember(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	memberID, err := httpx.PathID(r, "memberID")
	if err != nil {
		return err
	}
	var in trips.UpdateMemberInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	member, err := s.svc.Trips.UpdateMember(r.Context(), access, memberID, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, member)
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	memberID, err := httpx.PathID(r, "memberID")
	if err != nil {
		return err
	}
	if err := s.svc.Trips.RemoveMember(r.Context(), access, memberID); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (s *Server) handleTransferOwnership(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var body struct {
		MemberID core.ID `json:"member_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if err := s.svc.Trips.TransferOwnership(r.Context(), access, body.MemberID); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// ----------------------------------------------------------------- invites --

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	list, err := s.svc.Trips.Invites(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"invites": list})
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var in trips.InviteInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	invite, err := s.svc.Trips.CreateInvite(r.Context(), access, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, invite)
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	inviteID, err := httpx.PathID(r, "inviteID")
	if err != nil {
		return err
	}
	if err := s.svc.Trips.RevokeInvite(r.Context(), access, inviteID); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (s *Server) handlePreviewInvite(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(httpx.PathParam(r, "token"))
	preview, err := s.svc.Trips.PreviewInvite(r.Context(), token, p.User.ID)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, preview)
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(httpx.PathParam(r, "token"))
	trip, member, err := s.svc.Trips.Join(r.Context(), p.User, token)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"trip": trip, "member": member})
}
