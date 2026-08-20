package acceptance

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/uuid"
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
