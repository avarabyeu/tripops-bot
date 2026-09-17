package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/avarabyeu/tripops-bot/internal/httpx"
)

// routes registers the whole REST surface.
//
// Trip-scoped resources are nested under /trips/{tripID}/… rather than exposed
// flat (/events/{id}). The nesting is what lets one middleware authorize every
// request the same way, and it makes an id borrowed from another trip a 404
// instead of a leak.
func (s *Server) routes(r chi.Router) {
	// Unauthenticated operational endpoints.
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_ = httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.ready(r.Context()); err != nil {
			_ = httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		_ = httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.authenticate())

		// Identity and settings.
		r.Get("/me", s.handle(s.handleMe))
		r.Get("/me/notification-preferences", s.handle(s.handleGetPreferences))
		r.Patch("/me/notification-preferences", s.handle(s.handleUpdatePreferences))

		// Invites are resolved by token, not by trip: the caller is not a
		// member yet, which is the whole point.
		r.Get("/invites/{token}", s.handle(s.handlePreviewInvite))
		r.Post("/invites/{token}/join", s.handle(s.handleJoin))

		r.Get("/trips", s.handle(s.handleListTrips))
		r.Post("/trips", s.handle(s.handleCreateTrip))

		r.Route("/trips/{tripID}", func(r chi.Router) {
			r.Use(s.tripAccess)

			r.Get("/", s.withAccess(s.handleGetTrip))
			r.Patch("/", s.withAccess(s.handleUpdateTrip))
			r.Get("/dashboard", s.withAccess(s.handleDashboard))
			r.Get("/activity", s.withAccess(s.handleActivity))

			// People.
			r.Get("/members", s.withAccess(s.handleListMembers))
			r.Patch("/members/{memberID}", s.withAccess(s.handleUpdateMember))
			r.Delete("/members/{memberID}", s.withAccess(s.handleRemoveMember))
			r.Post("/transfer-ownership", s.withAccess(s.handleTransferOwnership))
			r.Get("/invites", s.withAccess(s.handleListInvites))
			r.Post("/invites", s.withAccess(s.handleCreateInvite))
			r.Delete("/invites/{inviteID}", s.withAccess(s.handleRevokeInvite))

			// Timeline.
			r.Get("/events", s.withAccess(s.handleListEvents))
			r.Post("/events", s.withAccess(s.handleCreateEvent))
			r.Get("/events/{eventID}", s.withAccess(s.handleGetEvent))
			r.Patch("/events/{eventID}", s.withAccess(s.handleUpdateEvent))
			r.Delete("/events/{eventID}", s.withAccess(s.handleDeleteEvent))
			r.Post("/events/{eventID}/rsvp", s.withAccess(s.handleRSVP))

			// Decisions.
			r.Get("/decisions", s.withAccess(s.handleListDecisions))
			r.Post("/decisions", s.withAccess(s.handleCreateDecision))
			r.Get("/decisions/{decisionID}", s.withAccess(s.handleGetDecision))
			r.Post("/decisions/{decisionID}/vote", s.withAccess(s.handleVote))
			r.Post("/decisions/{decisionID}/close", s.withAccess(s.handleCloseDecision))
			r.Post("/decisions/{decisionID}/resolve", s.withAccess(s.handleResolveDecision))
			r.Post("/decisions/{decisionID}/cancel", s.withAccess(s.handleCancelDecision))

			// Logistics.
			r.Get("/vehicles", s.withAccess(s.handleListVehicles))
			r.Post("/vehicles", s.withAccess(s.handleCreateVehicle))
			r.Patch("/vehicles/{vehicleID}", s.withAccess(s.handleUpdateVehicle))
			r.Delete("/vehicles/{vehicleID}", s.withAccess(s.handleDeleteVehicle))
			r.Post("/vehicles/{vehicleID}/join", s.withAccess(s.handleJoinVehicle))
			r.Post("/vehicles/{vehicleID}/leave", s.withAccess(s.handleLeaveVehicle))

			// Accommodation.
			r.Get("/accommodations", s.withAccess(s.handleListAccommodation))
			r.Post("/accommodations", s.withAccess(s.handleCreateAccommodation))
			r.Patch("/accommodations/{placeID}", s.withAccess(s.handleUpdateAccommodation))
			r.Delete("/accommodations/{placeID}", s.withAccess(s.handleDeleteAccommodation))
			r.Post("/accommodations/{placeID}/guest-status", s.withAccess(s.handleGuestStatus))

			// Checklists.
			r.Get("/checklists", s.withAccess(s.handleListChecklists))
			r.Post("/checklists", s.withAccess(s.handleCreateChecklist))
			r.Patch("/checklists/{listID}", s.withAccess(s.handleUpdateChecklist))
			r.Delete("/checklists/{listID}", s.withAccess(s.handleDeleteChecklist))
			r.Post("/checklists/{listID}/items", s.withAccess(s.handleAddChecklistItem))
			r.Patch("/checklist-items/{itemID}", s.withAccess(s.handleUpdateChecklistItem))
			r.Delete("/checklist-items/{itemID}", s.withAccess(s.handleDeleteChecklistItem))

			// Expenses and balances.
			r.Get("/expenses", s.withAccess(s.handleListExpenses))
			r.Post("/expenses", s.withAccess(s.handleCreateExpense))
			r.Patch("/expenses/{expenseID}", s.withAccess(s.handleUpdateExpense))
			r.Delete("/expenses/{expenseID}", s.withAccess(s.handleDeleteExpense))
			r.Get("/balances", s.withAccess(s.handleBalances))
			r.Post("/settlements", s.withAccess(s.handleCreateSettlement))
			r.Post("/settlements/{settlementID}/settle", s.withAccess(s.handleSettle))
			r.Delete("/settlements/{settlementID}", s.withAccess(s.handleCancelSettlement))

			// Attachments.
			r.Get("/attachments", s.withAccess(s.handleListAttachments))
			r.Post("/attachments", s.withAccess(s.handleCreateAttachment))
			r.Delete("/attachments/{attachmentID}", s.withAccess(s.handleDeleteAttachment))
		})
	})
}
