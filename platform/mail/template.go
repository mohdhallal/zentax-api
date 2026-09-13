package mail

import (
	"errors"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
)

// Rendering: text and HTML from the same typed values.
//
// Every template comes in two forms that say the SAME thing. A recipient whose
// client shows the plain part must learn exactly what a recipient with HTML
// learns — a filing deadline that differs between the two parts is a wrong
// answer delivered with confidence — so the two sources are written side by
// side, and template_test.go asserts that every value rendered into one appears
// in the other.
//
// The values are typed (a dateonly.Date is a date, an invite token is a token),
// never a pre-formatted blob handed in by a caller: formatting, escaping and
// link building all happen here, once, where they can be tested.
//
// The product name and the public base address come from configuration through
// Brand, so a white-labelled deployment and a self-hosted one differ by config
// rather than by code.

// Brand is the deployment's identity, built once at startup from config
// (mail.productName / app.publicBaseUrl / mail.fromAddress / mail.replyTo).
type Brand struct {
	// ProductName is what the mail calls itself ("ZenTax").
	ProductName string
	// BaseURL is the product's public origin, without a trailing slash — the
	// base of every link. It may be empty (a cell that has no public hostname
	// yet, ADR-0025), and then rendering a template that needs a link fails
	// with a permanent error naming PUBLIC_BASE_URL rather than sending a mail
	// whose only call to action is broken.
	BaseURL string
	// From is the sender identity every message carries.
	From Address
	// ReplyTo is where a human reply goes. Optional.
	ReplyTo Address
}

// NewBrand validates the identity a deployment configured. It does NOT require
// BaseURL: see the field comment.
func NewBrand(productName, baseURL string, from, replyTo Address) (Brand, error) {
	name := strings.TrimSpace(productName)
	if name == "" {
		return Brand{}, errors.New("mail: product name is required")
	}
	if err := from.Validate(); err != nil {
		return Brand{}, fmt.Errorf("mail: sender: %w", err)
	}
	if !replyTo.IsZero() {
		if err := replyTo.Validate(); err != nil {
			return Brand{}, fmt.Errorf("mail: reply-to: %w", err)
		}
	}
	return Brand{
		ProductName: name,
		BaseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		From:        from,
		ReplyTo:     replyTo,
	}, nil
}

// ErrNoBaseURL is returned when a template needs a link and the deployment has
// no public origin yet. It is deliberately its own error: a cell may boot
// before its custom domain exists (ADR-0025), so this is a condition that
// clears when the operator sets the origin — the message waits rather than
// dying, which is why the caller must be able to tell it apart from a template
// that will never render.
var ErrNoBaseURL = errors.New("mail: no public base address is configured, so no link can be built (set PUBLIC_BASE_URL)")

// Link resolves an absolute URL for a path that must begin with "/".
func (b Brand) Link(path string) (string, error) {
	if b.BaseURL == "" {
		return "", ErrNoBaseURL
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("mail: link path %q must start with /", path)
	}
	return b.BaseURL + path, nil
}

// Template is one message in two renderings. T is the typed value set it reads.
type Template[T any] struct {
	kind    string
	subject *texttemplate.Template
	text    *texttemplate.Template
	html    *htmltemplate.Template
}

// Kind is the template's fixed label — the value that reaches Message.Kind, a
// log line and the outbox row.
func (t *Template[T]) Kind() string { return t.kind }

// view is what a template source sees: the brand's two public facts, plus the
// typed data under .Data.
type view[T any] struct {
	Product string
	BaseURL string
	Data    T
}

// newTemplate parses one template at package initialisation. A parse error is a
// programming error in a string constant, so it panics: the process must not
// start with a template that cannot render.
func newTemplate[T any](kind, subject, textContent, htmlContent string) *Template[T] {
	textSet := texttemplate.Must(texttemplate.New("layout").Parse(textLayout))
	texttemplate.Must(textSet.New("content").Parse(textContent))

	htmlSet := htmltemplate.Must(htmltemplate.New("layout").Parse(htmlLayout))
	htmltemplate.Must(htmlSet.New("content").Parse(htmlContent))

	return &Template[T]{
		kind:    kind,
		subject: texttemplate.Must(texttemplate.New("subject").Parse(subject)),
		text:    textSet,
		html:    htmlSet,
	}
}

// Render turns typed values into a Message ready for a Sender.
//
// Every failure here is PERMANENT: a template that cannot execute, a link that
// cannot be built and a message that fails validation will all fail the same
// way on every retry, so the outbox records the reason instead of burning
// attempts on it.
func (t *Template[T]) Render(b Brand, to Address, data T) (Message, error) {
	op := "mail: render " + t.kind

	if b.ProductName == "" {
		return Message{}, Permanent(op, "", errors.New("brand is not configured"))
	}
	v := view[T]{Product: b.ProductName, BaseURL: b.BaseURL, Data: data}

	subject, err := execute(t.subject, v)
	if err != nil {
		return Message{}, Permanent(op, "subject", err)
	}
	text, err := execute(t.text, v)
	if err != nil {
		return Message{}, Permanent(op, "text", err)
	}
	html, err := executeHTML(t.html, v)
	if err != nil {
		return Message{}, Permanent(op, "html", err)
	}

	msg := Message{
		Kind:    t.kind,
		From:    b.From,
		ReplyTo: b.ReplyTo,
		To:      to,
		Subject: collapseSpace(subject),
		Text:    strings.TrimSpace(text) + "\n",
		HTML:    strings.TrimSpace(html) + "\n",
	}
	if err := msg.Validate(); err != nil {
		return Message{}, Permanent(op, "", err)
	}
	return msg, nil
}

func execute[T any](t *texttemplate.Template, v view[T]) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func executeHTML[T any](t *htmltemplate.Template, v view[T]) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

// collapseSpace folds a multi-line subject template into the single line a
// Subject header is allowed to be.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// The two layouts. They carry the product name and the public address, and
// nothing else: no tracking pixel, no remote image (blocked by default in every
// serious client anyway), no external stylesheet. HTML mail is styled inline
// because that is the only thing clients reliably honour.
const textLayout = `{{template "content" .}}

--
{{.Product}}{{if .BaseURL}} · {{.BaseURL}}{{end}}
This is an automated message from your {{.Product}} account.
`

const htmlLayout = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Product}}</title>
</head>
<body style="margin:0;padding:24px;background:#f5f5f7;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1f2430;">
<div style="max-width:600px;margin:0 auto;background:#ffffff;border:1px solid #e3e5ea;border-radius:8px;padding:28px;">
<div style="font-weight:600;font-size:15px;letter-spacing:0.02em;color:#4338ca;margin-bottom:20px;">{{.Product}}</div>
{{template "content" .}}
<hr style="border:0;border-top:1px solid #e3e5ea;margin:28px 0 16px;">
<p style="margin:0;font-size:12px;color:#6b7280;">
{{.Product}}{{if .BaseURL}} · <a href="{{.BaseURL}}" style="color:#4338ca;">{{.BaseURL}}</a>{{end}}<br>
This is an automated message from your {{.Product}} account.
</p>
</div>
</body>
</html>
`
