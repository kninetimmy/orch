package routing

// RiskDomain is one of the sensitive domains PRD §11 routes directly to
// a Specialist executor plus a strong Reviewer. The set is closed: any
// other value is an unknown domain and rejected as ErrBadTask.
type RiskDomain string

const (
	RiskSecurity              RiskDomain = "security"
	RiskAuthentication        RiskDomain = "authentication"
	RiskAuthorization         RiskDomain = "authorization"
	RiskSecrets               RiskDomain = "secrets"
	RiskCryptography          RiskDomain = "cryptography"
	RiskMigrations            RiskDomain = "migrations"
	RiskConcurrency           RiskDomain = "concurrency"
	RiskDataIntegrity         RiskDomain = "data-integrity"
	RiskDestructiveOperations RiskDomain = "destructive-operations"
)

// Domains returns the nine risk domains in canonical order. Rationale
// strings render present domains in this order, so the audit record is
// deterministic regardless of the caller's Task ordering.
func Domains() []RiskDomain {
	return []RiskDomain{
		RiskSecurity,
		RiskAuthentication,
		RiskAuthorization,
		RiskSecrets,
		RiskCryptography,
		RiskMigrations,
		RiskConcurrency,
		RiskDataIntegrity,
		RiskDestructiveOperations,
	}
}

// Valid reports whether d is a member of the closed risk-domain set.
func (d RiskDomain) Valid() bool {
	switch d {
	case RiskSecurity, RiskAuthentication, RiskAuthorization, RiskSecrets,
		RiskCryptography, RiskMigrations, RiskConcurrency, RiskDataIntegrity,
		RiskDestructiveOperations:
		return true
	default:
		return false
	}
}
