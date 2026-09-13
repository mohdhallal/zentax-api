// Package domain holds the notification producers' vocabulary: what the
// product owes a human, and the seams a producer needs to say it.
//
// A producer NEVER sends. It writes one row to the transactional outbox
// (platform/outbox) on the transaction that owes the message, and the scheduled
// delivery runner turns that row into mail later. Two consequences shape
// everything here:
//
//   - The payload is a SNAPSHOT. The runner renders from the row and never
//     re-reads live data, so whatever a recipient must see has to be in the
//     payload at enqueue time — not an id the renderer would have to resolve
//     against a tenant it is not acting for.
//   - The dedupe key is the idempotency contract. It is what makes a producer
//     safe to run twice, which for a scheduled job is not an optimisation but
//     the difference between one digest a day and one per process restart.
package domain

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mohamadhallal/zentax-api/platform/outbox"
)

// The template vocabulary of the outbox row's `template` column. These strings
// are the contract with the mail layer — one template renders each — so they
// live here as constants rather than as literals at each call site.
const (
	// TemplateMemberInvited carries InvitePayload: the link that activates an
	// invited member's account.
	TemplateMemberInvited = "member.invited"
	// TemplateDeadlineReminder carries DigestPayload: one person's tax tasks
	// that are overdue, due today, or due within the reminder lead time.
	TemplateDeadlineReminder = "deadline.reminder"
)

// Enqueuer is the producer half of the outbox, named here as the narrow thing
// this module needs. *outbox.Queue satisfies it as it stands — the interface
// exists so a producer can be unit-tested without a database, not to wrap
// anything.
//
// An implementation MUST write on the CALLER'S transaction and MUST NOT open
// one of its own: the whole guarantee is that the message lives or dies with
// the change that owes it.
type Enqueuer interface {
	Enqueue(ctx context.Context, m outbox.NewMessage) (outbox.Receipt, error)
}

// Payload freezes a typed payload into the map the outbox row stores.
//
// The producers here build TYPED payloads — a digest is a struct with counts
// and dates, not a hand-assembled map — because that is what makes the
// assembly testable and keeps a template's variables discoverable from Go. The
// row holds JSON, so the last step is this round-trip, which also guarantees
// that what the test asserts on is exactly what the renderer will read: any
// value that cannot survive JSON never reaches the queue silently.
func Payload(v any) (map[string]any, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("notifications: encode payload: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, fmt.Errorf("notifications: payload is not an object: %w", err)
	}
	return out, nil
}
