package domain

import "testing"

func TestTickValidate(t *testing.T) {
	tk := &Tick{Market: MarketAShare, Symbol: " 600000 ", LastPrice: 10.5}
	if err := tk.Validate(); err != nil {
		t.Fatalf("valid tick rejected: %v", err)
	}
	if tk.Symbol != "600000" {
		t.Fatalf("symbol should be normalized, got %q", tk.Symbol)
	}
	if tk.TimestampMS <= 0 {
		t.Fatal("timestamp should be defaulted")
	}
	if tk.Key() != "A_SHARE:600000" {
		t.Fatalf("unexpected key: %s", tk.Key())
	}
}

func TestTickValidateRejects(t *testing.T) {
	if err := (&Tick{Symbol: "600000"}).Validate(); err == nil {
		t.Fatal("zero price should be rejected")
	}
	if err := (&Tick{LastPrice: 1}).Validate(); err == nil {
		t.Fatal("empty symbol should be rejected")
	}
}

func TestParseMarket(t *testing.T) {
	if m, err := ParseMarket("a_share"); err != nil || m != MarketAShare {
		t.Fatalf("ParseMarket a_share: %v %v", m, err)
	}
	if _, err := ParseMarket("NOPE"); err == nil {
		t.Fatal("unknown market should error")
	}
	if MarketCrypto.String() != "CRYPTO" {
		t.Fatalf("unexpected name: %s", MarketCrypto.String())
	}
}
