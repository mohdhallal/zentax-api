package config

import (
	"errors"
	"fmt"
	netmail "net/mail"
	"os"
	"strconv"
	"strings"
	"time"
)

// Outbound mail (ADR-0009's "Email — SES / SMTP" adapter, platform/mail).
//
// One setting decides which adapter the composition root builds, and the rest
// describe the identity every message carries and the transport it leaves by.
// As everywhere else in this package an EMPTY environment value never overrides
// the file — a deployment manifest routinely passes "" for the driver it is not
// using.
const (
	EnvMailDriver      = "MAIL_DRIVER"       // log | smtp | ses
	EnvMailFromAddress = "MAIL_FROM_ADDRESS" // the envelope sender, e.g. no-reply@zentax.software
	EnvMailFromName    = "MAIL_FROM_NAME"    // display name; defaults to the product name
	EnvMailReplyTo     = "MAIL_REPLY_TO"     // optional: where a human reply goes
	EnvMailProductName = "MAIL_PRODUCT_NAME" // what the mail calls itself

	EnvMailSMTPHost          = "MAIL_SMTP_HOST"
	EnvMailSMTPPort          = "MAIL_SMTP_PORT"
	EnvMailSMTPUsername      = "MAIL_SMTP_USERNAME"
	EnvMailSMTPPassword      = "MAIL_SMTP_PASSWORD" //nolint:gosec // G101: the NAME of an environment variable, not a credential
	EnvMailSMTPAuth          = "MAIL_SMTP_AUTH"     // none | plain | login | crammd5
	EnvMailSMTPTLS           = "MAIL_SMTP_TLS"      // starttls | implicit | none
	EnvMailSMTPAllowInsecure = "MAIL_SMTP_ALLOW_INSECURE"
	EnvMailSMTPSkipTLSVerify = "MAIL_SMTP_SKIP_TLS_VERIFY"
	EnvMailSMTPCAFile        = "MAIL_SMTP_CA_FILE"
	EnvMailSMTPTimeoutMs     = "MAIL_SMTP_TIMEOUT_MS"
	EnvMailSMTPLocalName     = "MAIL_SMTP_LOCAL_NAME" // the EHLO name

	EnvMailSESRegion           = "MAIL_SES_REGION"
	EnvMailSESConfigurationSet = "MAIL_SES_CONFIGURATION_SET"
	EnvMailSESEndpoint         = "MAIL_SES_ENDPOINT"
	EnvMailSESTimeoutMs        = "MAIL_SES_TIMEOUT_MS"
)

// Mail drivers.
const (
	// MailDriverLog renders each message into the log and delivers nothing. It
	// is the development and test default, and a deployed tier is refused on
	// it — see validateDeployed.
	MailDriverLog = "log"
	// MailDriverSMTP is the self-hosted edition's relay.
	MailDriverSMTP = "smtp"
	// MailDriverSES is Amazon SES, the hosted product's provider.
	MailDriverSES = "ses"
)

// SMTP auth methods and TLS modes, mirrored from platform/mail/smtpmail so a
// typo fails the boot instead of the first send. They are duplicated rather
// than imported to keep this package a leaf: config is imported by everything,
// and it should not drag a transport package in behind it.
const (
	MailSMTPAuthNone    = "none"
	MailSMTPAuthPlain   = "plain"
	MailSMTPAuthLogin   = "login"
	MailSMTPAuthCRAMMD5 = "crammd5"

	MailSMTPTLSStartTLS = "starttls"
	MailSMTPTLSImplicit = "implicit"
	MailSMTPTLSNone     = "none"
)

// Mail defaults. Code, not file (the same rule the rate limiter follows): a
// config file that says nothing about mail still boots in development with a
// working, non-delivering mailer, and a deployed tier still fails closed.
const (
	DefaultMailProductName = "ZenTax"
	// DefaultMailFromAddress is only ever used by the log driver, which does
	// not deliver. It is deliberately a non-routable domain: if it ever reached
	// a real relay, it would bounce loudly rather than send mail from an
	// address nobody owns.
	DefaultMailFromAddress = "no-reply@localhost"
	DefaultMailSMTPPort    = 587
	DefaultMailTimeoutMs   = 15000
)

// MailConfig selects and configures the outbound mailer.
type MailConfig struct {
	Driver string `json:"driver"` // log | smtp | ses

	// FromAddress is the sender every message carries. In SES it must be a
	// verified identity; in SMTP the relay usually enforces its own rule.
	FromAddress string `json:"fromAddress"`
	FromName    string `json:"fromName"`
	// ReplyTo is optional. Leaving it empty makes the mail no-reply, which is
	// honest when nobody watches the mailbox.
	ReplyTo string `json:"replyTo"`
	// ProductName is what the templates call the product.
	ProductName string `json:"productName"`

	SMTP MailSMTPConfig `json:"smtp"`
	SES  MailSESConfig  `json:"ses"`
}

// MailSMTPConfig configures the self-host relay.
type MailSMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"` // none | plain | login | crammd5
	TLS      string `json:"tls"`  // starttls | implicit | none

	// AllowInsecure is the operator's explicit consent to an unprotected
	// session — TLS "none", a relay that does not offer STARTTLS, or a
	// certificate that is not verified. Without it every one of those is
	// refused, because a silent downgrade to plaintext puts invite tokens and
	// filing data on the wire in the clear and nothing in the product would
	// report it.
	AllowInsecure bool   `json:"allowInsecure"`
	SkipTLSVerify bool   `json:"skipTlsVerify"`
	CAFile        string `json:"caFile"`
	TimeoutMs     int    `json:"timeoutMs"`
	LocalName     string `json:"localName"`
}

// MailSESConfig configures the hosted provider.
type MailSESConfig struct {
	Region string `json:"region"`
	// ConfigurationSet is how bounces and complaints reach an event
	// destination; without one they are invisible.
	ConfigurationSet string `json:"configurationSet"`
	Endpoint         string `json:"endpoint"` // optional: a VPC endpoint
	TimeoutMs        int    `json:"timeoutMs"`
}

// Sends reports whether this configuration actually delivers mail — i.e. it is
// not the logging driver.
func (m MailConfig) Sends() bool { return m.Driver != MailDriverLog }

// SendTimeout is how long ONE transport call may take, taken from whichever
// driver is configured. Both adapters apply their own timeout only when the
// caller sets no deadline, and the dispatcher always sets one — so without
// this the operator-facing MAIL_*_TIMEOUT_MS settings are inert and every send
// is capped by the dispatcher's default instead.
func (m MailConfig) SendTimeout() time.Duration {
	ms := DefaultMailTimeoutMs
	switch m.Driver {
	case MailDriverSMTP:
		if m.SMTP.TimeoutMs > 0 {
			ms = m.SMTP.TimeoutMs
		}
	case MailDriverSES:
		if m.SES.TimeoutMs > 0 {
			ms = m.SES.TimeoutMs
		}
	}
	return time.Duration(ms) * time.Millisecond
}

// ApplyDefaults fills what an omitted `mail` section leaves empty. Exported for
// the same reason RateLimitConfig.ApplyDefaults is: a Config assembled in code
// (the acceptance harness) needs the same starting point Load produces.
func (m *MailConfig) ApplyDefaults() {
	if m.Driver == "" {
		m.Driver = MailDriverLog
	}
	if m.ProductName == "" {
		m.ProductName = DefaultMailProductName
	}
	if m.FromName == "" {
		m.FromName = m.ProductName
	}
	if m.FromAddress == "" && m.Driver == MailDriverLog {
		m.FromAddress = DefaultMailFromAddress
	}
	if m.SMTP.Port == 0 {
		m.SMTP.Port = DefaultMailSMTPPort
	}
	if m.SMTP.Auth == "" {
		// A username is the operator saying "authenticate"; without one the
		// only coherent default is an unauthenticated relay.
		if m.SMTP.Username != "" {
			m.SMTP.Auth = MailSMTPAuthPlain
		} else {
			m.SMTP.Auth = MailSMTPAuthNone
		}
	}
	if m.SMTP.TLS == "" {
		m.SMTP.TLS = MailSMTPTLSStartTLS
	}
	if m.SMTP.TimeoutMs <= 0 {
		m.SMTP.TimeoutMs = DefaultMailTimeoutMs
	}
	if m.SES.TimeoutMs <= 0 {
		m.SES.TimeoutMs = DefaultMailTimeoutMs
	}
}

// validate refuses a mail configuration that cannot send what it claims it
// will. Every violation is reported at once, and each message names the
// environment variable that fixes it.
func (m MailConfig) validate() error {
	var errs []error

	switch m.Driver {
	case MailDriverLog:
	case MailDriverSMTP:
		errs = append(errs, m.SMTP.validate()...)
	case MailDriverSES:
		if strings.TrimSpace(m.SES.Region) == "" {
			errs = append(errs, fmt.Errorf("mail.ses.region is required for the ses driver (set %s)", EnvMailSESRegion))
		}
	default:
		errs = append(errs, fmt.Errorf("mail.driver must be %q, %q or %q, got %q (set %s)",
			MailDriverLog, MailDriverSMTP, MailDriverSES, m.Driver, EnvMailDriver))
	}

	if err := validateMailbox(m.FromAddress); err != nil {
		errs = append(errs, fmt.Errorf("mail.fromAddress %w (set %s)", err, EnvMailFromAddress))
	}
	if strings.TrimSpace(m.ReplyTo) != "" {
		if err := validateMailbox(m.ReplyTo); err != nil {
			errs = append(errs, fmt.Errorf("mail.replyTo %w (set %s)", err, EnvMailReplyTo))
		}
	}
	if strings.TrimSpace(m.ProductName) == "" {
		errs = append(errs, fmt.Errorf("mail.productName must not be empty (set %s)", EnvMailProductName))
	}

	return errors.Join(errs...)
}

func (s MailSMTPConfig) validate() []error {
	var errs []error

	if strings.TrimSpace(s.Host) == "" {
		errs = append(errs, fmt.Errorf("mail.smtp.host is required for the smtp driver (set %s)", EnvMailSMTPHost))
	}
	if s.Port < 1 || s.Port > 65535 {
		errs = append(errs, fmt.Errorf("mail.smtp.port must be between 1 and 65535, got %d (set %s)", s.Port, EnvMailSMTPPort))
	}

	switch s.Auth {
	case MailSMTPAuthNone:
	case MailSMTPAuthPlain, MailSMTPAuthLogin, MailSMTPAuthCRAMMD5:
		if s.Username == "" || s.Password == "" {
			errs = append(errs, fmt.Errorf("mail.smtp.auth %q needs a username and a password (set %s and %s)",
				s.Auth, EnvMailSMTPUsername, EnvMailSMTPPassword))
		}
	default:
		errs = append(errs, fmt.Errorf("mail.smtp.auth must be %q, %q, %q or %q, got %q (set %s)",
			MailSMTPAuthNone, MailSMTPAuthPlain, MailSMTPAuthLogin, MailSMTPAuthCRAMMD5, s.Auth, EnvMailSMTPAuth))
	}

	switch s.TLS {
	case MailSMTPTLSStartTLS, MailSMTPTLSImplicit:
	case MailSMTPTLSNone:
		if !s.AllowInsecure {
			errs = append(errs, fmt.Errorf(
				"mail.smtp.tls is %q, which sends mail in the clear — set %s to %q or %q, or set %s=true to accept an unprotected relay",
				MailSMTPTLSNone, EnvMailSMTPTLS, MailSMTPTLSStartTLS, MailSMTPTLSImplicit, EnvMailSMTPAllowInsecure))
		}
	default:
		errs = append(errs, fmt.Errorf("mail.smtp.tls must be %q, %q or %q, got %q (set %s)",
			MailSMTPTLSStartTLS, MailSMTPTLSImplicit, MailSMTPTLSNone, s.TLS, EnvMailSMTPTLS))
	}

	if s.SkipTLSVerify && !s.AllowInsecure {
		errs = append(errs, fmt.Errorf(
			"mail.smtp.skipTlsVerify accepts any certificate — set %s=true to allow it, or point %s at the relay's CA bundle",
			EnvMailSMTPAllowInsecure, EnvMailSMTPCAFile))
	}
	if s.TimeoutMs <= 0 {
		errs = append(errs, fmt.Errorf("mail.smtp.timeoutMs must be positive (set %s)", EnvMailSMTPTimeoutMs))
	}

	return errs
}

// validateMailDeployed holds the two fail-closed mail rules of a deployed tier
// (ADR-0014). Both exist for the same reason: mail that is not sent is not an
// error anyone sees. A tenant does not know a reminder was due, and the product
// looks like it is working right up to the missed filing deadline — so the
// failure has to happen at boot, where an operator is watching.
func (m MailConfig) validateDeployed(envName string) []error {
	var errs []error

	if m.Driver == MailDriverLog {
		errs = append(errs, fmt.Errorf(
			"mail.driver is %q in %s: the log driver writes messages to the log and delivers nothing (set %s=%s or %s=%s)",
			MailDriverLog, envName, EnvMailDriver, MailDriverSES, EnvMailDriver, MailDriverSMTP))
	}

	// A sender that is not a real, routable identity is the other way mail
	// silently stops: SES refuses an unverified identity, and a relay rewrites
	// or drops a bare hostname sender.
	if err := validateMailbox(m.FromAddress); err != nil {
		errs = append(errs, fmt.Errorf("mail.fromAddress %w in %s (set %s)", err, envName, EnvMailFromAddress))
	} else if !strings.Contains(mailboxDomain(m.FromAddress), ".") {
		errs = append(errs, fmt.Errorf(
			"mail.fromAddress %q is not a routable sender in %s — the domain must be a fully-qualified name the deployment owns (set %s)",
			m.FromAddress, envName, EnvMailFromAddress))
	}

	return errs
}

// validateMailbox accepts one bare mailbox — no display name, which belongs in
// mail.fromName. net/mail is the same parser the mailer uses, so an address
// that passes here cannot fail later at the seam.
func validateMailbox(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New("is required")
	}
	parsed, err := netmail.ParseAddress(trimmed)
	if err != nil {
		return fmt.Errorf("%q is not a valid e-mail address", trimmed)
	}
	if parsed.Address != trimmed || parsed.Name != "" {
		return fmt.Errorf("%q must be a bare mailbox, with any display name in mail.fromName", trimmed)
	}
	return nil
}

func mailboxDomain(value string) string {
	_, domain, _ := strings.Cut(strings.TrimSpace(value), "@")
	return strings.ToLower(domain)
}

func mergeMailEnvOverrides(m *MailConfig) {
	setIf := func(env string, dst *string) {
		if val := os.Getenv(env); val != "" {
			*dst = val
		}
	}
	setIf(EnvMailDriver, &m.Driver)
	setIf(EnvMailFromAddress, &m.FromAddress)
	setIf(EnvMailFromName, &m.FromName)
	setIf(EnvMailReplyTo, &m.ReplyTo)
	setIf(EnvMailProductName, &m.ProductName)

	setIf(EnvMailSMTPHost, &m.SMTP.Host)
	setIf(EnvMailSMTPUsername, &m.SMTP.Username)
	setIf(EnvMailSMTPPassword, &m.SMTP.Password)
	setIf(EnvMailSMTPAuth, &m.SMTP.Auth)
	setIf(EnvMailSMTPTLS, &m.SMTP.TLS)
	setIf(EnvMailSMTPCAFile, &m.SMTP.CAFile)
	setIf(EnvMailSMTPLocalName, &m.SMTP.LocalName)
	setPositiveInt(EnvMailSMTPPort, &m.SMTP.Port)
	setPositiveInt(EnvMailSMTPTimeoutMs, &m.SMTP.TimeoutMs)
	setBool(EnvMailSMTPAllowInsecure, &m.SMTP.AllowInsecure)
	setBool(EnvMailSMTPSkipTLSVerify, &m.SMTP.SkipTLSVerify)

	setIf(EnvMailSESRegion, &m.SES.Region)
	setIf(EnvMailSESConfigurationSet, &m.SES.ConfigurationSet)
	setIf(EnvMailSESEndpoint, &m.SES.Endpoint)
	setPositiveInt(EnvMailSESTimeoutMs, &m.SES.TimeoutMs)
}

func setBool(env string, dst *bool) {
	val := os.Getenv(env)
	if val == "" {
		return
	}
	if b, err := strconv.ParseBool(val); err == nil {
		*dst = b
	}
}
