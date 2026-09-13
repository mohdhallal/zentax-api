package worm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// The archive is the copy an auditor is handed, so the credential that made a
// change has to survive the file — and the PUBLISHED RECIPE has to be enough to
// recompute the hash of an entry that carries one. A reader with only the file
// must reach the same answer as platform/audit, or an auditor reimplementing
// the check would conclude the evidence was forged.
func TestASegmentCarriesTheCredentialAndThePublishedRecipeReproducesItsHash(t *testing.T) {
	tenantID := uuid.NewString()
	sessionID := uuid.NewString()
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	e := audit.Entry{
		EventID:        uuid.NewString(),
		TenantID:       tenantID,
		Seq:            1,
		ActorID:        uuid.NewString(),
		Action:         "entity.updated",
		ResourceType:   "entity",
		ResourceID:     uuid.NewString(),
		OccurredAt:     at.Truncate(audit.ChainResolution),
		RequestID:      uuid.NewString(),
		CredentialID:   sessionID,
		CredentialKind: app.CredentialSession,
		Details:        json.RawMessage(`{"fields":{"status":{"from":"active","to":"inactive"}}}`),
		PrevHash:       audit.GenesisHash,
		HashVersion:    audit.HashVersionCredential,
	}
	e.Hash = audit.ComputeHash(&e)

	built, err := Build(BuildInput{
		SegmentID:     uuid.NewString(),
		SegmentSeq:    1,
		TenantID:      tenantID,
		ExportedAt:    at.Add(time.Hour),
		StartPrevHash: audit.GenesisHash,
		Entries:       []audit.Entry{e},
	})
	require.NoError(t, err)

	carried := built.Document.Entries[0]
	require.Equal(t, sessionID, carried.CredentialID, "the archive must name the credential, not only the person")
	require.Equal(t, app.CredentialSession, carried.CredentialKind)

	// The recipe, as a third party would read it: take the field list for the
	// entry's OWN hashVersion, join with "\n", sha256, hex.
	fields := built.Document.Verification.EntryHashFieldsByVersion[strconv.FormatInt(int64(carried.HashVersion), 10)]
	require.Equal(t,
		[]string{"prevHash", "eventId", "tenantId", "seq", "actorId", "action",
			"resourceType", "resourceId", "occurredAt", "requestId", "details",
			"credentialKind", "credentialId"},
		fields, "the published field order for version 3")

	sum := sha256.Sum256([]byte(hashInput(carried)))
	require.Equal(t, carried.Hash, hex.EncodeToString(sum[:]),
		"the published recipe must reproduce the stored hash of an entry that names a credential")

	// And the file verifies as a whole, which is what the exporter refuses to
	// upload without.
	rep, err := VerifyFile(built.Bytes)
	require.NoError(t, err)
	require.Equal(t, int64(1), rep.LastVerifiedSeq)
}

// A file may span the cut-over, and the older entries in it must still be
// RECOMPUTED — under their own envelope — rather than demoted to link-checked.
// A version bump that quietly excused every earlier entry would erase the
// product's tamper evidence one constant at a time.
func TestASegmentSpanningTheCredentialCutoverVerifiesBothSides(t *testing.T) {
	tenantID := uuid.NewString()
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	older := audit.Entry{
		EventID: uuid.NewString(), TenantID: tenantID, Seq: 1,
		ActorID: uuid.NewString(), Action: "entity.created", ResourceType: "entity",
		ResourceID: uuid.NewString(), OccurredAt: at.Truncate(audit.ChainResolution),
		RequestID: uuid.NewString(), Details: json.RawMessage(`{}`),
		PrevHash: audit.GenesisHash, HashVersion: audit.HashVersionMicrosecond,
	}
	older.Hash = audit.ComputeHash(&older)

	newer := audit.Entry{
		EventID: uuid.NewString(), TenantID: tenantID, Seq: 2,
		ActorID: uuid.NewString(), Action: "entity.updated", ResourceType: "entity",
		ResourceID: uuid.NewString(), OccurredAt: at.Add(time.Second).Truncate(audit.ChainResolution),
		RequestID: uuid.NewString(), CredentialID: uuid.NewString(), CredentialKind: app.CredentialAPIToken,
		Details: json.RawMessage(`{}`), PrevHash: older.Hash, HashVersion: audit.HashVersionCredential,
	}
	newer.Hash = audit.ComputeHash(&newer)

	built, err := Build(BuildInput{
		SegmentID:     uuid.NewString(),
		SegmentSeq:    1,
		TenantID:      tenantID,
		ExportedAt:    at.Add(time.Hour),
		StartPrevHash: audit.GenesisHash,
		Entries:       []audit.Entry{older, newer},
	})
	require.NoError(t, err)

	rep, err := VerifyFile(built.Bytes)
	require.NoError(t, err)
	require.Zero(t, rep.PreCutoverEntries, "a version-2 entry is recomputable and must be recomputed")
	require.Equal(t, int64(1), rep.FirstVerifiedSeq)
	require.Equal(t, int64(2), rep.LastVerifiedSeq)

	// Re-attributing the newer entry to another credential breaks the file.
	var doc Document
	require.NoError(t, json.Unmarshal(mustSegment(t, built.Bytes), &doc))
	doc.Entries[1].CredentialID = uuid.NewString()
	_, err = VerifyDocument(doc)
	require.Error(t, err, "an entry moved to another credential must read as tampering")
}

func mustSegment(t *testing.T, fileBytes []byte) json.RawMessage {
	t.Helper()
	var f File
	require.NoError(t, json.Unmarshal(fileBytes, &f))
	return f.Segment
}
