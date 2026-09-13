package notifications_test

import (
	"net/http"
	"strings"

	notificationsdomain "github.com/mohamadhallal/zentax-api/modules/notifications/domain"
	notificationsusecases "github.com/mohamadhallal/zentax-api/modules/notifications/usecases"
	"github.com/mohamadhallal/zentax-api/platform/outbox"
)

type inviteResponse struct {
	Member struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"member"`
	InviteToken     string `json:"inviteToken"`
	InviteExpiresAt string `json:"inviteExpiresAt"`
}

func (s *NotificationsSuite) invite(tenant, email, name string) inviteResponse {
	var out inviteResponse
	r := s.As(tenant).POST(s.T(), "/members", map[string]any{
		"email": email, "name": name, "role": "preparer",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &out)
	return out
}

// TestInviteQueuesExactlyOneMessageToTheInvitedAddress is the first half of the
// wave: an administrator invites somebody and the link goes to THEM, rather
// than being copied out of the API response by hand.
//
// It drives the real route, so what it proves includes the wiring: the invite
// use case, the outbox producer, the tenant GUC the row's tenant_id defaults
// from, and the RLS policy the read goes back through.
func (s *NotificationsSuite) TestInviteQueuesExactlyOneMessageToTheInvitedAddress() {
	tenant := s.InsertTenant("notif-invite", "Invite Tenant").String()

	invited := s.invite(tenant, "Jane.Doe@Acme.test", "Jane Doe")

	queued := s.outboxOf(tenant, notificationsdomain.TemplateMemberInvited)
	s.Require().Len(queued, 1, "one invite, one message")

	msg := queued[0]
	s.Require().Equal("jane.doe@acme.test", msg.Recipient,
		"addressed to the invited mailbox, lowercased as the account is")
	s.Require().Equal("email", msg.Channel)
	s.Require().Equal("pending", msg.Status, "queued, not sent: delivery is the runner's")
	s.Require().NotNil(msg.CreatedBy, "an invite has an acting administrator (ADR-0008)")

	// The dedupe key is the TOKEN's id, which is what makes the mail one per
	// issued credential rather than one per call.
	s.Require().NotNil(msg.DedupeKey)
	s.Require().True(strings.HasPrefix(*msg.DedupeKey, "invite:"), "dedupe key: %s", *msg.DedupeKey)

	// AT REST IT IS CIPHERTEXT. This is the read a database backup, a replica
	// or a support query gets: an envelope, with no credential and no name in
	// it. invite_tokens keeps only a SHA-256 so that a leaked table cannot be
	// replayed, and the queue must not hand that protection back.
	s.Require().True(outbox.IsSealed(msg.Payload),
		"the payload column must hold a sealed envelope: %s", string(msg.Payload))
	s.Require().NotContains(string(msg.Payload), "zti_")
	s.Require().NotContains(string(msg.Payload), "Jane Doe")

	// Opened with the application's key it is the snapshot the renderer builds
	// the link from, and nothing else.
	payload := s.payloadOf(msg)
	s.Require().Equal(invited.InviteToken, payload["inviteToken"],
		"the queued credential is the one the response returned — the same link")
	s.Require().Equal("Jane Doe", payload["recipientName"])
	s.Require().NotEmpty(payload["expiresAt"])

	// The token is STILL in the response. That is deliberate for now (the
	// seeding tool and the demo oracle read it from there); removing it is the
	// follow-up once delivery is proven in a deployment.
	s.Require().True(strings.HasPrefix(invited.InviteToken, "zti_"))
}

// TestInviteAndItsMessageCommitTogether is the transactional half. A rejected
// invite must leave nothing behind — no user, and no message promising a link
// for an account that does not exist.
func (s *NotificationsSuite) TestInviteAndItsMessageCommitTogether() {
	tenant := s.InsertTenant("notif-invite-tx", "Invite Tx Tenant").String()

	s.invite(tenant, "first@acme.test", "First")
	s.Require().Len(s.outboxOf(tenant, notificationsdomain.TemplateMemberInvited), 1)

	// The same address again: the users row conflicts, the whole request rolls
	// back, and the outbox is untouched.
	s.As(tenant).POST(s.T(), "/members", map[string]any{
		"email": "first@acme.test", "name": "First Again", "role": "preparer",
	}).AssertStatus(s.T(), http.StatusConflict)

	s.Require().Len(s.outboxOf(tenant, notificationsdomain.TemplateMemberInvited), 1,
		"a refused invite queues nothing")
}

// TestReissuingAnInviteSendsTheNewLink: "resend the invite" is a new credential,
// so it is a new message — the dedupe key is the token, not the member.
func (s *NotificationsSuite) TestReissuingAnInviteSendsTheNewLink() {
	tenant := s.InsertTenant("notif-invite-reissue", "Reissue Tenant").String()

	first := s.invite(tenant, "reissue@acme.test", "Re Issue")

	var second inviteResponse
	r := s.As(tenant).POST(s.T(), "/members/"+first.Member.ID+"/invite", nil)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &second)
	s.Require().NotEqual(first.InviteToken, second.InviteToken)

	queued := s.outboxOf(tenant, notificationsdomain.TemplateMemberInvited)
	s.Require().Len(queued, 2, "a re-issued invite is a second message, not a suppressed duplicate")
	s.Require().NotEqual(*queued[0].DedupeKey, *queued[1].DedupeKey)

	tokens := []any{s.payloadOf(queued[0])["inviteToken"], s.payloadOf(queued[1])["inviteToken"]}
	s.Require().ElementsMatch([]any{first.InviteToken, second.InviteToken}, tokens,
		"each message carries its own token")
}

// TestInviteMessagesStayInsideTheirTenant: two tenants invite, and neither can
// see the other's queued mail — the row's tenant comes from the request's GUC,
// and the policy is what a reader goes back through.
func (s *NotificationsSuite) TestInviteMessagesStayInsideTheirTenant() {
	tenantA := s.InsertTenant("notif-iso-a", "Iso A").String()
	tenantB := s.InsertTenant("notif-iso-b", "Iso B").String()

	s.invite(tenantA, "a@acme.test", "A Person")
	s.invite(tenantB, "b@acme.test", "B Person")

	forA := s.outboxOf(tenantA, notificationsdomain.TemplateMemberInvited)
	forB := s.outboxOf(tenantB, notificationsdomain.TemplateMemberInvited)
	s.Require().Len(forA, 1)
	s.Require().Len(forB, 1)
	s.Require().Equal("a@acme.test", forA[0].Recipient)
	s.Require().Equal("b@acme.test", forB[0].Recipient)
	s.Require().Equal(tenantA, forA[0].TenantID)
	s.Require().Equal(tenantB, forB[0].TenantID)
}

// TestInviteDedupeKeyNamesTheToken pins the key's shape, because it is a
// contract two things depend on: re-running a producer, and an operator
// tracing a message back to the credential it carries.
func (s *NotificationsSuite) TestInviteDedupeKeyNamesTheToken() {
	tenant := s.InsertTenant("notif-invite-key", "Key Tenant").String()
	s.invite(tenant, "key@acme.test", "Key Person")

	queued := s.outboxOf(tenant, notificationsdomain.TemplateMemberInvited)
	s.Require().Len(queued, 1)

	var tokenID string
	s.Require().NoError(s.DB.Get(&tokenID,
		`SELECT it.id FROM invite_tokens it
		 JOIN users u ON u.id = it.user_id
		 WHERE u.email = $1`, "key@acme.test"))

	s.Require().Equal(notificationsusecases.InviteDedupeKey(tokenID), *queued[0].DedupeKey)
}
