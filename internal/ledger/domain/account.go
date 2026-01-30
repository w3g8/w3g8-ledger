package domain

import (
	"errors"
	"time"

	"finplatform/internal/common/money"
)

// AccountType represents the type of ledger account
type AccountType string

const (
	AccountTypeAsset     AccountType = "asset"
	AccountTypeLiability AccountType = "liability"
	AccountTypeEquity    AccountType = "equity"
	AccountTypeRevenue   AccountType = "revenue"
	AccountTypeExpense   AccountType = "expense"
)

// NormalBalance represents the normal balance side of an account
type NormalBalance string

const (
	NormalBalanceDebit  NormalBalance = "debit"
	NormalBalanceCredit NormalBalance = "credit"
)

// AccountStatus represents the status of an account
type AccountStatus string

const (
	AccountStatusActive   AccountStatus = "active"
	AccountStatusInactive AccountStatus = "inactive"
	AccountStatusClosed   AccountStatus = "closed"
)

// Account represents a ledger account
type Account struct {
	ID            string            `json:"id"`
	TenantID      string            `json:"tenant_id"`
	Code          string            `json:"code"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	AccountType   AccountType       `json:"account_type"`
	NormalBalance NormalBalance     `json:"normal_balance"`
	Currency      money.Currency    `json:"currency"`
	ParentID      *string           `json:"parent_id,omitempty"`
	Path          string            `json:"path"`
	IsSystem      bool              `json:"is_system"`
	IsPlaceholder bool              `json:"is_placeholder"`
	Status        AccountStatus     `json:"status"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// NewAccount creates a new account
func NewAccount(id, tenantID, code, name string, accountType AccountType, currency money.Currency) (*Account, error) {
	if id == "" {
		return nil, errors.New("id is required")
	}
	if tenantID == "" {
		return nil, errors.New("tenant_id is required")
	}
	if code == "" {
		return nil, errors.New("code is required")
	}
	if name == "" {
		return nil, errors.New("name is required")
	}

	normalBalance := GetNormalBalance(accountType)

	return &Account{
		ID:            id,
		TenantID:      tenantID,
		Code:          code,
		Name:          name,
		AccountType:   accountType,
		NormalBalance: normalBalance,
		Currency:      currency,
		Path:          code,
		Status:        AccountStatusActive,
		Metadata:      make(map[string]string),
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}, nil
}

// GetNormalBalance returns the normal balance for an account type
func GetNormalBalance(accountType AccountType) NormalBalance {
	switch accountType {
	case AccountTypeAsset, AccountTypeExpense:
		return NormalBalanceDebit
	case AccountTypeLiability, AccountTypeEquity, AccountTypeRevenue:
		return NormalBalanceCredit
	default:
		return NormalBalanceDebit
	}
}

// SetParent sets the parent account and updates the path
func (a *Account) SetParent(parent *Account) error {
	if parent == nil {
		a.ParentID = nil
		a.Path = a.Code
		return nil
	}

	if parent.TenantID != a.TenantID {
		return errors.New("parent account must be in the same tenant")
	}
	if parent.Currency != a.Currency {
		return errors.New("parent account must have the same currency")
	}

	a.ParentID = &parent.ID
	a.Path = parent.Path + "/" + a.Code
	return nil
}

// CanHaveEntries returns whether this account can have entries posted to it
func (a *Account) CanHaveEntries() bool {
	return !a.IsPlaceholder && a.Status == AccountStatusActive
}

// SystemAccounts returns the standard system account codes
func SystemAccounts() []struct {
	Code        string
	Name        string
	AccountType AccountType
} {
	return []struct {
		Code        string
		Name        string
		AccountType AccountType
	}{
		// Assets
		{"1000", "Cash and Equivalents", AccountTypeAsset},
		{"1100", "Customer Wallet Assets", AccountTypeAsset},
		{"1200", "Accounts Receivable", AccountTypeAsset},
		{"1300", "Pending Settlements", AccountTypeAsset},

		// Liabilities
		{"2000", "Customer Wallet Liabilities", AccountTypeLiability},
		{"2100", "Accounts Payable", AccountTypeLiability},
		{"2200", "Pending Payouts", AccountTypeLiability},
		{"2300", "Held Funds", AccountTypeLiability},

		// Equity
		{"3000", "Retained Earnings", AccountTypeEquity},
		{"3100", "Owner's Equity", AccountTypeEquity},

		// Revenue
		{"4000", "Fee Revenue", AccountTypeRevenue},
		{"4100", "Transaction Fees", AccountTypeRevenue},
		{"4200", "Service Fees", AccountTypeRevenue},

		// Expenses
		{"5000", "Operating Expenses", AccountTypeExpense},
		{"5100", "Payment Processing Costs", AccountTypeExpense},
		{"5200", "Affiliate Commissions", AccountTypeExpense},
	}
}
