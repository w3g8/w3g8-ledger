package ledger

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"

	"finplatform/internal/common/database"
	"finplatform/internal/common/events"
	"finplatform/internal/common/money"
	"finplatform/internal/ledger/domain"
	"finplatform/internal/ledger/store"
)

// Service provides ledger operations
type Service struct {
	store  *store.Store
	db     *database.DB
	logger *slog.Logger
}

// NewService creates a new ledger service
func NewService(db *database.DB, logger *slog.Logger) *Service {
	return &Service{
		store:  store.New(db),
		db:     db,
		logger: logger,
	}
}

// CreateAccountRequest is the request to create an account
type CreateAccountRequest struct {
	TenantID      string              `json:"tenant_id" validate:"required"`
	Code          string              `json:"code" validate:"required,max=50"`
	Name          string              `json:"name" validate:"required,max=255"`
	Description   string              `json:"description"`
	AccountType   domain.AccountType  `json:"account_type" validate:"required,oneof=asset liability equity revenue expense"`
	Currency      money.Currency      `json:"currency" validate:"required,len=3"`
	ParentID      *string             `json:"parent_id"`
	IsSystem      bool                `json:"is_system"`
	IsPlaceholder bool                `json:"is_placeholder"`
}

// CreateAccount creates a new ledger account
func (s *Service) CreateAccount(ctx context.Context, req CreateAccountRequest) (*domain.Account, error) {
	id := ulid.Make().String()

	account, err := domain.NewAccount(id, req.TenantID, req.Code, req.Name, req.AccountType, req.Currency)
	if err != nil {
		return nil, fmt.Errorf("creating account: %w", err)
	}

	account.Description = req.Description
	account.IsSystem = req.IsSystem
	account.IsPlaceholder = req.IsPlaceholder

	// Handle parent relationship
	if req.ParentID != nil {
		parent, err := s.store.GetAccount(ctx, req.TenantID, *req.ParentID)
		if err != nil {
			return nil, fmt.Errorf("getting parent account: %w", err)
		}
		if err := account.SetParent(parent); err != nil {
			return nil, err
		}
	}

	if err := s.store.CreateAccount(ctx, account); err != nil {
		return nil, err
	}

	s.logger.Info("account created",
		"account_id", account.ID,
		"code", account.Code,
		"type", account.AccountType,
	)

	return account, nil
}

// GetAccount retrieves an account by ID
func (s *Service) GetAccount(ctx context.Context, tenantID, id string) (*domain.Account, error) {
	return s.store.GetAccount(ctx, tenantID, id)
}

// GetAccountByCode retrieves an account by code
func (s *Service) GetAccountByCode(ctx context.Context, tenantID, code string) (*domain.Account, error) {
	return s.store.GetAccountByCode(ctx, tenantID, code)
}

// ListAccounts lists accounts with optional type filter
func (s *Service) ListAccounts(ctx context.Context, tenantID string, accountType *domain.AccountType, limit, offset int) ([]*domain.Account, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return s.store.ListAccounts(ctx, tenantID, accountType, limit, offset)
}

// PostEntriesRequest represents a request to post ledger entries
type PostEntriesRequest struct {
	TenantID    string             `json:"tenant_id" validate:"required"`
	Reference   string             `json:"reference"`
	Description string             `json:"description"`
	SourceType  domain.SourceType  `json:"source_type" validate:"required"`
	SourceID    string             `json:"source_id"`
	Currency    money.Currency     `json:"currency" validate:"required,len=3"`
	Entries     []EntryRequest     `json:"entries" validate:"required,min=2,dive"`
}

// EntryRequest represents a single entry in a post request
type EntryRequest struct {
	AccountID   string           `json:"account_id" validate:"required"`
	EntryType   domain.EntryType `json:"entry_type" validate:"required,oneof=debit credit"`
	Amount      int64            `json:"amount" validate:"required,gt=0"`
	Description string           `json:"description"`
}

// PostEntries creates and posts a balanced set of ledger entries
func (s *Service) PostEntries(ctx context.Context, req PostEntriesRequest) (*domain.Batch, error) {
	batchID := ulid.Make().String()

	builder := domain.NewBatchBuilder(batchID, req.TenantID, req.SourceType, req.Currency).
		WithReference(req.Reference).
		WithDescription(req.Description).
		WithSourceID(req.SourceID)

	for _, e := range req.Entries {
		entryID := ulid.Make().String()
		amount := money.New(e.Amount, req.Currency)

		if e.EntryType == domain.EntryTypeDebit {
			builder.Debit(entryID, e.AccountID, amount, e.Description)
		} else {
			builder.Credit(entryID, e.AccountID, amount, e.Description)
		}
	}

	batch, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("building batch: %w", err)
	}

	// Create and post in a single transaction
	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.CreateBatchTx(ctx, tx, batch); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Post the batch
	if err := s.store.PostBatch(ctx, req.TenantID, batchID, ""); err != nil {
		return nil, fmt.Errorf("posting batch: %w", err)
	}

	// Fetch the posted batch
	batch, err = s.store.GetBatchWithEntries(ctx, req.TenantID, batchID)
	if err != nil {
		return nil, err
	}

	s.logger.Info("batch posted",
		"batch_id", batch.ID,
		"entry_count", batch.EntryCount,
		"total", batch.TotalDebits.AmountMinor,
		"currency", batch.TotalDebits.Currency,
	)

	return batch, nil
}

// GetBatch retrieves a batch with its entries
func (s *Service) GetBatch(ctx context.Context, tenantID, id string) (*domain.Batch, error) {
	return s.store.GetBatchWithEntries(ctx, tenantID, id)
}

// GetAccountBalance retrieves the current balance for an account
func (s *Service) GetAccountBalance(ctx context.Context, accountID string) (int64, error) {
	return s.store.GetAccountBalance(ctx, accountID)
}

// GetAccountEntries retrieves entries for an account
func (s *Service) GetAccountEntries(ctx context.Context, accountID string, limit, offset int) ([]*domain.Entry, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return s.store.GetAccountEntries(ctx, accountID, nil, nil, limit, offset)
}

// InitializeSystemAccounts creates the standard system accounts for a tenant
func (s *Service) InitializeSystemAccounts(ctx context.Context, tenantID string, currency money.Currency) error {
	systemAccounts := domain.SystemAccounts()

	for _, sa := range systemAccounts {
		req := CreateAccountRequest{
			TenantID:    tenantID,
			Code:        sa.Code,
			Name:        sa.Name,
			AccountType: sa.AccountType,
			Currency:    currency,
			IsSystem:    true,
		}

		_, err := s.CreateAccount(ctx, req)
		if err != nil {
			// Skip if already exists
			if database.IsUniqueViolation(err) {
				continue
			}
			return fmt.Errorf("creating system account %s: %w", sa.Code, err)
		}
	}

	s.logger.Info("system accounts initialized",
		"tenant_id", tenantID,
		"currency", currency,
	)

	return nil
}

// CreateBatchPostedEvent creates an event for a posted batch
func (s *Service) CreateBatchPostedEvent(batch *domain.Batch) (*events.Event, error) {
	data := events.LedgerBatchPostedData{
		BatchID:      batch.ID,
		EntryCount:   batch.EntryCount,
		TotalDebits:  batch.TotalDebits.AmountMinor,
		TotalCredits: batch.TotalCredits.AmountMinor,
		Currency:     string(batch.TotalDebits.Currency),
	}

	return events.NewEvent(
		events.EventLedgerBatchPosted,
		batch.TenantID,
		"ledger_batch",
		batch.ID,
		data,
	)
}
