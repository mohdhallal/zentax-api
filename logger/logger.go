package logger

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// Field is a structured log attribute. Backed by slog.Attr so the package-level
// constructors below map directly onto the standard library.
type Field = slog.Attr

// Config controls logger construction. Loaded from the JSON config file's "log"
// section; CtxFields is wired internally and never serialized.
//
// TODO(ADR-0015): layer OpenTelemetry (→ Loki/Tempo) and a PII-redaction handler
// on top of this base slog handler.
type Config struct {
	Level  string `json:"level"`  // debug | info | warn | error (default: info)
	Format string `json:"format"` // json | text (default: json)

	CtxFields func(context.Context) []Field `json:"-"`
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
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Format == "text" {
		return slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.NewJSONHandler(os.Stdout, opts)
}

// InitBasic sets a sane default JSON logger (info level). Used before config load.
func InitBasic() {
	Log = &slogLogger{l: slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))}
}

// Init constructs the logger from config.
func Init(cfg *Config) {
	if cfg == nil {
		cfg = &Config{}
	}
	cfg.CtxFields = extractCtxFields
	Log = &slogLogger{l: slog.New(handlerFor(cfg))}
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
func Error(err error) Field                        { return slog.Any("error", err) }
func Duration(key string, val time.Duration) Field { return slog.Duration(key, val) }
func Any(key string, val any) Field                { return slog.Any(key, val) }
func Float64(key string, val float64) Field        { return slog.Float64(key, val) }
func Int64(key string, val int64) Field            { return slog.Int64(key, val) }
func Strings(key string, val []string) Field       { return slog.Any(key, val) }
func Time(key string, val time.Time) Field         { return slog.Time(key, val) }

func Stringp(key string, val *string) Field {
	if val == nil {
		return slog.String(key, "")
	}
	return slog.String(key, *val)
}
