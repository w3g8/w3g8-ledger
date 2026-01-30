package money

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Currency represents an ISO 4217 currency code
type Currency string

const (
	USD Currency = "USD"
	EUR Currency = "EUR"
	GBP Currency = "GBP"
	JPY Currency = "JPY"
)

// CurrencyInfo contains metadata about a currency
type CurrencyInfo struct {
	Code          Currency
	MinorUnits    int // Number of decimal places
	Symbol        string
	SymbolFirst   bool
}

var currencies = map[Currency]CurrencyInfo{
	USD: {Code: USD, MinorUnits: 2, Symbol: "$", SymbolFirst: true},
	EUR: {Code: EUR, MinorUnits: 2, Symbol: "€", SymbolFirst: true},
	GBP: {Code: GBP, MinorUnits: 2, Symbol: "£", SymbolFirst: true},
	JPY: {Code: JPY, MinorUnits: 0, Symbol: "¥", SymbolFirst: true},
}

// GetCurrencyInfo returns info about a currency
func GetCurrencyInfo(c Currency) (CurrencyInfo, bool) {
	info, ok := currencies[c]
	return info, ok
}

// Money represents a monetary amount in minor units (cents, pence, etc.)
type Money struct {
	AmountMinor int64    `json:"amount_minor"`
	Currency    Currency `json:"currency"`
}

// New creates a new Money value from minor units
func New(amountMinor int64, currency Currency) Money {
	return Money{
		AmountMinor: amountMinor,
		Currency:    currency,
	}
}

// NewFromMajor creates Money from major units (e.g., dollars)
func NewFromMajor(amountMajor float64, currency Currency) Money {
	info, ok := currencies[currency]
	if !ok {
		info = CurrencyInfo{MinorUnits: 2}
	}
	multiplier := math.Pow(10, float64(info.MinorUnits))
	return Money{
		AmountMinor: int64(math.Round(amountMajor * multiplier)),
		Currency:    currency,
	}
}

// Zero returns a zero amount for a currency
func Zero(currency Currency) Money {
	return Money{AmountMinor: 0, Currency: currency}
}

// IsZero returns true if the amount is zero
func (m Money) IsZero() bool {
	return m.AmountMinor == 0
}

// IsPositive returns true if the amount is positive
func (m Money) IsPositive() bool {
	return m.AmountMinor > 0
}

// IsNegative returns true if the amount is negative
func (m Money) IsNegative() bool {
	return m.AmountMinor < 0
}

// Abs returns the absolute value
func (m Money) Abs() Money {
	if m.AmountMinor < 0 {
		return Money{AmountMinor: -m.AmountMinor, Currency: m.Currency}
	}
	return m
}

// Negate returns the negated amount
func (m Money) Negate() Money {
	return Money{AmountMinor: -m.AmountMinor, Currency: m.Currency}
}

// Add adds two money values (must be same currency)
func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("currency mismatch: %s vs %s", m.Currency, other.Currency)
	}
	return Money{
		AmountMinor: m.AmountMinor + other.AmountMinor,
		Currency:    m.Currency,
	}, nil
}

// MustAdd adds two money values, panics on currency mismatch
func (m Money) MustAdd(other Money) Money {
	result, err := m.Add(other)
	if err != nil {
		panic(err)
	}
	return result
}

// Sub subtracts two money values (must be same currency)
func (m Money) Sub(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("currency mismatch: %s vs %s", m.Currency, other.Currency)
	}
	return Money{
		AmountMinor: m.AmountMinor - other.AmountMinor,
		Currency:    m.Currency,
	}, nil
}

// MustSub subtracts two money values, panics on currency mismatch
func (m Money) MustSub(other Money) Money {
	result, err := m.Sub(other)
	if err != nil {
		panic(err)
	}
	return result
}

// Multiply multiplies by an integer
func (m Money) Multiply(factor int64) Money {
	return Money{
		AmountMinor: m.AmountMinor * factor,
		Currency:    m.Currency,
	}
}

// MultiplyFloat multiplies by a float (rounds to nearest)
func (m Money) MultiplyFloat(factor float64) Money {
	return Money{
		AmountMinor: int64(math.Round(float64(m.AmountMinor) * factor)),
		Currency:    m.Currency,
	}
}

// Divide divides by an integer with rounding
func (m Money) Divide(divisor int64) Money {
	if divisor == 0 {
		panic("division by zero")
	}
	return Money{
		AmountMinor: int64(math.Round(float64(m.AmountMinor) / float64(divisor))),
		Currency:    m.Currency,
	}
}

// Percentage calculates a percentage (basis points / 10000)
func (m Money) Percentage(basisPoints int64) Money {
	return Money{
		AmountMinor: int64(math.Round(float64(m.AmountMinor) * float64(basisPoints) / 10000)),
		Currency:    m.Currency,
	}
}

// Compare returns -1, 0, or 1
func (m Money) Compare(other Money) (int, error) {
	if m.Currency != other.Currency {
		return 0, fmt.Errorf("currency mismatch: %s vs %s", m.Currency, other.Currency)
	}
	if m.AmountMinor < other.AmountMinor {
		return -1, nil
	}
	if m.AmountMinor > other.AmountMinor {
		return 1, nil
	}
	return 0, nil
}

// Equal checks equality
func (m Money) Equal(other Money) bool {
	return m.AmountMinor == other.AmountMinor && m.Currency == other.Currency
}

// GreaterThan checks if m > other
func (m Money) GreaterThan(other Money) bool {
	cmp, err := m.Compare(other)
	return err == nil && cmp > 0
}

// LessThan checks if m < other
func (m Money) LessThan(other Money) bool {
	cmp, err := m.Compare(other)
	return err == nil && cmp < 0
}

// ToMajor converts to major units as float
func (m Money) ToMajor() float64 {
	info, ok := currencies[m.Currency]
	if !ok {
		info = CurrencyInfo{MinorUnits: 2}
	}
	divisor := math.Pow(10, float64(info.MinorUnits))
	return float64(m.AmountMinor) / divisor
}

// String returns a human-readable representation
func (m Money) String() string {
	info, ok := currencies[m.Currency]
	if !ok {
		return fmt.Sprintf("%d %s (minor)", m.AmountMinor, m.Currency)
	}
	major := m.ToMajor()
	format := fmt.Sprintf("%%.%df", info.MinorUnits)
	if info.SymbolFirst {
		return fmt.Sprintf("%s"+format, info.Symbol, major)
	}
	return fmt.Sprintf(format+"%s", major, info.Symbol)
}

// MarshalJSON implements json.Marshaler
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
	}{
		AmountMinor: m.AmountMinor,
		Currency:    string(m.Currency),
	})
}

// UnmarshalJSON implements json.Unmarshaler
func (m *Money) UnmarshalJSON(data []byte) error {
	var v struct {
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m.AmountMinor = v.AmountMinor
	m.Currency = Currency(v.Currency)
	return nil
}

// Scan implements sql.Scanner
func (m *Money) Scan(src interface{}) error {
	if src == nil {
		*m = Money{}
		return nil
	}
	switch v := src.(type) {
	case int64:
		m.AmountMinor = v
		return nil
	case []byte:
		return json.Unmarshal(v, m)
	case string:
		return json.Unmarshal([]byte(v), m)
	default:
		return errors.New("cannot scan into Money")
	}
}

// Value implements driver.Valuer
func (m Money) Value() (driver.Value, error) {
	return json.Marshal(m)
}

// Allocate splits money into n parts with remainder going to first allocation
func (m Money) Allocate(parts int) []Money {
	if parts <= 0 {
		return nil
	}

	base := m.AmountMinor / int64(parts)
	remainder := m.AmountMinor % int64(parts)

	result := make([]Money, parts)
	for i := 0; i < parts; i++ {
		result[i] = Money{
			AmountMinor: base,
			Currency:    m.Currency,
		}
	}

	// Distribute remainder
	for i := int64(0); i < remainder; i++ {
		result[i].AmountMinor++
	}

	return result
}

// AllocateByRatios splits money by ratios (e.g., [1, 2, 3] = 1/6, 2/6, 3/6)
func (m Money) AllocateByRatios(ratios []int64) []Money {
	if len(ratios) == 0 {
		return nil
	}

	var total int64
	for _, r := range ratios {
		total += r
	}
	if total == 0 {
		return nil
	}

	result := make([]Money, len(ratios))
	var allocated int64

	for i, ratio := range ratios {
		share := int64(math.Round(float64(m.AmountMinor) * float64(ratio) / float64(total)))
		result[i] = Money{
			AmountMinor: share,
			Currency:    m.Currency,
		}
		allocated += share
	}

	// Handle rounding remainder
	if diff := m.AmountMinor - allocated; diff != 0 {
		result[0].AmountMinor += diff
	}

	return result
}

// Sum adds up multiple money values
func Sum(amounts ...Money) (Money, error) {
	if len(amounts) == 0 {
		return Money{}, nil
	}

	result := amounts[0]
	for _, a := range amounts[1:] {
		var err error
		result, err = result.Add(a)
		if err != nil {
			return Money{}, err
		}
	}
	return result, nil
}

// MustSum sums values, panics on currency mismatch
func MustSum(amounts ...Money) Money {
	result, err := Sum(amounts...)
	if err != nil {
		panic(err)
	}
	return result
}
