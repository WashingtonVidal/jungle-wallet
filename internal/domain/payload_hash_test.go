package domain

import (
	"encoding/json"
	"testing"
)

func TestCanonicalHashPreservesMoneyAndLegacy(t *testing.T) {
	a := []byte(`{"providerId":"p","money":{"amount":"25.00","currency":"BRL"},"kind":"BET"}`)
	b := []byte(`{"kind":"BET","money":{"amount":"25.00","currency":"BRL"},"providerId":"p"}`)
	canonical, legacy, err := CanonicalPayloadHash(a)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := CanonicalPayloadHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != other {
		t.Fatal("key order changed canonical hash")
	}
	if legacy == canonical {
		t.Fatal("legacy compatibility path not exercised")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(a, &values); err != nil {
		t.Fatal(err)
	}
	var input MoneyInput
	if err := json.Unmarshal(values["money"], &input); err != nil {
		t.Fatal(err)
	}
	money, err := input.Parse()
	if err != nil || money.Units() != 2500 {
		t.Fatal("hash altered exact money")
	}
}
