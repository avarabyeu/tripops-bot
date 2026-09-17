package api

import (
	"net/http"

	"github.com/avarabyeu/tripops-bot/internal/accommodation"
	"github.com/avarabyeu/tripops-bot/internal/checklists"
	"github.com/avarabyeu/tripops-bot/internal/httpx"
	"github.com/avarabyeu/tripops-bot/internal/logistics"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// ---------------------------------------------------------------- vehicles --

func (s *Server) handleListVehicles(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	list, err := s.svc.Logistics.List(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"vehicles": list})
}

func (s *Server) handleCreateVehicle(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var in logistics.VehicleInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	vehicle, err := s.svc.Logistics.Create(r.Context(), access, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, vehicle)
}

func (s *Server) handleUpdateVehicle(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "vehicleID")
	if err != nil {
		return err
	}
	var in logistics.VehiclePatch
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	vehicle, err := s.svc.Logistics.Update(r.Context(), access, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, vehicle)
}

func (s *Server) handleDeleteVehicle(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "vehicleID")
	if err != nil {
		return err
	}
	if err := s.svc.Logistics.Delete(r.Context(), access, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (s *Server) handleJoinVehicle(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "vehicleID")
	if err != nil {
		return err
	}
	vehicle, err := s.svc.Logistics.Join(r.Context(), access, id)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, vehicle)
}

func (s *Server) handleLeaveVehicle(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "vehicleID")
	if err != nil {
		return err
	}
	vehicle, err := s.svc.Logistics.Leave(r.Context(), access, id)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, vehicle)
}

// ----------------------------------------------------------- accommodation --

func (s *Server) handleListAccommodation(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	list, err := s.svc.Accommodation.List(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"accommodations": list})
}

func (s *Server) handleCreateAccommodation(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var in accommodation.Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	place, err := s.svc.Accommodation.Create(r.Context(), access, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, place)
}

func (s *Server) handleUpdateAccommodation(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "placeID")
	if err != nil {
		return err
	}
	var in accommodation.Patch
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	place, err := s.svc.Accommodation.Update(r.Context(), access, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, place)
}

func (s *Server) handleDeleteAccommodation(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "placeID")
	if err != nil {
		return err
	}
	if err := s.svc.Accommodation.Delete(r.Context(), access, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (s *Server) handleGuestStatus(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "placeID")
	if err != nil {
		return err
	}
	var body struct {
		Status   accommodation.GuestStatus `json:"status"`
		MemberID string                    `json:"member_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	memberID, err := memberIDParam(body.MemberID, access.Member.ID)
	if err != nil {
		return err
	}
	place, err := s.svc.Accommodation.SetGuestStatus(r.Context(), access, id, memberID, body.Status)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, place)
}

// -------------------------------------------------------------- checklists --

func (s *Server) handleListChecklists(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	lists, err := s.svc.Checklists.Lists(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"checklists": lists,
		"progress":   checklists.SumProgress(lists),
	})
}

func (s *Server) handleCreateChecklist(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var in checklists.ListInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	list, err := s.svc.Checklists.CreateList(r.Context(), access, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, list)
}

func (s *Server) handleUpdateChecklist(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "listID")
	if err != nil {
		return err
	}
	var in checklists.ListPatch
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	list, err := s.svc.Checklists.UpdateList(r.Context(), access, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, list)
}

func (s *Server) handleDeleteChecklist(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "listID")
	if err != nil {
		return err
	}
	if err := s.svc.Checklists.DeleteList(r.Context(), access, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (s *Server) handleAddChecklistItem(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "listID")
	if err != nil {
		return err
	}
	var in checklists.ItemInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	item, err := s.svc.Checklists.AddItem(r.Context(), access, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, item)
}

func (s *Server) handleUpdateChecklistItem(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "itemID")
	if err != nil {
		return err
	}
	var in checklists.ItemPatch
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	item, err := s.svc.Checklists.UpdateItem(r.Context(), access, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, item)
}

func (s *Server) handleDeleteChecklistItem(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "itemID")
	if err != nil {
		return err
	}
	if err := s.svc.Checklists.DeleteItem(r.Context(), access, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}
