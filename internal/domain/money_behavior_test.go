package domain

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestMoneyExactArithmetic(t *testing.T) {
	a := money(t, "0.10")
	b := money(t, "0.20")
	sum, err := a.Add(b)
	if err != nil || sum.Amount() != "0.30" || sum.Units() != 30 {
		t.Fatalf("0.10 + 0.20: %v, %v", sum, err)
	}
	if a.Amount() != "0.10" || b.Amount() != "0.20" {
		t.Fatal("addition mutated an operand")
	}
	diff, err := a.Sub(b)
	if err != nil || diff.Amount() != "-0.10" {
		t.Fatalf("signed difference: %v, %v", diff, err)
	}
	positive, err := diff.Negate()
	if err != nil || positive != a || diff.Amount() != "-0.10" {
		t.Fatal("negation must return a new value", err)
	}
	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"1.00", "2.00", -1}, {"2.00", "1.00", 1}, {"1.00", "1.00", 0},
		{"-2.00", "-1.00", -1},
	} {
		got, err := money(t, tc.left).Compare(money(t, tc.right))
		if err != nil || got != tc.want {
			t.Fatalf("comparison: got %d, %v", got, err)
		}
	}
}

func TestMoneyExternalContract(t *testing.T) {
	for _, currency := range []string{"BRL", "USD", "EUR"} {
		var input MoneyInput
		payload := `{"amount":"25.00","currency":"` + currency + `"}`
		if err := json.Unmarshal([]byte(payload), &input); err != nil {
			t.Fatal(err)
		}
		m, err := input.Parse()
		if err != nil || m.Units() != 2500 || m.Currency() != currency {
			t.Fatal(m, err)
		}
		encoded, err := json.Marshal(m)
		if err != nil || string(encoded) != payload {
			t.Fatalf("JSON contract: %s, %v", encoded, err)
		}
		zero, err := Zero(currency)
		if err != nil || zero.Amount() != "0.00" || zero.ValidateExternal() != nil {
			t.Fatal(zero, err)
		}
	}
	for _, amount := range []string{"-0.01", "-0.00", "", "NaN", "Infinity", "1e2", "25.001", "25", "25.0", "025.00", " 25.00", "25.00\n", "25,00", "+25.00"} {
		t.Run(amount, func(t *testing.T) {
			if _, err := (MoneyInput{Amount: amount, Currency: "BRL"}).Parse(); err == nil {
				t.Fatal("invalid external input accepted")
			}
		})
	}
	var input MoneyInput
	if err := json.Unmarshal([]byte(`{"amount":25.00,"currency":"BRL"}`), &input); err == nil {
		t.Fatal("numeric JSON amount accepted; contract requires a string")
	}
	for _, currency := range []string{"", "brl", "XYZ", "JPY"} {
		if _, err := Parse("1.00", currency); !errors.Is(err, ErrCurrency) {
			t.Fatal("unsupported currency accepted", currency, err)
		}
	}
	if err := money(t, "-0.01").ValidateExternal(); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestMoneyZeroValueRejected(t *testing.T) {
	var invalid Money
	valid := money(t, "1.00")
	if invalid.Valid() || !errors.Is(invalid.ValidateExternal(), ErrInvalid) {
		t.Fatal("zero Go value accepted")
	}
	for _, op := range []func() (Money, error){
		func() (Money, error) { return invalid.Add(valid) },
		func() (Money, error) { return valid.Add(invalid) },
		func() (Money, error) { return invalid.Sub(valid) },
		func() (Money, error) { return valid.Sub(invalid) },
		invalid.Negate,
	} {
		if _, err := op(); !errors.Is(err, ErrInvalid) {
			t.Fatal("uninitialized operand accepted", err)
		}
	}
	if _, err := invalid.Compare(valid); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := valid.Compare(invalid); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestMoneyBoundaryResults(t *testing.T) {
	max, _ := FromUnits(math.MaxInt64, "BRL")
	min, _ := FromUnits(math.MinInt64, "BRL")
	sum, err := max.Add(min)
	if err != nil || sum.Amount() != "-0.01" {
		t.Fatal(sum, err)
	}
	zero, _ := Zero("BRL")
	if got, err := zero.Add(max); err != nil || got != max {
		t.Fatal(got, err)
	}
	if got, err := zero.Add(min); err != nil || got != min {
		t.Fatal(got, err)
	}
	if _, err := zero.Sub(min); !errors.Is(err, ErrOverflow) {
		t.Fatal(err)
	}
	for _, amount := range []string{"92233720368547758.08", "-92233720368547758.09"} {
		if _, err := Parse(amount, "BRL"); !errors.Is(err, ErrOverflow) {
			t.Fatal("overflow not classified", err)
		}
	}
}
