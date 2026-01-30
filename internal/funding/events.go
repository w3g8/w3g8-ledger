// Package funding provides wallet funding operations across all payment rails.
package funding

import (
	"encoding/json"
	"time"

	"github.com/oklog/ulid/v2"

	"finplatform/internal/common/money"
)

// NATS subjects for funding events
const (
	SubjectIntentCreated  = "funding.intent.created"
	SubjectFundingUpdate  = "funding.update"
	SubjectLedgerPost     = "ledger.post"
	SubjectLedgerPosted   = "ledger.posted"
	SubjectReconImported  = "recon.statement.imported"
	SubjectReconMismatch  = "recon.mismatch.detected"
)

// EventType identifies the type of funding event.
type EventType string

const (
	EventIntentCreated       EventType = "funding.intent.created"
	EventFundingPending      EventType = "funding.pending"
	EventFundingSettled      EventType = "funding.settled"
	EventFundingFailed       EventType = "funding.failed"
	EventFundingReversed     EventType = "funding.reversed"
	EventInboundCreditDetected EventType = "bank.inbound_credit"
)

// Envelope wraps all events with common metadata.
type Envelope struct {
	ID            string          `json:"id"`
	Type          EventType       `json:"type"`
	TenantID      string          `json:"tenant_id"`
	CorrelationID string          `json:"correlation_id"`
	Timestamp     time.Time       `json:"timestamp"`
	Data          json.RawMessage `json:"data"`
}

// NewEnvelope creates a new event envelope.
func NewEnvelope(eventType EventType, tenantID, correlationID string, data any) (*Envelope, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	return &Envelope{
		ID:            ulid.Make().String(),
		Type:          eventType,
		TenantID:      tenantID,
		CorrelationID: correlationID,
		Timestamp:     time.Now().UTC(),
		Data:          jsonData,
	}, nil
}

// IntentCreatedEvent is published when a funding intent is created.
type IntentCreatedEvent struct {
	IntentID       string       `json:"intent_id"`
	WalletID       string       `json:"wallet_id"`
	CustomerID     string       `json:"customer_id"`
	Amount         money.Money  `json:"amount"`
	Method         Method       `json:"method"`
	IdempotencyKey string       `json:"idempotency_key"`
}

// FundingUpdateEvent is the normalized update event from any rail.
type FundingUpdateEvent struct {
	IntentID      string       `json:"intent_id"`
	WalletID      string       `json:"wallet_id"`
	Status        IntentStatus `json:"status"`
	ProviderRef   string       `json:"provider_ref,omitempty"`
	Rail          string       `json:"rail"` // FPS, SEPA, OPEN_BANKING, CARD
	Amount        money.Money  `json:"amount"`
	ErrorCode     string       `json:"error_code,omitempty"`
	ErrorMessage  string       `json:"error_message,omitempty"`
	SettledAt     *time.Time   `json:"settled_at,omitempty"`
}

// LedgerPostCommand is sent to request a ledger posting.
type LedgerPostCommand struct {
	IntentID    string       `json:"intent_id"`
	TenantID    string       `json:"tenant_id"`
	WalletID    string       `json:"wallet_id"`
	Amount      money.Money  `json:"amount"`
	SourceType  string       `json:"source_type"` // deposit, card_funding, etc.
	SourceID    string       `json:"source_id"`
	Reference   string       `json:"reference"`
	Description string       `json:"description"`
}

// LedgerPostedEvent is published after ledger posting completes.
type LedgerPostedEvent struct {
	IntentID      string      `json:"intent_id"`
	BatchID       string      `json:"batch_id"`
	TenantID      string      `json:"tenant_id"`
	WalletID      string      `json:"wallet_id"`
	Amount        money.Money `json:"amount"`
	EntryCount    int         `json:"entry_count"`
	TotalDebits   int64       `json:"total_debits"`
	TotalCredits  int64       `json:"total_credits"`
}

// InboundCreditEvent is detected from bank statements.
type InboundCreditEvent struct {
	StatementID   string      `json:"statement_id"`
	Rail          string      `json:"rail"` // FPS, SEPA
	Reference     string      `json:"reference"`
	Amount        money.Money `json:"amount"`
	SenderName    string      `json:"sender_name,omitempty"`
	SenderAccount string      `json:"sender_account,omitempty"`
	ReceivedAt    time.Time   `json:"received_at"`
}

// ReconMismatchEvent is published when reconciliation finds discrepancies.
type ReconMismatchEvent struct {
	IntentID       string      `json:"intent_id,omitempty"`
	StatementRef   string      `json:"statement_ref"`
	ExpectedAmount money.Money `json:"expected_amount"`
	ActualAmount   money.Money `json:"actual_amount"`
	MismatchType   string      `json:"mismatch_type"` // amount, missing, duplicate
	DetectedAt     time.Time   `json:"detected_at"`
}
