// Package sesmail is the cloud adapter of platform/mail: Amazon SES, the
// provider the deployment record names (ADR-0009, "Email — SES / SMTP"; the
// SaaS cells of ADR-0024 run inside the same account).
//
// It speaks the SES v2 REST API — POST /v2/email/outbound-emails — over plain
// net/http, signed with the SDK's SigV4 signer and the SDK's default
// credential chain (task role / IRSA / env / shared config). That is a
// deliberate choice over pulling in the generated service client: the whole
// surface used here is one request and one response, the account already
// depends on the SDK core and its signer for object storage, and the seam this
// package sits behind must not drag a second megabyte of generated code into
// every binary that can send an e-mail.
//
// Nothing in it touches the network unless a caller sends: the HTTP transport
// and the credential provider are both injectable, so the tests exercise
// signing, request shape and every failure classification against a fake — an
// acceptance run stays offline by construction, not by convention.
package sesmail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	"github.com/mohamadhallal/zentax-api/platform/mail"
)

// DefaultTimeout bounds one SendEmail call when the context carries no
// deadline.
const DefaultTimeout = 15 * time.Second

// service is the SigV4 service name for SES (both v1 and v2 sign as "ses").
const service = "ses"

// sendPath is the SES v2 SendEmail resource.
const sendPath = "/v2/email/outbound-emails"

// Doer is the part of *http.Client this adapter uses. A test supplies a fake,
// which is what keeps an acceptance run off the network.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Options configures the adapter.
type Options struct {
	// Region is required: it selects the endpoint AND the signing scope, and
	// under ADR-0005 it is the region the tenant's data may be processed in.
	Region string
	// ConfigurationSet attaches an SES configuration set, which is how bounce
	// and complaint events get delivered to the account's event destination.
	ConfigurationSet string
	// Endpoint overrides the derived endpoint (a VPC endpoint, or a test
	// double). Empty means https://email.<region>.amazonaws.com.
	Endpoint string
	// Timeout bounds one call when the caller's context has no deadline.
	Timeout time.Duration

	// Credentials overrides the SDK default chain. Tests use it; a deployment
	// leaves it nil so the task role is picked up.
	Credentials aws.CredentialsProvider
	// HTTPClient overrides the transport. Tests use it.
	HTTPClient Doer
}

// Sender delivers through Amazon SES.
type Sender struct {
	region    string
	endpoint  string
	configSet string
	timeout   time.Duration
	creds     aws.CredentialsProvider
	signer    *v4.Signer
	http      Doer
	now       func() time.Time
}

var _ mail.Sender = (*Sender)(nil)

// New builds the adapter. It fails closed on a missing region and, unless the
// caller supplied credentials, resolves the SDK default chain once at startup
// so a broken credential configuration is a boot failure rather than a
// notification that silently never arrives.
func New(ctx context.Context, opts Options) (*Sender, error) {
	region := strings.TrimSpace(opts.Region)
	if region == "" {
		return nil, errors.New("ses mail: region is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}

	creds := opts.Credentials
	if creds == nil {
		cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			return nil, fmt.Errorf("ses mail: load aws config: %w", err)
		}
		creds = cfg.Credentials
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: opts.Timeout}
	}

	endpoint := strings.TrimRight(strings.TrimSpace(opts.Endpoint), "/")
	if endpoint == "" {
		endpoint = "https://email." + region + ".amazonaws.com"
	}

	return &Sender{
		region:    region,
		endpoint:  endpoint,
		configSet: strings.TrimSpace(opts.ConfigurationSet),
		timeout:   opts.Timeout,
		creds:     creds,
		signer:    v4.NewSigner(),
		http:      client,
		now:       time.Now,
	}, nil
}

// The SES v2 SendEmail request, as much of it as this seam needs. Simple
// content (subject + text + html) rather than a raw MIME part: SES assembles
// the multipart body, so the adapter carries no MIME code and the two
// alternatives cannot drift apart in transit.
type sendEmailInput struct {
	FromEmailAddress     string      `json:"FromEmailAddress"`
	Destination          destination `json:"Destination"`
	ReplyToAddresses     []string    `json:"ReplyToAddresses,omitempty"`
	Content              content     `json:"Content"`
	ConfigurationSetName string      `json:"ConfigurationSetName,omitempty"`
}

type destination struct {
	ToAddresses []string `json:"ToAddresses"`
}

type content struct {
	Simple simple `json:"Simple"`
}

type simple struct {
	Subject body  `json:"Subject"`
	Body    parts `json:"Body"`
}

type parts struct {
	Text *body `json:"Text,omitempty"`
	HTML *body `json:"Html,omitempty"`
}

type body struct {
	Data    string `json:"Data"`
	Charset string `json:"Charset"`
}

const utf8Charset = "UTF-8"

// Send delivers one message.
func (s *Sender) Send(ctx context.Context, msg mail.Message) error {
	if err := msg.Validate(); err != nil {
		return mail.Permanent("ses: message", "", err)
	}

	input := sendEmailInput{
		FromEmailAddress:     msg.From.String(),
		Destination:          destination{ToAddresses: []string{msg.To.String()}},
		Content:              content{Simple: simple{Subject: body{Data: msg.Subject, Charset: utf8Charset}}},
		ConfigurationSetName: s.configSet,
	}
	input.Content.Simple.Body.Text = &body{Data: msg.Text, Charset: utf8Charset}
	if msg.HTML != "" {
		input.Content.Simple.Body.HTML = &body{Data: msg.HTML, Charset: utf8Charset}
	}
	if !msg.ReplyTo.IsZero() {
		input.ReplyToAddresses = []string{msg.ReplyTo.String()}
	}

	payload, err := json.Marshal(input)
	if err != nil {
		return mail.Permanent("ses: encode", "", err)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint+sendPath, bytes.NewReader(payload))
	if err != nil {
		return mail.Permanent("ses: request", "", err)
	}
	req.Header.Set("Content-Type", "application/json")

	creds, err := s.creds.Retrieve(ctx)
	if err != nil {
		// A credential chain that cannot answer is usually an instance-metadata
		// blip or an expired role, both of which pass.
		return mail.Retryable("ses: credentials", "", err)
	}
	sum := sha256.Sum256(payload)
	if err := s.signer.SignHTTP(ctx, creds, req, hex.EncodeToString(sum[:]), service, s.region, s.now().UTC()); err != nil {
		return mail.Retryable("ses: sign", "", err)
	}

	resp, err := s.http.Do(req)
	if err != nil {
		return mail.Retryable("ses: send-email", "", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return classify("ses: send-email", resp)
}

// maxErrorBody bounds what is read from a failed response. The body is a
// provider error document, not ours, and it ends up inside an error string.
const maxErrorBody = 8 << 10

// classify turns an SES error response into the verdict the outbox acts on.
//
// The shape of the rule: 5xx and 429 are the service saying "not now"; every
// other 4xx is the service saying "not this message" — an unverified sender, a
// rejected body, a configuration set that does not exist — and no number of
// retries changes any of them. The named exceptions below are the 4xx codes
// that are nonetheless about capacity rather than about the message.
func classify(op string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

	errType := errorType(resp, raw)
	code := errType
	if code == "" {
		code = strconv.Itoa(resp.StatusCode)
	}
	detail := errors.New(strings.TrimSpace(errorMessage(raw)))

	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || retryableTypes[errType] {
		return mail.Retryable(op, code, detail)
	}
	return mail.Permanent(op, code, detail)
}

// retryableTypes are the 4xx SES error types that describe capacity, not the
// message. SendingPaused is here on purpose: an account-level pause is lifted
// by a human, and the outbox's bounded attempts are a cheaper way to survive
// that than discarding every notification raised while it lasted.
var retryableTypes = map[string]bool{
	"ThrottlingException":         true,
	"TooManyRequestsException":    true,
	"Throttling":                  true,
	"RequestThrottled":            true,
	"RequestThrottledException":   true,
	"SlowDown":                    true,
	"RequestTimeout":              true,
	"RequestTimeoutException":     true,
	"ServiceUnavailable":          true,
	"ServiceUnavailableException": true,
	"InternalServiceError":        true,
	"InternalFailure":             true,
	"SendingPausedException":      true,
	"LimitExceededException":      true,
}

// errorType reads the exception name from the header AWS puts it in, falling
// back to the "__type" field of the error document. Both arrive decorated
// ("MessageRejected:http://internal.amazon.com/…", "com.amazonaws#Throttling"),
// so both are trimmed to the bare name.
func errorType(resp *http.Response, rawBody []byte) string {
	name := resp.Header.Get("x-amzn-errortype")
	if name == "" {
		var doc struct {
			Type string `json:"__type"`
		}
		if err := json.Unmarshal(rawBody, &doc); err == nil {
			name = doc.Type
		}
	}
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "#"); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSpace(name)
}

// errorMessage pulls the human-readable part out of the error document. SES
// answers with "message" or "Message" depending on the error.
func errorMessage(rawBody []byte) string {
	var doc struct {
		Message      string `json:"message"`
		MessageUpper string `json:"Message"`
	}
	if err := json.Unmarshal(rawBody, &doc); err == nil {
		if doc.Message != "" {
			return doc.Message
		}
		if doc.MessageUpper != "" {
			return doc.MessageUpper
		}
	}
	return string(bytes.TrimSpace(rawBody))
}
