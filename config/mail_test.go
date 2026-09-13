package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/logger"
)

// deployedWithMail is a production config that passes every fail-closed rule,
// including the mail ones — the baseline these tests break one rule at a time.
func deployedWithMail() *Config {
	return &Config{
		App:      AppConfig{Env: EnvProduction, Port: 3000},
		Database: DatabaseConfig{URL: "postgres://app:pw@db.internal:5432/zentax?sslmode=require"},
		CORS:     CORSConfig{AllowedOrigins: []string{"https://app.zentax.example"}},
		Log:      &logger.Config{Level: "info", Format: "json"},
		Auth:     AuthConfig{EncryptionKey: testKeyB64, SessionCookieSecure: true},
		Storage:  StorageConfig{Driver: StorageDriverFS, FS: StorageFSConfig{Root: "/var/lib/zentax/documents"}},
		Mail: MailConfig{
			Driver:      MailDriverSES,
			FromAddress: "no-reply@zentax.software",
			SES:         MailSESConfig{Region: "eu-central-1"},
		},
	}
}

// --- defaults ---------------------------------------------------------------

func TestMail_OmittedSectionDefaultsToTheLogDriver(t *testing.T) {
	t.Parallel()

	c := validBase() // a development config
	require.NoError(t, c.validate())

	assert.Equal(t, MailDriverLog, c.Mail.Driver, "development sends nothing until it is told to")
	assert.Equal(t, DefaultMailProductName, c.Mail.ProductName)
	assert.Equal(t, DefaultMailProductName, c.Mail.FromName, "the display name follows the product name")
	assert.Equal(t, DefaultMailFromAddress, c.Mail.FromAddress)
	assert.Equal(t, DefaultMailSMTPPort, c.Mail.SMTP.Port)
	assert.Equal(t, MailSMTPTLSStartTLS, c.Mail.SMTP.TLS)
	assert.Equal(t, MailSMTPAuthNone, c.Mail.SMTP.Auth)
	assert.Equal(t, DefaultMailTimeoutMs, c.Mail.SMTP.TimeoutMs)
	assert.Equal(t, DefaultMailTimeoutMs, c.Mail.SES.TimeoutMs)
	assert.False(t, c.Mail.Sends())
}

func TestMail_AUsernameImpliesAuthentication(t *testing.T) {
	t.Parallel()

	m := MailConfig{SMTP: MailSMTPConfig{Username: "zentax"}}
	m.ApplyDefaults()
	assert.Equal(t, MailSMTPAuthPlain, m.SMTP.Auth)

	m = MailConfig{}
	m.ApplyDefaults()
	assert.Equal(t, MailSMTPAuthNone, m.SMTP.Auth)
}

func TestMail_DefaultsDoNotOverrideWhatWasSet(t *testing.T) {
	t.Parallel()

	m := MailConfig{
		Driver: MailDriverSMTP, ProductName: "Acme Tax", FromName: "Acme", FromAddress: "tax@acme.test",
		SMTP: MailSMTPConfig{Port: 465, TLS: MailSMTPTLSImplicit, Auth: MailSMTPAuthLogin, TimeoutMs: 5000},
		SES:  MailSESConfig{TimeoutMs: 3000},
	}
	m.ApplyDefaults()

	assert.Equal(t, "Acme Tax", m.ProductName)
	assert.Equal(t, "Acme", m.FromName)
	assert.Equal(t, "tax@acme.test", m.FromAddress)
	assert.Equal(t, 465, m.SMTP.Port)
	assert.Equal(t, MailSMTPTLSImplicit, m.SMTP.TLS)
	assert.Equal(t, MailSMTPAuthLogin, m.SMTP.Auth)
	assert.Equal(t, 5000, m.SMTP.TimeoutMs)
	assert.Equal(t, 3000, m.SES.TimeoutMs)
}

func TestMail_ANonLogDriverGetsNoDefaultSender(t *testing.T) {
	t.Parallel()

	// The localhost default belongs to the driver that never delivers; a driver
	// that does must name its own sender or fail validation.
	c := validBase()
	c.Mail = MailConfig{Driver: MailDriverSES, SES: MailSESConfig{Region: "eu-central-1"}}
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mail.fromAddress")
	assert.Contains(t, err.Error(), EnvMailFromAddress)
}

// --- validation, every tier -------------------------------------------------

func TestMail_UnknownDriverFailsClosed(t *testing.T) {
	t.Parallel()

	c := validBase()
	c.Mail.Driver = "sendgrid"
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mail.driver")
	assert.Contains(t, err.Error(), EnvMailDriver)
}

func TestMail_SMTPNeedsAHost(t *testing.T) {
	t.Parallel()

	c := validBase()
	c.Mail = MailConfig{Driver: MailDriverSMTP, FromAddress: "no-reply@zentax.test"}
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mail.smtp.host")

	c.Mail.SMTP.Host = "relay.internal"
	require.NoError(t, c.validate())
}

func TestMail_SMTPRefusesToSendInTheClearWithoutConsent(t *testing.T) {
	t.Parallel()

	c := validBase()
	c.Mail = MailConfig{
		Driver: MailDriverSMTP, FromAddress: "no-reply@zentax.test",
		SMTP: MailSMTPConfig{Host: "relay.internal", TLS: MailSMTPTLSNone},
	}
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "in the clear")
	assert.Contains(t, err.Error(), EnvMailSMTPAllowInsecure)

	c.Mail.SMTP.AllowInsecure = true
	require.NoError(t, c.validate(), "an operator may say so explicitly")
}

func TestMail_SMTPRefusesToSkipVerificationWithoutConsent(t *testing.T) {
	t.Parallel()

	c := validBase()
	c.Mail = MailConfig{
		Driver: MailDriverSMTP, FromAddress: "no-reply@zentax.test",
		SMTP: MailSMTPConfig{Host: "relay.internal", SkipTLSVerify: true},
	}
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvMailSMTPCAFile, "the message points at the answer that keeps verification on")

	c.Mail.SMTP.AllowInsecure = true
	require.NoError(t, c.validate())
}

func TestMail_SMTPRejectsMalformedTransportSettings(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*MailSMTPConfig){
		"unknown auth":        func(s *MailSMTPConfig) { s.Auth = "oauth2" },
		"unknown tls":         func(s *MailSMTPConfig) { s.TLS = "ssl" },
		"auth without secret": func(s *MailSMTPConfig) { s.Auth = MailSMTPAuthPlain; s.Username = "u" },
		"port out of range":   func(s *MailSMTPConfig) { s.Port = 70000 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := validBase()
			c.Mail = MailConfig{Driver: MailDriverSMTP, FromAddress: "no-reply@zentax.test", SMTP: MailSMTPConfig{Host: "relay.internal"}}
			c.Mail.ApplyDefaults()
			mutate(&c.Mail.SMTP)
			require.Error(t, c.validate())
		})
	}
}

func TestMail_SESNeedsARegion(t *testing.T) {
	t.Parallel()

	c := validBase()
	c.Mail = MailConfig{Driver: MailDriverSES, FromAddress: "no-reply@zentax.test"}
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mail.ses.region")
	assert.Contains(t, err.Error(), EnvMailSESRegion)
}

func TestMail_SenderAndReplyToMustBeBareMailboxes(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"not-an-address", "ZenTax <no-reply@zentax.test>", "no-reply@"} {
		c := validBase()
		c.Mail.FromAddress = address
		require.Error(t, c.validate(), "%q must be refused as a sender", address)

		c = validBase()
		c.Mail.ReplyTo = address
		require.Error(t, c.validate(), "%q must be refused as a reply-to", address)
	}

	// A blank reply-to is "unset", not "invalid": no-reply is a legitimate
	// choice when nobody watches the mailbox.
	blank := validBase()
	blank.Mail.ReplyTo = "  "
	require.NoError(t, blank.validate())

	c := validBase()
	c.Mail.FromAddress = "no-reply@zentax.test"
	c.Mail.ReplyTo = "support@zentax.test"
	require.NoError(t, c.validate())
}

// --- validation, deployed tiers ---------------------------------------------

func TestMailDeployed_ValidConfigPasses(t *testing.T) {
	t.Parallel()
	for _, env := range []string{EnvStaging, EnvProduction} {
		c := deployedWithMail()
		c.App.Env = env
		require.NoError(t, c.validate(), env)
	}
}

func TestMailDeployed_TheLogDriverIsRefused(t *testing.T) {
	t.Parallel()

	// The rule that matters most: a tier that cannot deliver must not pretend
	// it can. Mail that is never sent produces no error anyone sees — the
	// product looks healthy right up to the missed filing deadline.
	c := deployedWithMail()
	c.Mail.Driver = MailDriverLog
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delivers nothing")
	assert.Contains(t, err.Error(), EnvMailDriver)
}

func TestMailDeployed_AnOmittedMailSectionIsTheLogDriverAndIsRefused(t *testing.T) {
	t.Parallel()

	// Defaults are applied before the deployed rules run, so "said nothing
	// about mail" and "asked for the log driver" fail the same way.
	c := deployedWithMail()
	c.Mail = MailConfig{}
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvMailDriver)
}

func TestMailDeployed_TheSenderMustBeRoutable(t *testing.T) {
	t.Parallel()

	c := deployedWithMail()
	c.Mail.FromAddress = "no-reply@localhost"
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a routable sender")
	assert.Contains(t, err.Error(), EnvMailFromAddress)

	c.Mail.FromAddress = "no-reply"
	err = c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvMailFromAddress)

	c.Mail.FromAddress = "no-reply@eu.zentax.software"
	require.NoError(t, c.validate())
}

func TestMailDeployed_AllViolationsReportedAtOnce(t *testing.T) {
	t.Parallel()

	c := deployedWithMail()
	c.Mail = MailConfig{Driver: MailDriverLog, FromAddress: "no-reply@localhost"}
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvMailDriver)
	assert.Contains(t, err.Error(), EnvMailFromAddress)
}

// --- environment overrides ---------------------------------------------------

func TestMail_EnvOverrides(t *testing.T) {
	t.Setenv(EnvMailDriver, "smtp")
	t.Setenv(EnvMailFromAddress, "tax@acme.test")
	t.Setenv(EnvMailFromName, "Acme Tax")
	t.Setenv(EnvMailReplyTo, "support@acme.test")
	t.Setenv(EnvMailProductName, "Acme Tax Cloud")
	t.Setenv(EnvMailSMTPHost, "relay.acme.internal")
	t.Setenv(EnvMailSMTPPort, "2525")
	t.Setenv(EnvMailSMTPUsername, "zentax")
	t.Setenv(EnvMailSMTPPassword, "s3cret")
	t.Setenv(EnvMailSMTPAuth, "login")
	t.Setenv(EnvMailSMTPTLS, "implicit")
	t.Setenv(EnvMailSMTPAllowInsecure, "true")
	t.Setenv(EnvMailSMTPSkipTLSVerify, "true")
	t.Setenv(EnvMailSMTPCAFile, "/etc/zentax/relay-ca.pem")
	t.Setenv(EnvMailSMTPTimeoutMs, "9000")
	t.Setenv(EnvMailSMTPLocalName, "zentax.acme.test")
	t.Setenv(EnvMailSESRegion, "eu-west-1")
	t.Setenv(EnvMailSESConfigurationSet, "zentax-prod")
	t.Setenv(EnvMailSESEndpoint, "https://vpce-1234.email.eu-west-1.vpce.amazonaws.com")
	t.Setenv(EnvMailSESTimeoutMs, "7000")

	m := MailConfig{Driver: MailDriverLog}
	mergeMailEnvOverrides(&m)

	assert.Equal(t, MailDriverSMTP, m.Driver)
	assert.Equal(t, "tax@acme.test", m.FromAddress)
	assert.Equal(t, "Acme Tax", m.FromName)
	assert.Equal(t, "support@acme.test", m.ReplyTo)
	assert.Equal(t, "Acme Tax Cloud", m.ProductName)
	assert.Equal(t, MailSMTPConfig{
		Host: "relay.acme.internal", Port: 2525, Username: "zentax", Password: "s3cret",
		Auth: MailSMTPAuthLogin, TLS: MailSMTPTLSImplicit,
		AllowInsecure: true, SkipTLSVerify: true,
		CAFile: "/etc/zentax/relay-ca.pem", TimeoutMs: 9000, LocalName: "zentax.acme.test",
	}, m.SMTP)
	assert.Equal(t, MailSESConfig{
		Region: "eu-west-1", ConfigurationSet: "zentax-prod",
		Endpoint: "https://vpce-1234.email.eu-west-1.vpce.amazonaws.com", TimeoutMs: 7000,
	}, m.SES)
}

func TestMail_EmptyEnvNeverOverrides(t *testing.T) {
	// The compose file and the ECS task both pass "" for the driver they are
	// not using.
	for _, env := range []string{
		EnvMailDriver, EnvMailFromAddress, EnvMailFromName, EnvMailReplyTo, EnvMailProductName,
		EnvMailSMTPHost, EnvMailSMTPPort, EnvMailSMTPUsername, EnvMailSMTPPassword,
		EnvMailSMTPAuth, EnvMailSMTPTLS, EnvMailSMTPAllowInsecure, EnvMailSMTPSkipTLSVerify,
		EnvMailSMTPCAFile, EnvMailSMTPTimeoutMs, EnvMailSMTPLocalName,
		EnvMailSESRegion, EnvMailSESConfigurationSet, EnvMailSESEndpoint, EnvMailSESTimeoutMs,
	} {
		t.Setenv(env, "")
	}

	before := MailConfig{
		Driver: MailDriverSES, FromAddress: "keep@zentax.test", FromName: "Keep",
		ReplyTo: "keep-reply@zentax.test", ProductName: "Keep",
		SMTP: MailSMTPConfig{Host: "keep", Port: 25, Username: "keep", Password: "keep", Auth: MailSMTPAuthPlain, TLS: MailSMTPTLSImplicit, AllowInsecure: true, TimeoutMs: 1},
		SES:  MailSESConfig{Region: "keep", ConfigurationSet: "keep", Endpoint: "keep", TimeoutMs: 1},
	}
	after := before
	mergeMailEnvOverrides(&after)
	assert.Equal(t, before, after)
}

func TestMail_BadEnvValuesAreIgnored(t *testing.T) {
	t.Setenv(EnvMailSMTPPort, "five-two-five")
	t.Setenv(EnvMailSMTPTimeoutMs, "-1")
	t.Setenv(EnvMailSMTPAllowInsecure, "maybe")

	m := MailConfig{SMTP: MailSMTPConfig{Port: 587, TimeoutMs: 15000}}
	mergeMailEnvOverrides(&m)

	assert.Equal(t, 587, m.SMTP.Port)
	assert.Equal(t, 15000, m.SMTP.TimeoutMs)
	assert.False(t, m.SMTP.AllowInsecure)
}

func TestMail_EnvOverridesReachValidation(t *testing.T) {
	// The path a deployment actually takes: file says nothing, environment
	// names the transport, and the whole thing validates as production.
	t.Setenv(EnvMailDriver, "ses")
	t.Setenv(EnvMailFromAddress, "no-reply@eu.zentax.software")
	t.Setenv(EnvMailSESRegion, "eu-central-1")

	c := deployedWithMail()
	c.Mail = MailConfig{}
	mergeEnvOverrides(c)
	require.NoError(t, c.validate())
	assert.Equal(t, MailDriverSES, c.Mail.Driver)
	assert.True(t, c.Mail.Sends())
}
