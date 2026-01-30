// Package funding provides wallet funding operations across all payment rails.
package funding

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/oklog/ulid/v2"

	"finplatform/internal/common/money"
)

// Service orchestrates funding operations across all payment rails.
type Service struct {
	store     Store
	publisher Publisher
	ledger    LedgerClient
	logger    *slog.Logger

	// Provider adapters
	fps         FPSProvider
	sepa        SEPAProvider
	openBanking OpenBankingProvider
	cards       CardProvider
}

// Store persists funding intents and attempts.
type Store interface {
	// Intent operations
	CreateIntent(ctx context.Context, intent *FundingIntent) error
	GetIntent(ctx context.Context, tenantID, intentID string) (*FundingIntent, error)
	GetIntentByIdempotencyKey(ctx context.Context, tenantID, key string) (*FundingIntent, error)
	UpdateIntent(ctx context.Context, intent *FundingIntent) error
	ListPendingIntents(ctx context.Context, tenantID string, olderThan time.Duration, limit int) ([]*FundingIntent, error)

	// Attempt operations
	CreateAttempt(ctx context.Context, attempt *FundingAttempt) error
	GetAttempt(ctx context.Context, attemptID string) (*FundingAttempt, error)
	UpdateAttempt(ctx context.Context, attempt *FundingAttempt) error
	ListAttempts(ctx context.Context, intentID string) ([]*FundingAttempt, error)

	// Reference matching (for SEPA/FPS inbound)
	GetIntentByReference(ctx context.Context, tenantID, reference string) (*FundingIntent, error)
}

// Publisher publishes events to NATS.
type Publisher interface {
	Publish(ctx context.Context, subject string, envelope *Envelope) error
}

// LedgerClient posts entries to the ledger service.
type LedgerClient interface {
	PostFunding(ctx context.Context, req *LedgerPostCommand) (batchID string, err error)
}

// FPSProvider handles FPS payments.
type FPSProvider interface {
	Submit(ctx context.Context, intent *FundingIntent, attemptID string) (providerRef string, err error)
	GetStatus(ctx context.Context, providerRef string) (status string, settledAt *time.Time, err error)
}

// SEPAProvider handles SEPA payments.
type SEPAProvider interface {
	Submit(ctx context.Context, intent *FundingIntent, attemptID string) (providerRef string, err error)
	GetStatus(ctx context.Context, providerRef string) (status string, settledAt *time.Time, err error)
}

// OpenBankingProvider handles Open Banking payments.
type OpenBankingProvider interface {
	Initiate(ctx context.Context, intent *FundingIntent) (authURL string, providerRef string, err error)
	HandleCallback(ctx context.Context, providerRef string) (status string, err error)
}

// CardProvider handles card payments.
type CardProvider interface {
	Charge(ctx context.Context, intent *FundingIntent, cardToken string, threeDS *ThreeDSData) (providerRef string, err error)
	Capture(ctx context.Context, providerRef string) error
	Refund(ctx context.Context, providerRef string, amount money.Money) error
}

// ThreeDSData contains 3DS authentication data.
type ThreeDSData struct {
	Version       string
	Cavv          string
	Eci           string
	TransactionID string
}

// Config holds service configuration.
type Config struct {
	DefaultExpiry time.Duration
}

// NewService creates a new funding service.
func NewService(store Store, publisher Publisher, ledger LedgerClient, logger *slog.Logger) *Service {
	return &Service{
		store:     store,
		publisher: publisher,
		ledger:    ledger,
		logger:    logger,
	}
}

// SetFPSProvider sets the FPS provider.
func (s *Service) SetFPSProvider(p FPSProvider) { s.fps = p }

// SetSEPAProvider sets the SEPA provider.
func (s *Service) SetSEPAProvider(p SEPAProvider) { s.sepa = p }

// SetOpenBankingProvider sets the Open Banking provider.
func (s *Service) SetOpenBankingProvider(p OpenBankingProvider) { s.openBanking = p }

// SetCardProvider sets the card provider.
func (s *Service) SetCardProvider(p CardProvider) { s.cards = p }

// CreateIntentRequest is the request to create a funding intent.
type CreateIntentRequest struct {
	TenantID       string            `json:"tenant_id" validate:"required"`
	WalletID       string            `json:"wallet_id" validate:"required"`
	CustomerID     string            `json:"customer_id" validate:"required"`
	Amount         money.Money       `json:"amount" validate:"required"`
	Method         Method            `json:"method" validate:"required"`
	IdempotencyKey string            `json:"idempotency_key" validate:"required"`
	ReturnURL      string            `json:"return_url,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// CreateIntentResponse is the response from creating a funding intent.
type CreateIntentResponse struct {
	IntentID       string       `json:"intent_id"`
	Status         IntentStatus `json:"status"`
	RedirectURL    string       `json:"redirect_url,omitempty"`    // For Open Banking
	BankDetails    *BankDetails `json:"bank_details,omitempty"`    // For SEPA/FPS inbound
	PaymentSession string       `json:"payment_session,omitempty"` // For cards
}

// CreateIntent creates a new funding intent.
func (s *Service) CreateIntent(ctx context.Context, req *CreateIntentRequest) (*CreateIntentResponse, error) {
	// Check idempotency
	existing, err := s.store.GetIntentByIdempotencyKey(ctx, req.TenantID, req.IdempotencyKey)
	if err == nil && existing != nil {
		s.logger.Info("returning existing intent for idempotency key",
			"intent_id", existing.ID,
			"idempotency_key", req.IdempotencyKey,
		)
		return &CreateIntentResponse{
			IntentID:       existing.ID,
			Status:         existing.Status,
			RedirectURL:    existing.RedirectURL,
			BankDetails:    existing.BankDetails,
			PaymentSession: existing.PaymentSession,
		}, nil
	}

	// Create new intent
	intentID := ulid.Make().String()
	intent, err := NewFundingIntent(
		intentID,
		req.TenantID,
		req.WalletID,
		req.CustomerID,
		req.Amount,
		req.Method,
		req.IdempotencyKey,
	)
	if err != nil {
		return nil, fmt.Errorf("create intent: %w", err)
	}

	intent.Metadata = req.Metadata

	// Initialize based on method
	resp := &CreateIntentResponse{
		IntentID: intentID,
		Status:   IntentCreated,
	}

	switch req.Method {
	case MethodOpenBanking:
		if s.openBanking == nil {
			return nil, fmt.Errorf("open banking provider not configured")
		}
		authURL, providerRef, err := s.openBanking.Initiate(ctx, intent)
		if err != nil {
			return nil, fmt.Errorf("initiate open banking: %w", err)
		}
		intent.RedirectURL = authURL
		intent.ProviderRef = providerRef
		intent.Status = IntentPending
		resp.RedirectURL = authURL
		resp.Status = IntentPending

	case MethodSEPA, MethodFPS:
		// Generate unique reference for inbound matching
		reference := fmt.Sprintf("W3G8-%s", intentID[:8])
		intent.BankDetails = &BankDetails{
			Reference: reference,
			// Bank details would come from config
			IBAN:      "GB82WEST12345698765432", // Placeholder
			SortCode:  "123456",
			AccountNumber: "98765432",
		}
		resp.BankDetails = intent.BankDetails

	case MethodCard:
		// Card session created on demand
		intent.PaymentSession = fmt.Sprintf("session_%s", intentID)
		resp.PaymentSession = intent.PaymentSession
	}

	if err := s.store.CreateIntent(ctx, intent); err != nil {
		return nil, fmt.Errorf("store intent: %w", err)
	}

	// Publish event
	event := &IntentCreatedEvent{
		IntentID:       intent.ID,
		WalletID:       intent.WalletID,
		CustomerID:     intent.CustomerID,
		Amount:         intent.Amount,
		Method:         intent.Method,
		IdempotencyKey: intent.IdempotencyKey,
	}
	if env, err := NewEnvelope(EventIntentCreated, intent.TenantID, intent.ID, event); err == nil {
		s.publisher.Publish(ctx, SubjectIntentCreated, env)
	}

	s.logger.Info("funding intent created",
		"intent_id", intentID,
		"method", req.Method,
		"amount", req.Amount.AmountMinor,
		"currency", req.Amount.Currency,
	)

	return resp, nil
}

// GetIntent retrieves a funding intent.
func (s *Service) GetIntent(ctx context.Context, tenantID, intentID string) (*FundingIntent, error) {
	return s.store.GetIntent(ctx, tenantID, intentID)
}

// ProcessInboundCredit handles an inbound bank credit (SEPA/FPS).
func (s *Service) ProcessInboundCredit(ctx context.Context, event *InboundCreditEvent) error {
	s.logger.Info("processing inbound credit",
		"reference", event.Reference,
		"amount", event.Amount.AmountMinor,
		"rail", event.Rail,
	)

	// Match by reference
	// Extract tenant from reference or use default matching
	tenantID := "default" // Would be extracted from reference format
	intent, err := s.store.GetIntentByReference(ctx, tenantID, event.Reference)
	if err != nil {
		s.logger.Warn("no matching intent for inbound credit",
			"reference", event.Reference,
		)
		// Could create orphan record for manual matching
		return nil
	}

	// Verify amount matches
	if intent.Amount.AmountMinor != event.Amount.AmountMinor {
		s.logger.Warn("amount mismatch for funding intent",
			"intent_id", intent.ID,
			"expected", intent.Amount.AmountMinor,
			"received", event.Amount.AmountMinor,
		)
		// Publish mismatch event
		mismatch := &ReconMismatchEvent{
			IntentID:       intent.ID,
			StatementRef:   event.Reference,
			ExpectedAmount: intent.Amount,
			ActualAmount:   event.Amount,
			MismatchType:   "amount",
			DetectedAt:     time.Now(),
		}
		if env, err := NewEnvelope(EventFundingFailed, intent.TenantID, intent.ID, mismatch); err == nil {
			s.publisher.Publish(ctx, SubjectReconMismatch, env)
		}
		return fmt.Errorf("amount mismatch")
	}

	// Post to ledger
	return s.settleIntent(ctx, intent)
}

// ProcessCardPayment handles a card payment completion.
func (s *Service) ProcessCardPayment(ctx context.Context, intentID, transactionID string, captured bool) error {
	intent, err := s.store.GetIntent(ctx, "", intentID) // Would need tenant
	if err != nil {
		return err
	}

	if !captured {
		intent.MarkFailed("CARD_DECLINED", "Card payment was not captured")
		s.store.UpdateIntent(ctx, intent)
		return nil
	}

	intent.ProviderRef = transactionID
	return s.settleIntent(ctx, intent)
}

// settleIntent posts to ledger and marks intent as settled.
func (s *Service) settleIntent(ctx context.Context, intent *FundingIntent) error {
	// Post to ledger
	cmd := &LedgerPostCommand{
		IntentID:    intent.ID,
		TenantID:    intent.TenantID,
		WalletID:    intent.WalletID,
		Amount:      intent.Amount,
		SourceType:  string(intent.Method),
		SourceID:    intent.ID,
		Reference:   intent.ProviderRef,
		Description: fmt.Sprintf("Wallet funding via %s", intent.Method),
	}

	batchID, err := s.ledger.PostFunding(ctx, cmd)
	if err != nil {
		return fmt.Errorf("post to ledger: %w", err)
	}

	// Update intent
	if err := intent.MarkSettled(batchID); err != nil {
		return err
	}

	if err := s.store.UpdateIntent(ctx, intent); err != nil {
		return err
	}

	// Publish settled event
	event := &FundingUpdateEvent{
		IntentID:    intent.ID,
		WalletID:    intent.WalletID,
		Status:      IntentSettled,
		ProviderRef: intent.ProviderRef,
		Rail:        string(intent.Method),
		Amount:      intent.Amount,
		SettledAt:   intent.SettledAt,
	}
	if env, err := NewEnvelope(EventFundingSettled, intent.TenantID, intent.ID, event); err == nil {
		s.publisher.Publish(ctx, SubjectFundingUpdate, env)
	}

	s.logger.Info("funding intent settled",
		"intent_id", intent.ID,
		"batch_id", batchID,
		"amount", intent.Amount.AmountMinor,
	)

	return nil
}

// ProcessChargeback handles a chargeback/reversal.
func (s *Service) ProcessChargeback(ctx context.Context, intentID, reason string) error {
	intent, err := s.store.GetIntent(ctx, "", intentID)
	if err != nil {
		return err
	}

	if err := intent.MarkReversed(reason); err != nil {
		return err
	}

	// TODO: Post reversal to ledger

	if err := s.store.UpdateIntent(ctx, intent); err != nil {
		return err
	}

	// Publish reversed event
	event := &FundingUpdateEvent{
		IntentID:    intent.ID,
		WalletID:    intent.WalletID,
		Status:      IntentReversed,
		ProviderRef: intent.ProviderRef,
		Rail:        string(intent.Method),
		Amount:      intent.Amount,
	}
	if env, err := NewEnvelope(EventFundingReversed, intent.TenantID, intent.ID, event); err == nil {
		s.publisher.Publish(ctx, SubjectFundingUpdate, env)
	}

	s.logger.Info("funding intent reversed",
		"intent_id", intent.ID,
		"reason", reason,
	)

	return nil
}
