package api

import (
	"net/http"

	"github.com/avarabyeu/tripops-bot/internal/attachments"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/httpx"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// ---------------------------------------------------------------- expenses --

func (s *Server) handleListExpenses(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	list, err := s.svc.Expenses.List(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"expenses": list,
		"totals":   expenses.SummariseTotals(list, access.Trip.Currency),
	})
}

func (s *Server) handleCreateExpense(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var in expenses.Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	expense, err := s.svc.Expenses.Create(r.Context(), access, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, expense)
}

func (s *Server) handleUpdateExpense(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "expenseID")
	if err != nil {
		return err
	}
	var in expenses.Patch
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	expense, err := s.svc.Expenses.Update(r.Context(), access, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, expense)
}

func (s *Server) handleDeleteExpense(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "expenseID")
	if err != nil {
		return err
	}
	if err := s.svc.Expenses.Delete(r.Context(), access, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// ---------------------------------------------------------------- balances --

func (s *Server) handleExpenseReport(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	report, err := s.svc.Expenses.Report(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, report)
}

func (s *Server) handleBalances(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	report, err := s.svc.Expenses.Balances(r.Context(), access)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, report)
}

func (s *Server) handleCreateSettlement(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var in expenses.SettlementInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	settlement, err := s.svc.Expenses.RecordSettlement(r.Context(), access, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, settlement)
}

func (s *Server) handleSettle(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "settlementID")
	if err != nil {
		return err
	}
	settlement, err := s.svc.Expenses.MarkSettled(r.Context(), access, id)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, settlement)
}

func (s *Server) handleCancelSettlement(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "settlementID")
	if err != nil {
		return err
	}
	if err := s.svc.Expenses.CancelSettlement(r.Context(), access, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// ------------------------------------------------------------- attachments --

func (s *Server) handleListAttachments(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	ownerType := attachments.OwnerType(r.URL.Query().Get("owner_type"))
	ownerID, err := httpx.QueryID(r, "owner_id")
	if err != nil {
		return err
	}
	list, err := s.svc.Attachments.List(r.Context(), access, ownerType, ownerID)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"attachments": list})
}

func (s *Server) handleCreateAttachment(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	var in attachments.Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	attachment, err := s.svc.Attachments.Add(r.Context(), access, in)
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusCreated, attachment)
}

func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request, access trips.Access) error {
	id, err := httpx.PathID(r, "attachmentID")
	if err != nil {
		return err
	}
	if err := s.svc.Attachments.Delete(r.Context(), access, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}
