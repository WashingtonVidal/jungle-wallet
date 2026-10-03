package domain

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func money(t *testing.T, amount string) Money {
	t.Helper()
	m, e := Parse(amount, "BRL")
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestMoneyParsing(t *testing.T) {
	for _, s := range []string{"0.00", "25.00", "92233720368547758.07", "-92233720368547758.08"} {
		m, e := Parse(s, "BRL")
		if e != nil || m.Amount() != s {
			t.Fatalf("%s: %v %s", s, e, m.Amount())
		}
		b, e := json.Marshal(m)
		if e != nil || !json.Valid(b) {
			t.Fatal(e)
		}
	}
	for _, s := range []string{"", "NaN", "Infinity", "1e2", "1.001", "01.00", "1", "1.0", " 1.00", "+1.00", "-0.00", "92233720368547758.08", "-92233720368547758.09"} {
		if _, e := Parse(s, "BRL"); e == nil {
			t.Errorf("accepted %q", s)
		}
	}
	if _, e := (MoneyInput{"-1.00", "BRL"}).Parse(); e == nil {
		t.Fatal("negative external input accepted")
	}
	if _, e := json.Marshal(Money{}); e == nil {
		t.Fatal("zero value serialized")
	}
}
func TestMoneyArithmeticBoundaries(t *testing.T) {
	max, _ := FromUnits(math.MaxInt64, "BRL")
	min, _ := FromUnits(math.MinInt64, "BRL")
	one, _ := FromUnits(1, "BRL")
	neg, _ := FromUnits(-1, "BRL")
	for _, op := range []func() (Money, error){func() (Money, error) { return max.Add(one) }, func() (Money, error) { return min.Add(neg) }, func() (Money, error) { return min.Sub(one) }, func() (Money, error) { return max.Sub(neg) }, min.Negate} {
		if _, e := op(); !errors.Is(e, ErrOverflow) {
			t.Fatalf("expected overflow, got %v", e)
		}
	}
	zero, e := min.Sub(min)
	if e != nil || zero.Units() != 0 {
		t.Fatal("valid subtraction rejected", e)
	}
	usd, _ := Parse("1.00", "USD")
	brl := money(t, "1.00")
	if _, e := brl.Add(usd); !errors.Is(e, ErrCurrency) {
		t.Fatal(e)
	}
	if _, e := brl.Sub(usd); !errors.Is(e, ErrCurrency) {
		t.Fatal(e)
	}
	if _, e := brl.Compare(usd); !errors.Is(e, ErrCurrency) {
		t.Fatal(e)
	}
	if _, e := (Money{}).Add(brl); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
