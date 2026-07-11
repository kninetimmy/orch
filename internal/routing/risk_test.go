package routing

import "testing"

func TestRiskDomainValid(t *testing.T) {
	tests := map[string]struct {
		domain RiskDomain
		want   bool
	}{
		"security":               {RiskSecurity, true},
		"authentication":         {RiskAuthentication, true},
		"authorization":          {RiskAuthorization, true},
		"secrets":                {RiskSecrets, true},
		"cryptography":           {RiskCryptography, true},
		"migrations":             {RiskMigrations, true},
		"concurrency":            {RiskConcurrency, true},
		"data-integrity":         {RiskDataIntegrity, true},
		"destructive-operations": {RiskDestructiveOperations, true},
		"empty rejected":         {"", false},
		"titlecase rejected":     {"Security", false},
		"unknown rejected":       {"performance", false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.domain.Valid(); got != tt.want {
				t.Errorf("RiskDomain(%q).Valid() = %v, want %v", tt.domain, got, tt.want)
			}
		})
	}
}

func TestDomainsOrderStable(t *testing.T) {
	want := []RiskDomain{
		RiskSecurity, RiskAuthentication, RiskAuthorization, RiskSecrets,
		RiskCryptography, RiskMigrations, RiskConcurrency, RiskDataIntegrity,
		RiskDestructiveOperations,
	}
	for call := 0; call < 2; call++ {
		got := Domains()
		if len(got) != len(want) {
			t.Fatalf("Domains() returned %d domains, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Domains()[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	}
}

func TestDomainsAllValid(t *testing.T) {
	for _, d := range Domains() {
		if !d.Valid() {
			t.Errorf("Domains() member %q is not Valid()", d)
		}
	}
}
