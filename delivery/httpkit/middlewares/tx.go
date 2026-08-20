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

type bufferedResponseWriter struct {
	http.ResponseWriter
	status int
	buf    []byte
}

func (w *bufferedResponseWriter) WriteHeader(code int) {
	w.status = code
}

func (w *bufferedResponseWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	return len(b), nil
}

func (w *bufferedResponseWriter) flush() {
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(w.buf)
}

func Transaction(db database.ExecerPgTx) types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &bufferedResponseWriter{ResponseWriter: w, status: http.StatusOK}

			err := db.WithinTransaction(r.Context(), func(txCtx context.Context) error {
				next.ServeHTTP(bw, r.WithContext(txCtx))
				if bw.status >= 400 {
					return errRollback
				}
				return nil
			})

			if err != nil && !errors.Is(err, errRollback) {
				httperr.HandleError(w, r, err)
				return
			}

			bw.flush()
		})
	}
}
