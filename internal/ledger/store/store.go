package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finplatform/internal/common/database"
	"finplatform/internal/common/money"
	"finplatform/internal/ledger/domain"
)

// Store provides ledger data access
type Store struct {
	db *database.DB
}

// New creates a new ledger store
func New(db *database.DB) *Store {
	return &Store{db: db}
}

// CreateAccount creates a new ledger account
func (s *Store) CreateAccount(ctx context.Context, account *domain.Account) error {
	query := `
		INSERT INTO ledger_accounts (
			id, tenant_id, code, name, description, account_type, normal_balance,
			currency, parent_id, path, is_system, is_placeholder, status, metadata,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
		)
	`

	_, err := s.db.Exec(ctx, query,
		account.ID,
		account.TenantID,
		account.Code,
		account.Name,
		account.Description,
		account.AccountType,
		account.NormalBalance,
		account.Currency,
		account.ParentID,
		account.Path,
		account.IsSystem,
		account.IsPlaceholder,
		account.Status,
		account.Metadata,
		account.CreatedAt,
		account.UpdatedAt,
	)

	if err != nil {
		if database.IsUniqueViolation(err) {
			return fmt.Errorf("account with code %s already exists: %w", account.Code, database.ErrAlreadyExists)
		}
		return fmt.Errorf("creating account: %w", err)
	}

	return nil
}

// GetAccount retrieves an account by ID
func (s *Store) GetAccount(ctx context.Context, tenantID, id string) (*domain.Account, error) {
	query := `
		SELECT id, tenant_id, code, name, description, account_type, normal_balance,
			   currency, parent_id, path, is_system, is_placeholder, status, metadata,
			   created_at, updated_at
		FROM ledger_accounts
		WHERE tenant_id = $1 AND id = $2
	`

	row := s.db.QueryRow(ctx, query, tenantID, id)
	return scanAccount(row)
}

// GetAccountByCode retrieves an account by code
func (s *Store) GetAccountByCode(ctx context.Context, tenantID, code string) (*domain.Account, error) {
	query := `
		SELECT id, tenant_id, code, name, description, account_type, normal_balance,
			   currency, parent_id, path, is_system, is_placeholder, status, metadata,
			   created_at, updated_at
		FROM ledger_accounts
		WHERE tenant_id = $1 AND code = $2
	`

	row := s.db.QueryRow(ctx, query, tenantID, code)
	return scanAccount(row)
}

// ListAccounts lists accounts with optional filters
func (s *Store) ListAccounts(ctx context.Context, tenantID string, accountType *domain.AccountType, limit, offset int) ([]*domain.Account, int64, error) {
	countQuery := `SELECT COUNT(*) FROM ledger_accounts WHERE tenant_id = $1`
	query := `
		SELECT id, tenant_id, code, name, description, account_type, normal_balance,
			   currency, parent_id, path, is_system, is_placeholder, status, metadata,
			   created_at, updated_at
		FROM ledger_accounts
		WHERE tenant_id = $1
	`

	args := []interface{}{tenantID}

	if accountType != nil {
		countQuery += ` AND account_type = $2`
		query += ` AND account_type = $2`
		args = append(args, *accountType)
	}

	var total int64
	err := s.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("counting accounts: %w", err)
	}

	query += fmt.Sprintf(` ORDER BY code LIMIT %d OFFSET %d`, limit, offset)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*domain.Account
	for rows.Next() {
		account, err := scanAccountRows(rows)
		if err != nil {
			return nil, 0, err
		}
		accounts = append(accounts, account)
	}

	return accounts, total, nil
}

// CreateBatch creates a new ledger batch with entries (within a transaction)
func (s *Store) CreateBatch(ctx context.Context, batch *domain.Batch) error {
	return s.db.WithTx(ctx, func(tx pgx.Tx) error {
		return s.CreateBatchTx(ctx, tx, batch)
	})
}

// CreateBatchTx creates a batch within an existing transaction
func (s *Store) CreateBatchTx(ctx context.Context, tx pgx.Tx, batch *domain.Batch) error {
	// Validate batch first
	if err := batch.Validate(); err != nil {
		return err
	}

	// Insert batch
	batchQuery := `
		INSERT INTO ledger_batches (
			id, tenant_id, reference, description, source_type, source_id,
			total_debits, total_credits, entry_count, currency, status,
			posted_at, posted_by, metadata, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
		)
	`

	_, err := tx.Exec(ctx, batchQuery,
		batch.ID,
		batch.TenantID,
		batch.Reference,
		batch.Description,
		batch.SourceType,
		batch.SourceID,
		batch.TotalDebits.AmountMinor,
		batch.TotalCredits.AmountMinor,
		batch.EntryCount,
		batch.TotalDebits.Currency,
		batch.Status,
		batch.PostedAt,
		batch.PostedBy,
		batch.Metadata,
		batch.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting batch: %w", err)
	}

	// Insert entries
	entryQuery := `
		INSERT INTO ledger_entries (
			id, batch_id, account_id, entry_type, amount, currency,
			balance_after, description, sequence, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)
	`

	for _, entry := range batch.Entries {
		_, err := tx.Exec(ctx, entryQuery,
			entry.ID,
			entry.BatchID,
			entry.AccountID,
			entry.EntryType,
			entry.Amount.AmountMinor,
			entry.Amount.Currency,
			entry.BalanceAfter,
			entry.Description,
			entry.Sequence,
			entry.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("inserting entry: %w", err)
		}
	}

	return nil
}

// PostBatch posts a pending batch (updates status and calculates balances)
func (s *Store) PostBatch(ctx context.Context, tenantID, batchID, userID string) error {
	return s.db.WithTxOptions(ctx, database.SerializableTxOptions(), func(tx pgx.Tx) error {
		// Lock and get the batch
		batch, err := s.getBatchForUpdate(ctx, tx, tenantID, batchID)
		if err != nil {
			return err
		}

		if batch.Status != domain.BatchStatusPending {
			return errors.New("batch is not pending")
		}

		// Get entries
		entries, err := s.getEntriesTx(ctx, tx, batchID)
		if err != nil {
			return err
		}

		// Update balances for each account
		for _, entry := range entries {
			// Get current balance
			var currentBalance int64
			err := tx.QueryRow(ctx, `
				SELECT COALESCE(
					(SELECT balance_after FROM ledger_entries
					 WHERE account_id = $1 AND balance_after IS NOT NULL
					 ORDER BY created_at DESC LIMIT 1),
					0
				)
			`, entry.AccountID).Scan(&currentBalance)
			if err != nil {
				return fmt.Errorf("getting current balance: %w", err)
			}

			// Get account to determine normal balance
			var normalBalance domain.NormalBalance
			err = tx.QueryRow(ctx, `
				SELECT normal_balance FROM ledger_accounts WHERE id = $1
			`, entry.AccountID).Scan(&normalBalance)
			if err != nil {
				return fmt.Errorf("getting account: %w", err)
			}

			// Calculate new balance
			var newBalance int64
			if normalBalance == domain.NormalBalanceDebit {
				if entry.EntryType == domain.EntryTypeDebit {
					newBalance = currentBalance + entry.Amount.AmountMinor
				} else {
					newBalance = currentBalance - entry.Amount.AmountMinor
				}
			} else {
				if entry.EntryType == domain.EntryTypeCredit {
					newBalance = currentBalance + entry.Amount.AmountMinor
				} else {
					newBalance = currentBalance - entry.Amount.AmountMinor
				}
			}

			// Update entry with balance
			_, err = tx.Exec(ctx, `
				UPDATE ledger_entries SET balance_after = $1 WHERE id = $2
			`, newBalance, entry.ID)
			if err != nil {
				return fmt.Errorf("updating entry balance: %w", err)
			}
		}

		// Mark batch as posted
		now := time.Now().UTC()
		_, err = tx.Exec(ctx, `
			UPDATE ledger_batches
			SET status = $1, posted_at = $2, posted_by = $3
			WHERE id = $4
		`, domain.BatchStatusPosted, now, userID, batchID)
		if err != nil {
			return fmt.Errorf("posting batch: %w", err)
		}

		return nil
	})
}

// GetBatch retrieves a batch by ID
func (s *Store) GetBatch(ctx context.Context, tenantID, id string) (*domain.Batch, error) {
	query := `
		SELECT id, tenant_id, reference, description, source_type, source_id,
			   total_debits, total_credits, entry_count, currency, status,
			   posted_at, posted_by, reversed_at, reversed_by, reversal_reason,
			   metadata, created_at
		FROM ledger_batches
		WHERE tenant_id = $1 AND id = $2
	`

	row := s.db.QueryRow(ctx, query, tenantID, id)
	return scanBatch(row)
}

// GetBatchWithEntries retrieves a batch with its entries
func (s *Store) GetBatchWithEntries(ctx context.Context, tenantID, id string) (*domain.Batch, error) {
	batch, err := s.GetBatch(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	entries, err := s.GetEntries(ctx, id)
	if err != nil {
		return nil, err
	}

	batch.Entries = entries
	return batch, nil
}

// GetEntries retrieves entries for a batch
func (s *Store) GetEntries(ctx context.Context, batchID string) ([]*domain.Entry, error) {
	query := `
		SELECT id, batch_id, account_id, entry_type, amount, currency,
			   balance_after, description, sequence, created_at
		FROM ledger_entries
		WHERE batch_id = $1
		ORDER BY sequence
	`

	rows, err := s.db.Query(ctx, query, batchID)
	if err != nil {
		return nil, fmt.Errorf("getting entries: %w", err)
	}
	defer rows.Close()

	return scanEntries(rows)
}

// GetAccountEntries retrieves entries for an account
func (s *Store) GetAccountEntries(ctx context.Context, accountID string, from, to *time.Time, limit, offset int) ([]*domain.Entry, int64, error) {
	countQuery := `SELECT COUNT(*) FROM ledger_entries WHERE account_id = $1`
	query := `
		SELECT id, batch_id, account_id, entry_type, amount, currency,
			   balance_after, description, sequence, created_at
		FROM ledger_entries
		WHERE account_id = $1
	`
	args := []interface{}{accountID}
	argIdx := 2

	if from != nil {
		countQuery += fmt.Sprintf(` AND created_at >= $%d`, argIdx)
		query += fmt.Sprintf(` AND created_at >= $%d`, argIdx)
		args = append(args, *from)
		argIdx++
	}

	if to != nil {
		countQuery += fmt.Sprintf(` AND created_at <= $%d`, argIdx)
		query += fmt.Sprintf(` AND created_at <= $%d`, argIdx)
		args = append(args, *to)
	}

	var total int64
	err := s.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("counting entries: %w", err)
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d OFFSET %d`, limit, offset)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing entries: %w", err)
	}
	defer rows.Close()

	entries, err := scanEntries(rows)
	return entries, total, err
}

// GetAccountBalance retrieves the current balance for an account
func (s *Store) GetAccountBalance(ctx context.Context, accountID string) (int64, error) {
	query := `
		SELECT COALESCE(
			(SELECT balance_after FROM ledger_entries
			 WHERE account_id = $1 AND balance_after IS NOT NULL
			 ORDER BY created_at DESC LIMIT 1),
			0
		)
	`

	var balance int64
	err := s.db.QueryRow(ctx, query, accountID).Scan(&balance)
	if err != nil {
		return 0, fmt.Errorf("getting balance: %w", err)
	}

	return balance, nil
}

// Helper functions

func (s *Store) getBatchForUpdate(ctx context.Context, tx pgx.Tx, tenantID, id string) (*domain.Batch, error) {
	query := `
		SELECT id, tenant_id, reference, description, source_type, source_id,
			   total_debits, total_credits, entry_count, currency, status,
			   posted_at, posted_by, reversed_at, reversed_by, reversal_reason,
			   metadata, created_at
		FROM ledger_batches
		WHERE tenant_id = $1 AND id = $2
		FOR UPDATE
	`

	row := tx.QueryRow(ctx, query, tenantID, id)
	return scanBatch(row)
}

func (s *Store) getEntriesTx(ctx context.Context, tx pgx.Tx, batchID string) ([]*domain.Entry, error) {
	query := `
		SELECT id, batch_id, account_id, entry_type, amount, currency,
			   balance_after, description, sequence, created_at
		FROM ledger_entries
		WHERE batch_id = $1
		ORDER BY sequence
	`

	rows, err := tx.Query(ctx, query, batchID)
	if err != nil {
		return nil, fmt.Errorf("getting entries: %w", err)
	}
	defer rows.Close()

	return scanEntries(rows)
}

func scanAccount(row pgx.Row) (*domain.Account, error) {
	var a domain.Account
	err := row.Scan(
		&a.ID, &a.TenantID, &a.Code, &a.Name, &a.Description,
		&a.AccountType, &a.NormalBalance, &a.Currency, &a.ParentID,
		&a.Path, &a.IsSystem, &a.IsPlaceholder, &a.Status, &a.Metadata,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, database.ErrNotFound
		}
		return nil, fmt.Errorf("scanning account: %w", err)
	}
	return &a, nil
}

func scanAccountRows(rows pgx.Rows) (*domain.Account, error) {
	var a domain.Account
	err := rows.Scan(
		&a.ID, &a.TenantID, &a.Code, &a.Name, &a.Description,
		&a.AccountType, &a.NormalBalance, &a.Currency, &a.ParentID,
		&a.Path, &a.IsSystem, &a.IsPlaceholder, &a.Status, &a.Metadata,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning account: %w", err)
	}
	return &a, nil
}

func scanBatch(row pgx.Row) (*domain.Batch, error) {
	var b domain.Batch
	var totalDebits, totalCredits int64
	var currency string
	err := row.Scan(
		&b.ID, &b.TenantID, &b.Reference, &b.Description, &b.SourceType, &b.SourceID,
		&totalDebits, &totalCredits, &b.EntryCount, &currency, &b.Status,
		&b.PostedAt, &b.PostedBy, &b.ReversedAt, &b.ReversedBy, &b.ReversalReason,
		&b.Metadata, &b.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, database.ErrNotFound
		}
		return nil, fmt.Errorf("scanning batch: %w", err)
	}
	b.TotalDebits = money.New(totalDebits, money.Currency(currency))
	b.TotalCredits = money.New(totalCredits, money.Currency(currency))
	return &b, nil
}

func scanEntries(rows pgx.Rows) ([]*domain.Entry, error) {
	var entries []*domain.Entry
	for rows.Next() {
		var e domain.Entry
		var amount int64
		var currency string
		err := rows.Scan(
			&e.ID, &e.BatchID, &e.AccountID, &e.EntryType, &amount, &currency,
			&e.BalanceAfter, &e.Description, &e.Sequence, &e.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning entry: %w", err)
		}
		e.Amount = money.New(amount, money.Currency(currency))
		entries = append(entries, &e)
	}
	return entries, nil
}

// Querier interface for testing
type Querier interface {
	Exec(ctx context.Context, sql string, args ...interface{}) (pgxpool.Row, error)
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
}
