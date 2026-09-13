package app

type RequesterKind int

const (
	RequesterUser RequesterKind = iota
	RequesterService
)

// Credential kinds: which store the acting credential's id belongs to. They
// are the vocabulary of Requester.CredentialKind and of audit_log.credential_kind
// — the column that says whether a credential id names a row in `sessions` or
// in `api_tokens`.
const (
	CredentialSession  = "session"
	CredentialAPIToken = "api_token"
)

type Requester struct {
	Kind RequesterKind
	ID   string
	// CredentialID is the CREDENTIAL this request authenticated with — the
	// session for a human, the API token for a machine — and CredentialKind
	// says which of the two (CredentialSession / CredentialAPIToken).
	//
	// It exists so the two ADR-0008 streams join: security_events records that
	// a session began, from where, and after which refusals; audit_log records
	// what changed. Without a credential on the envelope, a suspicious session
	// and the owner's own concurrent session are the same actor in the trail,
	// and "what did THAT session do" cannot be asked at all. Both are empty for
	// a principal that authenticated with neither credential — the scheduler's
	// system actor.
	CredentialID   string
	CredentialKind string
	ServiceAccount bool   // user-shaped machine principal (API-token auth); may never approve
	ServiceName    string // internal: app_name from internal_api_keys
	AccountID      int    // external: X-Account-Id
	APIKeyID       int    // external: X-API-Key-Id
	CustomerID     int    // external: X-Customer-Id
	CorrelationID  string // external: X-Correlation-Id
	ClientIP       string // external: X-Client-IP
	APIKey         string // external: raw key from Basic Auth (downstream forwarding)
	APISecret      string // external: raw secret from Basic Auth (downstream forwarding)
}

func (r *Requester) IsUser() bool    { return r.Kind == RequesterUser }
func (r *Requester) IsService() bool { return r.Kind == RequesterService }
