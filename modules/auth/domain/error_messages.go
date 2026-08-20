package domain

import "fmt"

const (
	ErrMsgInvalidCredentials = "invalid credentials" //nolint:gosec // error message, not a credential
	ErrMsgInvalidKeyFormat   = "invalid key format"
	ErrMsgCredentialInactive = "credential is inactive" //nolint:gosec // error message, not a credential
)

func ErrAPIKeyNotFound(nexusAccountID int) string {
	return fmt.Sprintf("api key not found for nexus account: %d", nexusAccountID)
}
