package acceptance

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

func (s *Suite) TruncateTables() {
	var tables []string
	err := s.DB.Select(&tables, `
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = current_schema()
		ORDER BY tablename`)
	s.Require().NoError(err)
	if len(tables) == 0 {
		return
	}

	quoted := make([]string, 0, len(tables))
	for _, table := range tables {
		quoted = append(quoted, `"`+strings.ReplaceAll(table, `"`, `""`)+`"`)
	}

	_, err = s.DB.Exec(fmt.Sprintf(
		"TRUNCATE TABLE %s RESTART IDENTITY CASCADE",
		strings.Join(quoted, ", "),
	))
	s.Require().NoError(err)
}

// InsertTenant inserts a tenant into the (RLS-free) registry and returns its id,
// for use with WithTenant. Tenants are provisioned out of band (control-plane),
// so tests seed them directly rather than through the tenant-scoped API.
func (s *Suite) InsertTenant(slug, name string) uuid.UUID {
	var id uuid.UUID
	err := s.DB.QueryRowx(
		`INSERT INTO tenants (slug, name) VALUES ($1, $2) RETURNING id`,
		slug, name,
	).Scan(&id)
	s.Require().NoError(err)
	return id
}

// InsertInternalAPIKey inserts a hashed key and returns cleartext key+secret
// suitable for use with WithInternalAuth.
func (s *Suite) InsertInternalAPIKey(appName string) (key, secret string) {
	keyUUID := uuid.New()
	rawSecret := []byte("acceptance-test-secret-" + appName)
	secretClear := base64.RawURLEncoding.EncodeToString(rawSecret)
	hash := sha512.Sum512(rawSecret)
	secretHash := base64.RawURLEncoding.EncodeToString(hash[:])

	_, err := s.DB.Exec(
		`INSERT INTO internal_api_keys (key, app_name, secret_hash) VALUES ($1, $2, $3)`,
		keyUUID, appName, secretHash,
	)
	s.Require().NoError(err)

	return keyUUID.String(), secretClear
}

// As returns a request builder authenticated as a seeded user of the given
// tenant (session-cookie auth, ADR-0011). Sessions are memoized per tenant for
// the current test, replacing the old X-Tenant-ID header helper.
func (s *Suite) As(tenantID string) *RequestBuilder {
	if s.sessions == nil {
		s.sessions = map[string]string{}
	}
	token, ok := s.sessions[tenantID]
	if !ok {
		token = s.seedSession(tenantID)
		s.sessions[tenantID] = token
	}
	return s.Client.External().WithSession(token)
}

// seedSession inserts a user + a valid session for a tenant and returns the raw
// cookie token (stored hashed, as RequireSession expects).
func (s *Suite) seedSession(tenantID string) string {
	var userID string
	email := "user-" + uuid.NewString() + "@test.local"
	err := s.DB.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, status) VALUES ($1, $2, 'Test User', 'active') RETURNING id`,
		tenantID, email).Scan(&userID)
	s.Require().NoError(err)

	token := uuid.NewString() + uuid.NewString()
	_, err = s.DB.Exec(
		`INSERT INTO sessions (token_hash, user_id, tenant_id, idle_expires_at, absolute_expires_at)
		 VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour', NOW() + INTERVAL '30 days')`,
		crypto.HashToken(token), userID, tenantID)
	s.Require().NoError(err)
	return token
}

// InsertUserWithPassword seeds an active user with an argon2id password hash and
// returns its id, for exercising the login flow.
func (s *Suite) InsertUserWithPassword(tenantID uuid.UUID, email, password string) uuid.UUID {
	hash, err := crypto.HashPassword(password)
	s.Require().NoError(err)
	var id uuid.UUID
	err = s.DB.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, password_hash, status)
		 VALUES ($1, $2, 'Admin', $3, 'active') RETURNING id`,
		tenantID, strings.ToLower(email), hash).Scan(&id)
	s.Require().NoError(err)
	return id
}
