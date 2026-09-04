package middlewares

import (
	"context"
	"errors"
	"net/http"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var errRollback = errors.New("transaction rollback: handler error")

// txResponseWriter is what the handler writes to inside the transaction.
// Buffered (the default) holds the whole body until the tx commits, so a
// failed commit can still answer an error. Streaming (RouteDefinition.Stream)
// writes through as soon as the handler does — for downloads, whose body must
// never be materialised in memory (ADR-0022); such routes are read-only, so
// nothing needs un-doing once the bytes are out.
type txResponseWriter struct {
	http.ResponseWriter
	stream      bool
	status      int
	wroteHeader bool
	buf         []byte
}

func (w *txResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	if w.stream {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *txResponseWriter) Write(b []byte) (int, error) {
	if w.stream {
		if !w.wroteHeader {
			w.WriteHeader(http.StatusOK)
		}
		return w.ResponseWriter.Write(b)
	}
	w.buf = append(w.buf, b...)
	return len(b), nil
}

// Flush lets a streaming handler push bytes to the client mid-body.
func (w *txResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok && w.stream {
		f.Flush()
	}
}

// flush releases a buffered response; a streaming one is already on the wire.
func (w *txResponseWriter) flush() {
	if w.stream {
		return
	}
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(w.buf)
}

// Transaction runs the handler inside a database transaction (the RLS tenant
// GUC is bound at that seam, ADR-0004), rolling back on any 4xx/5xx status.
func Transaction(db database.ExecerPgTx) types.Middleware {
	return transaction(db, false)
}

// StreamingTransaction is Transaction for RouteDefinition.Stream handlers: the
// response is written through instead of buffered until commit.
func StreamingTransaction(db database.ExecerPgTx) types.Middleware {
	return transaction(db, true)
}

func transaction(db database.ExecerPgTx, stream bool) types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tw := &txResponseWriter{ResponseWriter: w, stream: stream, status: http.StatusOK}

			err := db.WithinTransaction(r.Context(), func(txCtx context.Context) error {
				next.ServeHTTP(tw, r.WithContext(txCtx))
				if tw.status >= 400 {
					return errRollback
				}
				return nil
			})

			if err != nil && !errors.Is(err, errRollback) {
				// A streamed body is already on the wire: nothing sensible can be
				// appended to it. Only an unwritten response can carry the error.
				if !(stream && tw.wroteHeader) {
					httperr.HandleError(w, r, err)
				}
				return
			}

			tw.flush()
		})
	}
}
