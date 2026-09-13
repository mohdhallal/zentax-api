package notifications_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
	"github.com/mohamadhallal/zentax-api/platform/outbox"
)

// NotificationsSuite proves the two producers end to end against live
// Postgres: an invite queues its own delivery on the invite's transaction, and
// the daily reminder scan queues one digest per person per tenant-local day.
//
// Both assert on outbox_messages, which is RLS-isolated and FORCEd, and the
// suite connects as the non-BYPASSRLS app role — so every read here goes
// through withTenant, exactly as the application does. A test that could see
// the table without binding a tenant would be a test proving nothing about
// isolation.
type NotificationsSuite struct {
	acceptance.Suite
}

func TestNotificationsSuite(t *testing.T) {
	suite.Run(t, new(NotificationsSuite))
}

// queuedMessage is one outbox row as these tests read it.
type queuedMessage struct {
	ID        string          `db:"id"`
	TenantID  string          `db:"tenant_id"`
	Channel   string          `db:"channel"`
	Template  string          `db:"template"`
	Recipient string          `db:"recipient"`
	Payload   json.RawMessage `db:"payload"`
	DedupeKey *string         `db:"dedupe_key"`
	Status    string          `db:"status"`
	DueAt     string          `db:"due_at"`
	CreatedBy *string         `db:"created_by"`
}

// withTenant runs fn inside a transaction bound to the tenant, mirroring the
// application's Tx seam (platform/database.Exec.WithinTransaction): RLS reads
// app.tenant_id from a SET LOCAL, so without one the policies fail closed.
func (s *NotificationsSuite) withTenant(tenantID string, fn func(tx *sqlx.Tx)) {
	tx := s.DB.MustBegin()
	defer func() { _ = tx.Rollback() }()
	_, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	fn(tx)
}

// outboxFor returns a tenant's queued messages, oldest first.
func (s *NotificationsSuite) outboxFor(tenantID string) []queuedMessage {
	var rows []queuedMessage
	s.withTenant(tenantID, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&rows, `
			SELECT id, tenant_id, channel, template, recipient, payload, dedupe_key,
			       status, due_at::text AS due_at, created_by
			FROM outbox_messages
			ORDER BY created_at, id`))
	})
	return rows
}

// outboxOf returns a tenant's queued messages for one template.
func (s *NotificationsSuite) outboxOf(tenantID, template string) []queuedMessage {
	var out []queuedMessage
	for _, row := range s.outboxFor(tenantID) {
		if row.Template == template {
			out = append(out, row)
		}
	}
	return out
}

// testEncryptionKey is the key the harness boots the application with
// (acceptance/config.Tester's Auth.EncryptionKey, base64-decoded). A payload
// queued through the real HTTP route is SEALED with it, so a test that wants to
// read one has to open it — deliberately, because that is the property under
// test: what sits in the column is ciphertext.
func (s *NotificationsSuite) testEncryptionKey() []byte {
	key, err := base64.StdEncoding.DecodeString("emVudGF4LWRldi1lbmNyeXB0aW9uLWtleS0zMmJ5dGU=")
	s.Require().NoError(err)
	return key
}

// payloadOf decodes a queued message's frozen variable set, opening the seal
// when there is one. A producer a test drives directly (the reminder job below
// builds its own queue) may write cleartext; anything queued through the
// running application is sealed.
func (s *NotificationsSuite) payloadOf(m queuedMessage) map[string]any {
	raw := m.Payload
	if outbox.IsSealed(raw) {
		seal, err := outbox.NewSeal(s.testEncryptionKey())
		s.Require().NoError(err)
		opened, err := seal.Open(raw)
		s.Require().NoError(err)
		raw = json.RawMessage(opened)
	}
	var payload map[string]any
	s.Require().NoError(json.Unmarshal(raw, &payload))
	return payload
}
