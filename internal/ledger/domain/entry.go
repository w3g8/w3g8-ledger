package domain

import (
	"errors"
	"time"

	"finplatform/internal/common/money"
)

// EntryType represents the type of ledger entry
type EntryType string

const (
	EntryTypeDebit  EntryType = "debit"
	EntryTypeCredit EntryType = "credit"
)

// BatchStatus represents the status of a ledger batch
type BatchStatus string

const (
	BatchStatusPending  BatchStatus = "pending"
	BatchStatusPosted   BatchStatus = "posted"
	BatchStatusReversed BatchStatus = "reversed"
)

// SourceType represents the source of a ledger batch
type SourceType string

const (
	SourceTypeDeposit    SourceType = "deposit"
	SourceTypeWithdrawal SourceType = "withdrawal"
	SourceTypePayment    SourceType = "payment"
	SourceTypeFee        SourceType = "fee"
	SourceTypeAdjustment SourceType = "adjustment"
	SourceTypeTransfer   SourceType = "transfer"
)

// Entry represents a single ledger entry
type Entry struct {
	ID           string         `json:"id"`
	BatchID      string         `json:"batch_id"`
	AccountID    string         `json:"account_id"`
	EntryType    EntryType      `json:"entry_type"`
	Amount       money.Money    `json:"amount"`
	BalanceAfter *int64         `json:"balance_after,omitempty"`
	Description  string         `json:"description,omitempty"`
	Sequence     int            `json:"sequence"`
	CreatedAt    time.Time      `json:"created_at"`
}

// NewEntry creates a new ledger entry
func NewEntry(id, batchID, accountID string, entryType EntryType, amount money.Money, sequence int) (*Entry, error) {
	if id == "" {
		return nil, errors.New("id is required")
	}
	if batchID == "" {
		return nil, errors.New("batch_id is required")
	}
	if accountID == "" {
		return nil, errors.New("account_id is required")
	}
	if amount.AmountMinor <= 0 {
		return nil, errors.New("amount must be positive")
	}

	return &Entry{
		ID:        id,
		BatchID:   batchID,
		AccountID: accountID,
		EntryType: entryType,
		Amount:    amount,
		Sequence:  sequence,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// Batch represents a ledger batch (a group of balanced entries)
type Batch struct {
	ID             string            `json:"id"`
	TenantID       string            `json:"tenant_id"`
	Reference      string            `json:"reference,omitempty"`
	Description    string            `json:"description,omitempty"`
	SourceType     SourceType        `json:"source_type"`
	SourceID       string            `json:"source_id,omitempty"`
	TotalDebits    money.Money       `json:"total_debits"`
	TotalCredits   money.Money       `json:"total_credits"`
	EntryCount     int               `json:"entry_count"`
	Status         BatchStatus       `json:"status"`
	PostedAt       *time.Time        `json:"posted_at,omitempty"`
	PostedBy       *string           `json:"posted_by,omitempty"`
	ReversedAt     *time.Time        `json:"reversed_at,omitempty"`
	ReversedBy     *string           `json:"reversed_by,omitempty"`
	ReversalReason string            `json:"reversal_reason,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	Entries        []*Entry          `json:"entries,omitempty"`
}

// BatchBuilder helps construct valid ledger batches
type BatchBuilder struct {
	batch   *Batch
	entries []*Entry
	debits  int64
	credits int64
	seq     int
	err     error
}

// NewBatchBuilder creates a new batch builder
func NewBatchBuilder(id, tenantID string, sourceType SourceType, currency money.Currency) *BatchBuilder {
	if id == "" || tenantID == "" {
		return &BatchBuilder{err: errors.New("id and tenant_id are required")}
	}

	return &BatchBuilder{
		batch: &Batch{
			ID:           id,
			TenantID:     tenantID,
			SourceType:   sourceType,
			TotalDebits:  money.Zero(currency),
			TotalCredits: money.Zero(currency),
			Status:       BatchStatusPending,
			Metadata:     make(map[string]string),
			CreatedAt:    time.Now().UTC(),
		},
		entries: make([]*Entry, 0),
		seq:     0,
	}
}

// WithReference sets the external reference
func (b *BatchBuilder) WithReference(reference string) *BatchBuilder {
	if b.err != nil {
		return b
	}
	b.batch.Reference = reference
	return b
}

// WithDescription sets the description
func (b *BatchBuilder) WithDescription(description string) *BatchBuilder {
	if b.err != nil {
		return b
	}
	b.batch.Description = description
	return b
}

// WithSourceID sets the source ID
func (b *BatchBuilder) WithSourceID(sourceID string) *BatchBuilder {
	if b.err != nil {
		return b
	}
	b.batch.SourceID = sourceID
	return b
}

// WithMetadata adds metadata
func (b *BatchBuilder) WithMetadata(key, value string) *BatchBuilder {
	if b.err != nil {
		return b
	}
	b.batch.Metadata[key] = value
	return b
}

// Debit adds a debit entry
func (b *BatchBuilder) Debit(entryID, accountID string, amount money.Money, description string) *BatchBuilder {
	if b.err != nil {
		return b
	}

	if amount.Currency != b.batch.TotalDebits.Currency {
		b.err = errors.New("entry currency must match batch currency")
		return b
	}

	b.seq++
	entry, err := NewEntry(entryID, b.batch.ID, accountID, EntryTypeDebit, amount, b.seq)
	if err != nil {
		b.err = err
		return b
	}
	entry.Description = description

	b.entries = append(b.entries, entry)
	b.debits += amount.AmountMinor
	return b
}

// Credit adds a credit entry
func (b *BatchBuilder) Credit(entryID, accountID string, amount money.Money, description string) *BatchBuilder {
	if b.err != nil {
		return b
	}

	if amount.Currency != b.batch.TotalCredits.Currency {
		b.err = errors.New("entry currency must match batch currency")
		return b
	}

	b.seq++
	entry, err := NewEntry(entryID, b.batch.ID, accountID, EntryTypeCredit, amount, b.seq)
	if err != nil {
		b.err = err
		return b
	}
	entry.Description = description

	b.entries = append(b.entries, entry)
	b.credits += amount.AmountMinor
	return b
}

// Build validates and returns the batch
func (b *BatchBuilder) Build() (*Batch, error) {
	if b.err != nil {
		return nil, b.err
	}

	if len(b.entries) == 0 {
		return nil, errors.New("batch must have at least one entry")
	}

	if b.debits != b.credits {
		return nil, errors.New("batch must be balanced (debits must equal credits)")
	}

	b.batch.TotalDebits.AmountMinor = b.debits
	b.batch.TotalCredits.AmountMinor = b.credits
	b.batch.EntryCount = len(b.entries)
	b.batch.Entries = b.entries

	return b.batch, nil
}

// Validate validates a batch is balanced
func (batch *Batch) Validate() error {
	if batch.TotalDebits.AmountMinor != batch.TotalCredits.AmountMinor {
		return errors.New("batch is not balanced")
	}

	if batch.TotalDebits.Currency != batch.TotalCredits.Currency {
		return errors.New("batch currencies do not match")
	}

	if len(batch.Entries) != batch.EntryCount {
		return errors.New("entry count mismatch")
	}

	var debits, credits int64
	for _, entry := range batch.Entries {
		if entry.EntryType == EntryTypeDebit {
			debits += entry.Amount.AmountMinor
		} else {
			credits += entry.Amount.AmountMinor
		}
	}

	if debits != batch.TotalDebits.AmountMinor || credits != batch.TotalCredits.AmountMinor {
		return errors.New("entry totals do not match batch totals")
	}

	return nil
}

// Post marks the batch as posted
func (batch *Batch) Post(userID string) error {
	if batch.Status != BatchStatusPending {
		return errors.New("only pending batches can be posted")
	}

	now := time.Now().UTC()
	batch.Status = BatchStatusPosted
	batch.PostedAt = &now
	if userID != "" {
		batch.PostedBy = &userID
	}
	return nil
}

// Reverse marks the batch as reversed
func (batch *Batch) Reverse(userID, reason string) error {
	if batch.Status != BatchStatusPosted {
		return errors.New("only posted batches can be reversed")
	}

	now := time.Now().UTC()
	batch.Status = BatchStatusReversed
	batch.ReversedAt = &now
	if userID != "" {
		batch.ReversedBy = &userID
	}
	batch.ReversalReason = reason
	return nil
}

// Position represents an account's position for a period
type Position struct {
	ID             string         `json:"id"`
	TenantID       string         `json:"tenant_id"`
	AccountID      string         `json:"account_id"`
	PeriodType     string         `json:"period_type"`
	PeriodStart    time.Time      `json:"period_start"`
	PeriodEnd      time.Time      `json:"period_end"`
	OpeningBalance int64          `json:"opening_balance"`
	DebitTotal     int64          `json:"debit_total"`
	CreditTotal    int64          `json:"credit_total"`
	ClosingBalance int64          `json:"closing_balance"`
	EntryCount     int            `json:"entry_count"`
	Currency       money.Currency `json:"currency"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// Balance returns the balance for an account given entries
func CalculateBalance(account *Account, entries []*Entry) int64 {
	var balance int64
	for _, entry := range entries {
		if entry.AccountID != account.ID {
			continue
		}

		if account.NormalBalance == NormalBalanceDebit {
			if entry.EntryType == EntryTypeDebit {
				balance += entry.Amount.AmountMinor
			} else {
				balance -= entry.Amount.AmountMinor
			}
		} else {
			if entry.EntryType == EntryTypeCredit {
				balance += entry.Amount.AmountMinor
			} else {
				balance -= entry.Amount.AmountMinor
			}
		}
	}
	return balance
}
