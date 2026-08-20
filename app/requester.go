package app

type RequesterKind int

const (
	RequesterUser RequesterKind = iota
	RequesterService
)

type Requester struct {
	Kind          RequesterKind
	ID            string
	ServiceName   string // internal: app_name from internal_api_keys
	AccountID     int    // external: X-Account-Id
	APIKeyID      int    // external: X-API-Key-Id
	CustomerID    int    // external: X-Customer-Id
	CorrelationID string // external: X-Correlation-Id
	ClientIP      string // external: X-Client-IP
	APIKey        string // external: raw key from Basic Auth (downstream forwarding)
	APISecret     string // external: raw secret from Basic Auth (downstream forwarding)
}

func (r *Requester) IsUser() bool    { return r.Kind == RequesterUser }
func (r *Requester) IsService() bool { return r.Kind == RequesterService }
