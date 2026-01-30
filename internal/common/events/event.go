package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/oklog/ulid/v2"
)

// Event represents a domain event envelope
type Event struct {
	ID            string          `json:"event_id"`
	Type          string          `json:"type"`
	Version       int             `json:"version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	CorrelationID string          `json:"correlation_id"`
	CausationID   string          `json:"causation_id,omitempty"`
	TenantID      string          `json:"tenant_id"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Data          json.RawMessage `json:"data"`
}

// NewEvent creates a new event
func NewEvent(eventType string, tenantID, aggregateType, aggregateID string, data interface{}) (*Event, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	return &Event{
		ID:            ulid.Make().String(),
		Type:          eventType,
		Version:       1,
		OccurredAt:    time.Now().UTC(),
		TenantID:      tenantID,
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		Data:          dataBytes,
	}, nil
}

// WithCorrelation adds correlation and causation IDs
func (e *Event) WithCorrelation(correlationID, causationID string) *Event {
	e.CorrelationID = correlationID
	e.CausationID = causationID
	return e
}

// DecodeData decodes the event data into a struct
func (e *Event) DecodeData(v interface{}) error {
	return json.Unmarshal(e.Data, v)
}

// OutboxEntry represents an event in the outbox table
type OutboxEntry struct {
	ID          string    `db:"id"`
	EventID     string    `db:"event_id"`
	EventType   string    `db:"event_type"`
	Payload     []byte    `db:"payload"`
	CreatedAt   time.Time `db:"created_at"`
	PublishedAt *time.Time `db:"published_at"`
	Attempts    int       `db:"attempts"`
	LastError   *string   `db:"last_error"`
}

// EventStore is the interface for event storage
type EventStore interface {
	// Save persists an event (typically within a transaction)
	Save(ctx context.Context, event *Event) error
	// SaveToOutbox saves an event to the outbox (within a transaction)
	SaveToOutbox(ctx context.Context, event *Event) error
}

// EventPublisher publishes events to a message broker
type EventPublisher interface {
	Publish(ctx context.Context, event *Event) error
	PublishBatch(ctx context.Context, events []*Event) error
}

// EventHandler handles incoming events
type EventHandler interface {
	Handle(ctx context.Context, event *Event) error
	EventTypes() []string
}

// Common event types
const (
	// IAM events
	EventTenantCreated = "iam.tenant.created"
	EventUserCreated   = "iam.user.created"
	EventAPIKeyCreated = "iam.api_key.created"

	// Customer events
	EventCustomerCreated     = "customer.created"
	EventCustomerUpdated     = "customer.updated"
	EventKYCCaseCreated      = "customer.kyc_case.created"
	EventKYCCaseApproved     = "customer.kyc_case.approved"
	EventKYCCaseRejected     = "customer.kyc_case.rejected"

	// Ledger events
	EventLedgerAccountCreated = "ledger.account.created"
	EventLedgerBatchPosted    = "ledger.batch.posted"

	// Wallet events
	EventWalletCreated      = "wallet.created"
	EventWalletCredited     = "wallet.credited"
	EventWalletDebited      = "wallet.debited"
	EventWalletHoldCreated  = "wallet.hold.created"
	EventWalletHoldReleased = "wallet.hold.released"
	EventWalletHoldCaptured = "wallet.hold.captured"

	// Deposit events
	EventDepositReceived = "deposit.received"
	EventDepositMatched  = "deposit.matched"
	EventDepositCredited = "deposit.credited"
	EventDepositReturned = "deposit.returned"

	// Payment events
	EventPaymentIntentCreated   = "payment.intent.created"
	EventPaymentRouted          = "payment.routed"
	EventPaymentSubmitted       = "payment.submitted"
	EventPaymentCompleted       = "payment.completed"
	EventPaymentFailed          = "payment.failed"

	// Fee events
	EventFeeQuoteCreated = "fee.quote.created"
	EventFeeAccrued      = "fee.accrued"

	// Affiliate events
	EventAffiliateEarningAccrued = "affiliate.earning.accrued"
	EventAffiliatePayoutCreated  = "affiliate.payout.created"

	// Rules events
	EventPolicyCreated   = "rules.policy.created"
	EventPolicyActivated = "rules.policy.activated"
)

// Event data structures

// CustomerCreatedData is the data for customer.created events
type CustomerCreatedData struct {
	CustomerID string `json:"customer_id"`
	ExternalID string `json:"external_id,omitempty"`
	Email      string `json:"email"`
	Name       string `json:"name"`
}

// KYCCaseApprovedData is the data for customer.kyc_case.approved events
type KYCCaseApprovedData struct {
	CaseID     string    `json:"case_id"`
	CustomerID string    `json:"customer_id"`
	Level      string    `json:"level"`
	ApprovedAt time.Time `json:"approved_at"`
	ApprovedBy string    `json:"approved_by"`
}

// LedgerBatchPostedData is the data for ledger.batch.posted events
type LedgerBatchPostedData struct {
	BatchID       string `json:"batch_id"`
	EntryCount    int    `json:"entry_count"`
	TotalDebits   int64  `json:"total_debits"`
	TotalCredits  int64  `json:"total_credits"`
	Currency      string `json:"currency"`
}

// WalletCreditedData is the data for wallet.credited events
type WalletCreditedData struct {
	WalletID    string `json:"wallet_id"`
	AccountID   string `json:"account_id"`
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Reference   string `json:"reference"`
	NewBalance  int64  `json:"new_balance"`
}

// DepositReceivedData is the data for deposit.received events
type DepositReceivedData struct {
	DepositID    string `json:"deposit_id"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
	Source       string `json:"source"`
	Reference    string `json:"reference"`
}

// PaymentCompletedData is the data for payment.completed events
type PaymentCompletedData struct {
	PaymentID    string `json:"payment_id"`
	IntentID     string `json:"intent_id"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
	Recipient    string `json:"recipient"`
	CompletedAt  time.Time `json:"completed_at"`
}
