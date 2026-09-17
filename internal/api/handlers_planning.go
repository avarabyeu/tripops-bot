package api

import (
	"net/http"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/httpx"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// ---------------------------------------------------------------- timeline --

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	list, err := s.svc.Events.List(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"events": list})
}

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var in events.CreateInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	event, err := s.svc.Events.Create(r.Context(), access, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, event)
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	eventID, err := httpx.PathID(r, "eventID")
	if err != nil {
		return err
	}
	event, err := s.svc.Events.Get(r.Context(), access, eventID)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, event)
}

func (s *Server) handleUpdateEvent(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	eventID, err := httpx.PathID(r, "eventID")
	if err != nil {
		return err
	}
	var in events.UpdateInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	event, err := s.svc.Events.Update(r.Context(), access, eventID, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, event)
}

func (s *Server) handleDeleteEvent(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	eventID, err := httpx.PathID(r, "eventID")
	if err != nil {
		return err
	}
	if err := s.svc.Events.Delete(r.Context(), access, eventID); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (s *Server) handleRSVP(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	eventID, err := httpx.PathID(r, "eventID")
	if err != nil {
		return err
	}
	var body struct {
		Status   events.RSVP `json:"status"`
		MemberID string      `json:"member_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	memberID, err := memberIDParam(body.MemberID, access.Member.ID)
	if err != nil {
		return err
	}
	event, err := s.svc.Events.SetRSVP(r.Context(), access, eventID, memberID, body.Status)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, event)
}

// --------------------------------------------------------------- decisions --

func (s *Server) handleListDecisions(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	list, err := s.svc.Decisions.List(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"decisions": list})
}

func (s *Server) handleCreateDecision(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var in decisions.CreateInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	decision, err := s.svc.Decisions.Create(r.Context(), access, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, decision)
}

func (s *Server) handleGetDecision(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "decisionID")
	if err != nil {
		return err
	}
	decision, err := s.svc.Decisions.Get(r.Context(), access, id)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, decision)
}

func (s *Server) handleVote(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "decisionID")
	if err != nil {
		return err
	}
	var body struct {
		OptionID core.ID `json:"option_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	decision, err := s.svc.Decisions.Vote(r.Context(), access, id, body.OptionID)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, decision)
}

func (s *Server) handleCloseDecision(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "decisionID")
	if err != nil {
		return err
	}
	decision, err := s.svc.Decisions.Close(r.Context(), access, id)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, decision)
}

func (s *Server) handleResolveDecision(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "decisionID")
	if err != nil {
		return err
	}
	var in decisions.ResolveInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	decision, err := s.svc.Decisions.Resolve(r.Context(), access, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, decision)
}

func (s *Server) handleCancelDecision(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "decisionID")
	if err != nil {
		return err
	}
	decision, err := s.svc.Decisions.Cancel(r.Context(), access, id)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, decision)
}
