package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"
)

// Field is a structured log attribute. Backed by slog.Attr so the package-level
// constructors below map directly onto the standard library.
type Field = slog.Attr

// Config controls logger construction. Loaded from the JSON config file's "log"
// section; CtxFields and Writer are wired internally and never serialized.
//
// Every logger built here emits through the ADR-0015 redaction handler
// (redact.go): a sensitive key's value never reaches the writer. The remaining
// TODO(ADR-0015) is the transport — OpenTelemetry → Loki/Tempo — not the rule.
type Config struct {
	Level  string `json:"level"`  // debug | info | warn | error (default: info)
	Format string `json:"format"` // json | text (default: json)

	CtxFields func(context.Context) []Field `json:"-"`

	// Writer is where lines go. Defaults to os.Stdout, which is what every
	// deployment uses (the container's stdout is the log driver's input); a
	// test sets it to a buffer to assert on what was emitted.
	Writer io.Writer `json:"-"`
}

// Logger is the structured logging interface used across the app.
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)
	WithContext(ctx context.Context) Logger
}

// Log is the process-wide logger. Initialized via InitBasic or Init.
var Log Logger

var ctxExtractors []func(context.Context) []Field

type slogLogger struct {
	l   *slog.Logger
	ctx context.Context
}

func handlerFor(cfg *Config) slog.Handler {
	level := slog.LevelInfo
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}

	opts := &slog.HandlerOptions{Level: level}
	var base slog.Handler
	if cfg.Format == "text" {
		base = slog.NewTextHandler(w, opts)
	} else {
		base = slog.NewJSONHandler(w, opts)
	}

	// ADR-0015: nothing reaches the writer unredacted, including from a call
	// site written after this line.
	return redactHandler{inner: base}
}

// New builds a logger without touching the process-wide Log. Used by tests that
// need to read back what was emitted, and available to anything that needs a
// second sink.
func New(cfg *Config) Logger {
	if cfg == nil {
		cfg = &Config{}
	}
	return &slogLogger{l: slog.New(handlerFor(cfg))}
}

// InitBasic sets a sane default JSON logger (info level). Used before config load.
func InitBasic() {
	Log = New(&Config{})
}

// Init constructs the logger from config.
func Init(cfg *Config) {
	if cfg == nil {
		cfg = &Config{}
	}
	cfg.CtxFields = extractCtxFields
	Log = New(cfg)
}

// RegisterCtxField registers a function that derives log fields from a request
// context (e.g. requestId, requester). Applied by WithContext.
func RegisterCtxField(fn func(context.Context) []Field) {
	ctxExtractors = append(ctxExtractors, fn)
}

func extractCtxFields(ctx context.Context) []Field {
	var fields []Field
	for _, fn := range ctxExtractors {
		fields = append(fields, fn(ctx)...)
	}
	return fields
}

func (s *slogLogger) log(level slog.Level, msg string, fields []Field) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	attrs := fields
	if s.ctx != nil {
		if cf := extractCtxFields(s.ctx); len(cf) > 0 {
			attrs = append(append(make([]Field, 0, len(cf)+len(fields)), cf...), fields...)
		}
	}
	s.l.LogAttrs(ctx, level, msg, attrs...)
}

func (s *slogLogger) Debug(msg string, fields ...Field) { s.log(slog.LevelDebug, msg, fields) }
func (s *slogLogger) Info(msg string, fields ...Field)  { s.log(slog.LevelInfo, msg, fields) }
func (s *slogLogger) Warn(msg string, fields ...Field)  { s.log(slog.LevelWarn, msg, fields) }
func (s *slogLogger) Error(msg string, fields ...Field) { s.log(slog.LevelError, msg, fields) }

func (s *slogLogger) Fatal(msg string, fields ...Field) {
	s.log(slog.LevelError, msg, fields)
	os.Exit(1)
}

func (s *slogLogger) WithContext(ctx context.Context) Logger {
	return &slogLogger{l: s.l, ctx: ctx}
}

// Field constructors — map onto slog.

func String(key, val string) Field                 { return slog.String(key, val) }
func Int(key string, val int) Field                { return slog.Int(key, val) }
func Bool(key string, val bool) Field              { return slog.Bool(key, val) }
func Duration(key string, val time.Duration) Field { return slog.Duration(key, val) }

// Error renders an error as bounded, scrubbed text rather than handing the
// value to the encoder.
//
// It matters because an error's text is not ours. A Postgres unique violation
// carries `Key (email)=(jane@acme.test) already exists` in its Detail, and
// ADR-0015 puts that beyond the erasure boundary the moment it is logged. As a
// slog.Any the value stays opaque — the redaction handler can read a string
// value but not inside an arbitrary one — and the JSON handler would then write
// the message out in full. Rendering here is what brings it under RedactText:
// e-mail-shaped text out, length bounded.
//
// This is a mitigation, not a guarantee: free text can hold a name or a figure
// that no pattern recognizes. The durable fix is for a call site not to hand a
// raw driver error to a log at all.
func Error(err error) Field {
	if err == nil {
		return slog.String("error", "")
	}
	return slog.String("error", RedactText(err.Error(), MaxTextRunes))
}

// Any is the blind whole-object constructor ADR-0015 forbids ("never log
// request bodies, env, or whole objects"): it defeats the key-based redaction
// in redact.go, because the personal data sits inside the value under keys the
// handler never sees. It stays exported for this package's own use and for a
// value that is provably a scalar; logscan_test.go fails the build on any use
// outside this package.
func Any(key string, val any) Field          { return slog.Any(key, val) }
func Float64(key string, val float64) Field  { return slog.Float64(key, val) }
func Int64(key string, val int64) Field      { return slog.Int64(key, val) }
func Strings(key string, val []string) Field { return slog.Any(key, val) }
func Time(key string, val time.Time) Field   { return slog.Time(key, val) }

func Stringp(key string, val *string) Field {
	if val == nil {
		return slog.String(key, "")
	}
	return slog.String(key, *val)
}
