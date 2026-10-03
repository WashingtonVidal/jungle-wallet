// Package domain contains financial rules, without transport or database dependencies.
package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrInvalid      = errors.New("invalid input")
	ErrCurrency     = errors.New("incompatible currency")
	ErrOverflow     = errors.New("numeric overflow")
	ErrInsufficient = errors.New("insufficient balance")
	ErrTransition   = errors.New("invalid state transition")
)

// Money is immutable. The zero Go value is deliberately invalid.
// Supported currencies: BRL, USD and EUR, all with two decimal places.
type Money struct {
	units    int64
	currency string
}

var decimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)\.[0-9]{2}$`)

func FromUnits(units int64, currency string) (Money, error) {
	if currency != "BRL" && currency != "USD" && currency != "EUR" {
		return Money{}, ErrCurrency
	}
	return Money{units: units, currency: currency}, nil
}

func Zero(currency string) (Money, error) { return FromUnits(0, currency) }

// Parse accepts exactly two decimal places, no whitespace, exponents or leading zeros.
// Signed values are supported internally; ValidateExternal rejects negative inputs.
func Parse(amount, currency string) (Money, error) {
	if !decimal.MatchString(amount) || amount == "-0.00" {
		return Money{}, ErrInvalid
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(amount, ".", ""), 10, 64)
	if err != nil {
		return Money{}, ErrOverflow
	}
	return FromUnits(n, currency)
}

func (m Money) Valid() bool      { return m.currency == "BRL" || m.currency == "USD" || m.currency == "EUR" }
func (m Money) Units() int64     { return m.units }
func (m Money) Currency() string { return m.currency }
func (m Money) ValidateExternal() error {
	if !m.Valid() || m.units < 0 {
		return ErrInvalid
	}
	return nil
}
func (m Money) compatible(n Money) error {
	if !m.Valid() || !n.Valid() {
		return ErrInvalid
	}
	if m.currency != n.currency {
		return ErrCurrency
	}
	return nil
}
func (m Money) Add(n Money) (Money, error) {
	if err := m.compatible(n); err != nil {
		return Money{}, err
	}
	if n.units > 0 && m.units > math.MaxInt64-n.units || n.units < 0 && m.units < math.MinInt64-n.units {
		return Money{}, ErrOverflow
	}
	return FromUnits(m.units+n.units, m.currency)
}
func (m Money) Sub(n Money) (Money, error) {
	if err := m.compatible(n); err != nil {
		return Money{}, err
	}
	if n.units > 0 && m.units < math.MinInt64+n.units || n.units < 0 && m.units > math.MaxInt64+n.units {
		return Money{}, ErrOverflow
	}
	return FromUnits(m.units-n.units, m.currency)
}
func (m Money) Negate() (Money, error) {
	if !m.Valid() {
		return Money{}, ErrInvalid
	}
	if m.units == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return FromUnits(-m.units, m.currency)
}
func (m Money) Compare(n Money) (int, error) {
	if err := m.compatible(n); err != nil {
		return 0, err
	}
	if m.units < n.units {
		return -1, nil
	}
	if m.units > n.units {
		return 1, nil
	}
	return 0, nil
}
func (m Money) Amount() string {
	s := strconv.FormatInt(m.units, 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign = "-"
		s = s[1:]
	}
	for len(s) < 3 {
		s = "0" + s
	}
	return sign + s[:len(s)-2] + "." + s[len(s)-2:]
}
func (m Money) MarshalJSON() ([]byte, error) {
	if !m.Valid() {
		return nil, ErrInvalid
	}
	return json.Marshal(struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}{m.Amount(), m.currency})
}

// Decode through an external DTO, keeping Money immutable (no UnmarshalJSON setter).
type MoneyInput struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func (v MoneyInput) Parse() (Money, error) {
	if strings.HasPrefix(v.Amount, "-") {
		return Money{}, fmt.Errorf("%w: negative external money", ErrInvalid)
	}
	return Parse(v.Amount, v.Currency)
}
