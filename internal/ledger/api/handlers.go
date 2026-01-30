package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"finplatform/internal/common/api"
	"finplatform/internal/common/database"
	"finplatform/internal/common/middleware"
	"finplatform/internal/common/money"
	"finplatform/internal/ledger"
	"finplatform/internal/ledger/domain"
)

// Handler handles ledger HTTP requests
type Handler struct {
	service *ledger.Service
}

// NewHandler creates a new ledger handler
func NewHandler(service *ledger.Service) *Handler {
	return &Handler{service: service}
}

// Routes returns the ledger routes
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	// Account routes
	r.Post("/accounts", h.CreateAccount)
	r.Get("/accounts", h.ListAccounts)
	r.Get("/accounts/{id}", h.GetAccount)
	r.Get("/accounts/{id}/entries", h.GetAccountEntries)
	r.Get("/accounts/{id}/balance", h.GetAccountBalance)

	// Batch/Entry routes
	r.Post("/entries", h.PostEntries)
	r.Get("/batches/{id}", h.GetBatch)

	// Admin routes
	r.Post("/init-system-accounts", h.InitializeSystemAccounts)

	return r
}

// CreateAccountRequest is the API request for creating an account
type CreateAccountRequest struct {
	Code          string `json:"code" validate:"required,max=50"`
	Name          string `json:"name" validate:"required,max=255"`
	Description   string `json:"description"`
	AccountType   string `json:"account_type" validate:"required,oneof=asset liability equity revenue expense"`
	Currency      string `json:"currency" validate:"required,len=3"`
	ParentID      string `json:"parent_id"`
	IsPlaceholder bool   `json:"is_placeholder"`
}

// CreateAccount handles POST /accounts
func (h *Handler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())
	if tenantID == "" {
		api.BadRequest(w, "tenant ID required")
		return
	}

	var req CreateAccountRequest
	if err := api.DecodeAndValidate(r, &req); err != nil {
		api.ValidationError(w, err)
		return
	}

	var parentID *string
	if req.ParentID != "" {
		parentID = &req.ParentID
	}

	svcReq := ledger.CreateAccountRequest{
		TenantID:      tenantID,
		Code:          req.Code,
		Name:          req.Name,
		Description:   req.Description,
		AccountType:   domain.AccountType(req.AccountType),
		Currency:      parseStringToCurrency(req.Currency),
		ParentID:      parentID,
		IsPlaceholder: req.IsPlaceholder,
	}

	account, err := h.service.CreateAccount(r.Context(), svcReq)
	if err != nil {
		if database.IsUniqueViolation(err) {
			api.Conflict(w, "account with this code already exists")
			return
		}
		api.InternalError(w, "failed to create account")
		return
	}

	api.WriteData(w, http.StatusCreated, account)
}

// ListAccounts handles GET /accounts
func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())
	if tenantID == "" {
		api.BadRequest(w, "tenant ID required")
		return
	}

	var accountType *domain.AccountType
	if typeStr := r.URL.Query().Get("type"); typeStr != "" {
		t := domain.AccountType(typeStr)
		accountType = &t
	}

	limit := 50
	offset := 0
	// Parse limit/offset from query params if needed

	accounts, total, err := h.service.ListAccounts(r.Context(), tenantID, accountType, limit, offset)
	if err != nil {
		api.InternalError(w, "failed to list accounts")
		return
	}

	api.WritePaginated(w, accounts, &api.Pagination{
		Limit:   limit,
		Offset:  offset,
		Total:   total,
		HasMore: int64(offset+len(accounts)) < total,
	})
}

// GetAccount handles GET /accounts/{id}
func (h *Handler) GetAccount(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())
	if tenantID == "" {
		api.BadRequest(w, "tenant ID required")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		api.BadRequest(w, "account ID required")
		return
	}

	account, err := h.service.GetAccount(r.Context(), tenantID, id)
	if err != nil {
		if database.IsNotFound(err) {
			api.NotFound(w, "account not found")
			return
		}
		api.InternalError(w, "failed to get account")
		return
	}

	api.WriteData(w, http.StatusOK, account)
}

// GetAccountEntries handles GET /accounts/{id}/entries
func (h *Handler) GetAccountEntries(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		api.BadRequest(w, "account ID required")
		return
	}

	limit := 50
	offset := 0

	entries, total, err := h.service.GetAccountEntries(r.Context(), id, limit, offset)
	if err != nil {
		api.InternalError(w, "failed to get entries")
		return
	}

	api.WritePaginated(w, entries, &api.Pagination{
		Limit:   limit,
		Offset:  offset,
		Total:   total,
		HasMore: int64(offset+len(entries)) < total,
	})
}

// GetAccountBalance handles GET /accounts/{id}/balance
func (h *Handler) GetAccountBalance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		api.BadRequest(w, "account ID required")
		return
	}

	balance, err := h.service.GetAccountBalance(r.Context(), id)
	if err != nil {
		api.InternalError(w, "failed to get balance")
		return
	}

	api.WriteData(w, http.StatusOK, map[string]int64{"balance": balance})
}

// PostEntriesRequest is the API request for posting entries
type PostEntriesRequest struct {
	Reference   string        `json:"reference"`
	Description string        `json:"description"`
	SourceType  string        `json:"source_type" validate:"required,oneof=deposit withdrawal payment fee adjustment transfer"`
	SourceID    string        `json:"source_id"`
	Currency    string        `json:"currency" validate:"required,len=3"`
	Entries     []EntryInput  `json:"entries" validate:"required,min=2,dive"`
}

// EntryInput represents a single entry input
type EntryInput struct {
	AccountID   string `json:"account_id" validate:"required"`
	EntryType   string `json:"entry_type" validate:"required,oneof=debit credit"`
	Amount      int64  `json:"amount" validate:"required,gt=0"`
	Description string `json:"description"`
}

// PostEntries handles POST /entries
func (h *Handler) PostEntries(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())
	if tenantID == "" {
		api.BadRequest(w, "tenant ID required")
		return
	}

	var req PostEntriesRequest
	if err := api.DecodeAndValidate(r, &req); err != nil {
		api.ValidationError(w, err)
		return
	}

	entries := make([]ledger.EntryRequest, len(req.Entries))
	for i, e := range req.Entries {
		entries[i] = ledger.EntryRequest{
			AccountID:   e.AccountID,
			EntryType:   domain.EntryType(e.EntryType),
			Amount:      e.Amount,
			Description: e.Description,
		}
	}

	svcReq := ledger.PostEntriesRequest{
		TenantID:    tenantID,
		Reference:   req.Reference,
		Description: req.Description,
		SourceType:  domain.SourceType(req.SourceType),
		SourceID:    req.SourceID,
		Currency:    parseStringToCurrency(req.Currency),
		Entries:     entries,
	}

	batch, err := h.service.PostEntries(r.Context(), svcReq)
	if err != nil {
		api.InternalError(w, err.Error())
		return
	}

	api.WriteData(w, http.StatusCreated, batch)
}

// GetBatch handles GET /batches/{id}
func (h *Handler) GetBatch(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())
	if tenantID == "" {
		api.BadRequest(w, "tenant ID required")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		api.BadRequest(w, "batch ID required")
		return
	}

	batch, err := h.service.GetBatch(r.Context(), tenantID, id)
	if err != nil {
		if database.IsNotFound(err) {
			api.NotFound(w, "batch not found")
			return
		}
		api.InternalError(w, "failed to get batch")
		return
	}

	api.WriteData(w, http.StatusOK, batch)
}

// InitSystemAccountsRequest is the request for initializing system accounts
type InitSystemAccountsRequest struct {
	Currency string `json:"currency" validate:"required,len=3"`
}

// InitializeSystemAccounts handles POST /init-system-accounts
func (h *Handler) InitializeSystemAccounts(w http.ResponseWriter, r *http.Request) {
	tenantID := middleware.GetTenantID(r.Context())
	if tenantID == "" {
		api.BadRequest(w, "tenant ID required")
		return
	}

	var req InitSystemAccountsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}

	err := h.service.InitializeSystemAccounts(r.Context(), tenantID, parseStringToCurrency(req.Currency))
	if err != nil {
		api.InternalError(w, "failed to initialize system accounts")
		return
	}

	api.WriteData(w, http.StatusOK, map[string]string{"status": "initialized"})
}

func parseStringToCurrency(s string) money.Currency {
	return money.Currency(s)
}
