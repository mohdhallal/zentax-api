package audit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// AuditChainSuite proves the hash chain can be verified FROM THE STORED ROWS —
// the claim the whole tamper-evidence control rests on, and the one no
// in-memory test can make. Until the 2026-09-12 cut-over the writer stamped
// occurred_at at nanosecond precision and hashed it in that form while the
// column (TIMESTAMPTZ) keeps microseconds, so every entry written by a
// nanosecond-granular host — i.e. every Linux container the product runs in —
// was unverifiable the moment it committed. The bug survived because the
// assertions ran on a macOS host, whose clock happens to be microsecond-
// granular: the host, not the code, decided whether the suite could fail.
//
// So the clock is injected here, not borrowed from the host, and the entries
// travel through Postgres and back before anything is verified.
type AuditChainSuite struct {
	acceptance.Suite
}

func TestAuditChainSuite(t *testing.T) {
	suite.Run(t, new(AuditChainSuite))
}

// chainNanoClock is a clock that always carries nanoseconds the column cannot
// store, advancing by a millisecond per call.
func chainNanoClock(base time.Time) func() time.Time {
	calls := 0
	return func() time.Time {
		calls++
		return base.Add(time.Duration(calls)*time.Millisecond + time.Duration(100*calls+7)*time.Nanosecond)
	}
}

// chainActor seeds a user to attribute the entries to (audit records actors by
// id, never by name).
func (s *AuditChainSuite) chainActor(tenantID string) string {
	var userID string
	err := s.DB.QueryRowx(
		`INSERT INTO users (tenant_id, email, name, status) VALUES ($1, $2, 'Chain Actor', 'active') RETURNING id`,
		tenantID, "chain-"+uuid.NewString()+"@test.local").Scan(&userID)
	s.Require().NoError(err)
	return userID
}

// chainContext is the request context the Tx seam and the recorder read: the
// tenant (bound to the transaction's GUC, so RLS lets the append through) and
// the acting user.
func (s *AuditChainSuite) chainContext(tenantID, actorID string) context.Context {
	ctx := app.WithTenantID(s.T().Context(), tenantID)
	ctx = app.WithRequester(ctx, &app.Requester{Kind: app.RequesterUser, ID: actorID})
	return app.WithRequestId(ctx, "acc-chain-"+uuid.NewString())
}

// chainOf reads the tenant's whole chain back through the same loader an
// operator's verifier would use (hash_version included — omit it and every
// pre-cut-over row reads as tampering).
func (s *AuditChainSuite) chainOf(exec *database.Exec, ctx context.Context, tenantID string) []audit.Entry {
	var entries []audit.Entry
	s.Require().NoError(exec.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		entries, err = audit.LoadChain(ctx, exec, tenantID)
		return err
	}))
	return entries
}

// chainLegacyHash is the pre-cut-over canonical form (RFC3339Nano), kept here
// so the suite can write a row exactly as the old writer wrote it. Details must
// already be canonical — every row it builds below carries `{}`.
func chainLegacyHash(e *audit.Entry) string {
	fields := []string{
		e.PrevHash,
		e.EventID,
		e.TenantID,
		strconv.FormatInt(e.Seq, 10),
		e.ActorID,
		e.Action,
		e.ResourceType,
		e.ResourceID,
		e.OccurredAt.UTC().Format(time.RFC3339Nano),
		e.RequestID,
		string(e.Details),
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, "\n")))
	return hex.EncodeToString(sum[:])
}

// insertPreCutoverEntry appends one row the way the pre-cut-over writer did:
// the hash covers an instant carrying nanoseconds, the column stores the
// microsecond truncation of it, and hash_version records the old contract.
// Returns the stored hash, to link the next entry to.
func (s *AuditChainSuite) insertPreCutoverEntry(exec *database.Exec, ctx context.Context, tenantID, actorID, prevHash string, seq int64) string {
	stored := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC).Add(time.Duration(seq) * time.Millisecond)
	e := audit.Entry{
		EventID:      uuid.NewString(),
		TenantID:     tenantID,
		Seq:          seq,
		ActorID:      actorID,
		Action:       "entity.created",
		ResourceType: "entity",
		ResourceID:   uuid.NewString(),
		OccurredAt:   stored.Add(217 * time.Nanosecond), // what the old writer hashed
		RequestID:    "pre-cut-over",
		Details:      json.RawMessage(`{}`),
		PrevHash:     prevHash,
	}
	hash := chainLegacyHash(&e)

	s.Require().NoError(exec.WithinTransaction(ctx, func(ctx context.Context) error {
		_, err := exec.ExecContext(ctx, `
			INSERT INTO audit_log (event_id, tenant_id, seq, actor_id, action, resource_type, resource_id,
			                       occurred_at, request_id, details, prev_hash, hash, hash_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 1)`,
			e.EventID, e.TenantID, e.Seq, e.ActorID, e.Action, e.ResourceType, e.ResourceID,
			stored, e.RequestID, string(e.Details), e.PrevHash, hash)
		return err
	}))
	return hash
}

// The regression test proper: entries stamped by a nanosecond-granular clock,
// committed to Postgres, read back, and verified from the stored values.
func (s *AuditChainSuite) TestANanosecondClockStillLeavesAVerifiableChain() {
	tenant := s.InsertTenant("aud-chain", "Audit Chain").String()
	actor := s.chainActor(tenant)

	exec := database.NewExec(s.DB)
	rec := audit.NewRecorderWithClock(exec, chainNanoClock(time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)))
	ctx := s.chainContext(tenant, actor)

	resource := uuid.NewString()
	details := []map[string]any{
		{},
		{"fields": map[string]any{"status": map[string]any{"from": "active", "to": "inactive"}}},
		{"fields": map[string]any{"deadlineRule": map[string]any{"from": map[string]any{"offsetValue": 10}, "to": map[string]any{"offsetValue": 15}}}},
	}
	for _, d := range details {
		s.Require().NoError(exec.WithinTransaction(ctx, func(ctx context.Context) error {
			return rec.Record(ctx, "entity.updated", "entity", resource, d)
		}))
	}

	entries := s.chainOf(exec, ctx, tenant)
	s.Require().Len(entries, len(details))

	for _, e := range entries {
		s.Require().Equal(audit.CurrentHashVersion, e.HashVersion,
			"seq %d must be written under the current hashing contract", e.Seq)
		s.Require().Zero(e.OccurredAt.Sub(e.OccurredAt.Truncate(audit.ChainResolution)),
			"seq %d came back at a finer resolution than the column stores", e.Seq)
	}

	// The claim: nothing but the stored rows is needed to recompute the chain.
	// Before the fix this failed at seq 1 with "hash mismatch (entry
	// modified)" — the three nanosecond digits the hash covered were gone.
	s.Require().NoError(audit.VerifyChain(entries))

	rep, err := audit.Verify(entries)
	s.Require().NoError(err)
	s.Require().Equal(0, rep.PreCutoverEntries)
	s.Require().Equal(int64(1), rep.FirstVerifiableSeq)
	s.Require().Equal(int64(len(details)), rep.LastVerifiedSeq)

	// And tamper evidence still works on a row that IS verifiable: flip a
	// stored value in memory (the database refuses the UPDATE — see
	// TestTrailChainAttributionAndAppendOnly) and the chain must break there.
	tampered := append([]audit.Entry(nil), entries...)
	tampered[1].Action = "entity.deleted"
	s.Require().ErrorContains(audit.VerifyChain(tampered), "seq 2")
}

// The cut-over path: rows written before today cannot be recomputed, ever. The
// verifier must say so — naming the first sequence number it could verify —
// rather than reporting the tenant's history as tampered with.
func (s *AuditChainSuite) TestPreCutoverEntriesAreReportedNotCalledTampering() {
	tenant := s.InsertTenant("aud-cutover", "Audit Cut-over").String()
	actor := s.chainActor(tenant)

	exec := database.NewExec(s.DB)
	ctx := s.chainContext(tenant, actor)

	prev := audit.GenesisHash
	prev = s.insertPreCutoverEntry(exec, ctx, tenant, actor, prev, 1)
	prev = s.insertPreCutoverEntry(exec, ctx, tenant, actor, prev, 2)

	// The live writer appends after them, reading the chain head as always.
	rec := audit.NewRecorderWithClock(exec, chainNanoClock(time.Date(2026, 9, 12, 11, 0, 0, 0, time.UTC)))
	for i := 0; i < 2; i++ {
		s.Require().NoError(exec.WithinTransaction(ctx, func(ctx context.Context) error {
			return rec.Record(ctx, "entity.updated", "entity", uuid.NewString(), map[string]any{"n": i})
		}))
	}

	entries := s.chainOf(exec, ctx, tenant)
	s.Require().Len(entries, 4)
	s.Require().EqualValues(1, entries[0].HashVersion)
	s.Require().Equal(audit.CurrentHashVersion, entries[2].HashVersion)

	// Tolerant read: the links hold across the whole range, and verification
	// begins at seq 3. This is what an operator sees on a chain that predates
	// today.
	rep, err := audit.Verify(entries)
	s.Require().NoError(err, "a pre-cut-over prefix must not be reported as tampering")
	s.Require().Equal(2, rep.PreCutoverEntries)
	s.Require().Equal(int64(3), rep.FirstVerifiableSeq)
	s.Require().Equal(int64(4), rep.LastVerifiedSeq)
	s.Require().Contains(rep.String(), "hash-verified seq 3..4")

	// Strict read: the range as a whole does NOT verify, and the error says
	// from where it does.
	s.Require().ErrorContains(audit.VerifyChain(entries), "only from seq 3")

	// The marker excuses a prefix, never a row inside a live chain: a v1 row
	// appended after verifiable ones is an attempt to exempt a row from
	// recomputation, and breaks the chain at that seq.
	s.insertPreCutoverEntry(exec, ctx, tenant, actor, entries[3].Hash, 5)
	_, err = audit.Verify(s.chainOf(exec, ctx, tenant))
	s.Require().ErrorContains(err, "seq 5")
}
