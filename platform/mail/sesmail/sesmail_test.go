package sesmail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/platform/mail"
)

// The whole suite runs against a fake transport and static credentials: no
// call in this file can reach AWS, which is the property an acceptance run
// depends on.

type fakeHTTP struct {
	requests []*http.Request
	bodies   [][]byte

	status  int
	header  http.Header
	body    string
	failure error
}

func (f *fakeHTTP) Do(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		f.bodies = append(f.bodies, raw)
	}
	f.requests = append(f.requests, req)
	if f.failure != nil {
		return nil, f.failure
	}
	header := f.header
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: f.status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(f.body)),
	}, nil
}

func staticCredentials() aws.CredentialsProvider {
	return aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "secret", Source: "test"}, nil
	})
}

func senderWith(t *testing.T, transport Doer, configure func(*Options)) *Sender {
	t.Helper()
	opts := Options{Region: "eu-central-1", Credentials: staticCredentials(), HTTPClient: transport}
	if configure != nil {
		configure(&opts)
	}
	s, err := New(context.Background(), opts)
	require.NoError(t, err)
	s.now = func() time.Time { return time.Date(2026, 9, 13, 8, 30, 0, 0, time.UTC) }
	return s
}

func message() mail.Message {
	return mail.Message{
		Kind:    "deadline.reminder",
		From:    mail.Address{Name: "ZenTax", Email: "no-reply@zentax.software"},
		To:      mail.Address{Name: "Jane Doe", Email: "jane@acme.test"},
		ReplyTo: mail.Address{Email: "support@zentax.software"},
		Subject: "ZenTax deadlines for 2026-09-13",
		Text:    "Your VAT return is due on 2026-09-30.\n",
		HTML:    "<p>Your VAT return is due on 2026-09-30.</p>\n",
	}
}

func ok() *fakeHTTP {
	return &fakeHTTP{status: http.StatusOK, body: `{"MessageId":"0100018f-fake"}`}
}

// --- construction -----------------------------------------------------------

func TestNew_RequiresARegionAndDerivesTheEndpoint(t *testing.T) {
	t.Parallel()

	_, err := New(context.Background(), Options{Credentials: staticCredentials(), HTTPClient: ok()})
	require.Error(t, err)

	s := senderWith(t, ok(), nil)
	assert.Equal(t, "https://email.eu-central-1.amazonaws.com", s.endpoint)
	assert.Equal(t, DefaultTimeout, s.timeout)

	custom := senderWith(t, ok(), func(o *Options) { o.Endpoint = "https://vpce-1234.email.eu-central-1.vpce.amazonaws.com/" })
	assert.Equal(t, "https://vpce-1234.email.eu-central-1.vpce.amazonaws.com", custom.endpoint)
}

// --- the request ------------------------------------------------------------

func TestSend_BuildsAndSignsTheSendEmailRequest(t *testing.T) {
	t.Parallel()

	transport := ok()
	sender := senderWith(t, transport, func(o *Options) { o.ConfigurationSet = "zentax-staging" })

	require.NoError(t, sender.Send(context.Background(), message()))
	require.Len(t, transport.requests, 1)

	req := transport.requests[0]
	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "https://email.eu-central-1.amazonaws.com/v2/email/outbound-emails", req.URL.String())
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))

	// SigV4: the credential scope names this service and this region, and the
	// payload is covered by its own hash.
	authorization := req.Header.Get("Authorization")
	assert.Contains(t, authorization, "AWS4-HMAC-SHA256")
	assert.Contains(t, authorization, "Credential=AKIAEXAMPLE/20260913/eu-central-1/ses/aws4_request")
	assert.Contains(t, authorization, "Signature=")
	assert.Equal(t, "20260913T083000Z", req.Header.Get("X-Amz-Date"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(transport.bodies[0], &body))
	assert.Equal(t, `"ZenTax" <no-reply@zentax.software>`, body["FromEmailAddress"])
	assert.Equal(t, "zentax-staging", body["ConfigurationSetName"])
	assert.Equal(t, []any{`"Jane Doe" <jane@acme.test>`}, body["Destination"].(map[string]any)["ToAddresses"])
	assert.Equal(t, []any{"support@zentax.software"}, body["ReplyToAddresses"])

	simple := body["Content"].(map[string]any)["Simple"].(map[string]any)
	assert.Equal(t, "ZenTax deadlines for 2026-09-13", simple["Subject"].(map[string]any)["Data"])
	assert.Equal(t, "UTF-8", simple["Subject"].(map[string]any)["Charset"])

	parts := simple["Body"].(map[string]any)
	assert.Contains(t, parts["Text"].(map[string]any)["Data"], "due on 2026-09-30")
	assert.Contains(t, parts["Html"].(map[string]any)["Data"], "<p>", "the HTML alternative is sent as HTML")
}

func TestSend_OmitsWhatIsNotSet(t *testing.T) {
	t.Parallel()

	transport := ok()
	sender := senderWith(t, transport, nil)

	m := message()
	m.HTML = ""
	m.ReplyTo = mail.Address{}
	require.NoError(t, sender.Send(context.Background(), m))

	var body map[string]any
	require.NoError(t, json.Unmarshal(transport.bodies[0], &body))
	assert.NotContains(t, body, "ReplyToAddresses")
	assert.NotContains(t, body, "ConfigurationSetName")

	parts := body["Content"].(map[string]any)["Simple"].(map[string]any)["Body"].(map[string]any)
	assert.Contains(t, parts, "Text")
	assert.NotContains(t, parts, "Html", "a text-only message must not claim an empty HTML alternative")
}

func TestSend_RejectsAMalformedMessageWithoutCallingTheProvider(t *testing.T) {
	t.Parallel()

	transport := ok()
	sender := senderWith(t, transport, nil)

	m := message()
	m.Subject = "Reminder\r\nBcc: attacker@evil.test"
	err := sender.Send(context.Background(), m)

	require.Error(t, err)
	assert.True(t, mail.IsPermanent(err))
	assert.Empty(t, transport.requests, "a message that cannot be sent is not sent")
}

// --- classification ---------------------------------------------------------

func errorResponse(status int, errType, message string) *fakeHTTP {
	header := http.Header{}
	if errType != "" {
		header.Set("x-amzn-errortype", errType)
	}
	return &fakeHTTP{status: status, header: header, body: `{"message":"` + message + `"}`}
}

func TestSend_ClassifiesProviderFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		transport *fakeHTTP
		permanent bool
		code      string
	}{
		{
			name:      "a rejected message is permanent",
			transport: errorResponse(http.StatusBadRequest, "MessageRejected:http://internal.amazon.com/coral/", "Email address is not verified"),
			permanent: true,
			code:      "MessageRejected",
		},
		{
			name:      "an unverified sending domain is permanent",
			transport: errorResponse(http.StatusBadRequest, "MailFromDomainNotVerifiedException", "domain not verified"),
			permanent: true,
			code:      "MailFromDomainNotVerifiedException",
		},
		{
			name:      "a missing configuration set is permanent",
			transport: errorResponse(http.StatusNotFound, "NotFoundException", "configuration set does not exist"),
			permanent: true,
			code:      "NotFoundException",
		},
		{
			name:      "a throttle is temporary",
			transport: errorResponse(http.StatusBadRequest, "ThrottlingException", "Maximum sending rate exceeded"),
			permanent: false,
			code:      "ThrottlingException",
		},
		{
			name:      "too many requests is temporary",
			transport: errorResponse(http.StatusTooManyRequests, "", "slow down"),
			permanent: false,
			code:      "429",
		},
		{
			name:      "a service error is temporary",
			transport: errorResponse(http.StatusInternalServerError, "InternalServiceError", "we broke it"),
			permanent: false,
			code:      "InternalServiceError",
		},
		{
			name:      "an account-level pause is temporary",
			transport: errorResponse(http.StatusBadRequest, "SendingPausedException", "account sending is paused"),
			permanent: false,
			code:      "SendingPausedException",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := senderWith(t, tc.transport, nil).Send(context.Background(), message())
			require.Error(t, err)
			assert.Equal(t, tc.permanent, mail.IsPermanent(err))
			assert.Equal(t, !tc.permanent, mail.IsRetryable(err))
			assert.Contains(t, err.Error(), tc.code)
		})
	}
}

func TestSend_ReadsTheErrorTypeFromTheBodyWhenTheHeaderIsAbsent(t *testing.T) {
	t.Parallel()

	transport := &fakeHTTP{
		status: http.StatusBadRequest,
		body:   `{"__type":"com.amazonaws.sesv2#MessageRejected","Message":"Email address is not verified"}`,
	}
	err := senderWith(t, transport, nil).Send(context.Background(), message())
	require.Error(t, err)
	assert.True(t, mail.IsPermanent(err))
	assert.Contains(t, err.Error(), "MessageRejected")
	assert.Contains(t, err.Error(), "not verified")
}

func TestSend_TransportFailureIsRetryable(t *testing.T) {
	t.Parallel()

	transport := &fakeHTTP{failure: errors.New("dial tcp: connection reset by peer")}
	err := senderWith(t, transport, nil).Send(context.Background(), message())
	require.Error(t, err)
	assert.True(t, mail.IsRetryable(err))
}

func TestSend_CredentialFailureIsRetryable(t *testing.T) {
	t.Parallel()

	transport := ok()
	sender := senderWith(t, transport, func(o *Options) {
		o.Credentials = aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{}, errors.New("no EC2 IMDS role found")
		})
	})

	err := sender.Send(context.Background(), message())
	require.Error(t, err)
	assert.True(t, mail.IsRetryable(err), "an expired role or an IMDS blip passes")
	assert.Empty(t, transport.requests)
}

func TestSend_AcceptsAnySuccessStatus(t *testing.T) {
	t.Parallel()
	require.NoError(t, senderWith(t, &fakeHTTP{status: http.StatusOK, body: `{"MessageId":"x"}`}, nil).Send(context.Background(), message()))
}

func TestSender_SatisfiesTheSeam(t *testing.T) {
	t.Parallel()
	var seam mail.Sender = senderWith(t, ok(), nil)
	assert.NotNil(t, seam)
}
