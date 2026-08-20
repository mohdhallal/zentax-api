package middlewares_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares"
)

type mockExecerPgTx struct {
	withFn func(ctx context.Context, fn func(context.Context) error) error
}

func (m *mockExecerPgTx) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return m.withFn(ctx, fn)
}

// executeTx simulates a real transaction: calls fn and returns its error.
func executeTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func TestTransaction_SuccessPath_CommitsAndFlushes(t *testing.T) {
	t.Parallel()

	db := &mockExecerPgTx{withFn: executeTx}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":true}`))
	})

	mw := middlewares.Transaction(db)(handler)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `{"status":true}`)
}

func TestTransaction_Handler4xx_RollsBackAndFlushesErrorResponse(t *testing.T) {
	t.Parallel()

	db := &mockExecerPgTx{withFn: executeTx}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"status":false}`))
	})

	mw := middlewares.Transaction(db)(handler)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, r)

	// 4xx triggers rollback internally, but the buffered response is still flushed.
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `{"status":false}`)
}

func TestTransaction_DBError_NotRollback_Returns500(t *testing.T) {
	t.Parallel()

	dbErr := errors.New("connection reset by peer")
	db := &mockExecerPgTx{
		withFn: func(ctx context.Context, fn func(context.Context) error) error {
			// handler succeeds, but commit fails
			_ = fn(ctx)
			return dbErr
		},
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := middlewares.Transaction(db)(handler)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"status":false`)
}

func TestTransaction_ResponseBuffered_NotWrittenUntilAfterTx(t *testing.T) {
	t.Parallel()

	var realWriterSeenDuringTx bool
	w := httptest.NewRecorder()

	db := &mockExecerPgTx{
		withFn: func(ctx context.Context, fn func(context.Context) error) error {
			err := fn(ctx)
			// At this point the transaction is still open; real writer must be empty.
			realWriterSeenDuringTx = w.Body.Len() == 0
			return err
		},
	}
	handler := http.HandlerFunc(func(hw http.ResponseWriter, r *http.Request) {
		hw.WriteHeader(http.StatusCreated)
		_, _ = hw.Write([]byte("body"))
	})

	mw := middlewares.Transaction(db)(handler)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)

	mw.ServeHTTP(w, r)

	require.True(t, realWriterSeenDuringTx, "real writer should be empty while transaction is open")
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "body", w.Body.String())
}

func TestTransaction_TxContextPropagated(t *testing.T) {
	t.Parallel()

	type txKey struct{}
	var capturedCtx context.Context

	db := &mockExecerPgTx{
		withFn: func(ctx context.Context, fn func(context.Context) error) error {
			txCtx := context.WithValue(ctx, txKey{}, "tx-marker")
			return fn(txCtx)
		},
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
		w.WriteHeader(http.StatusOK)
	})

	mw := middlewares.Transaction(db)(handler)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, r)

	require.NotNil(t, capturedCtx)
	assert.Equal(t, "tx-marker", capturedCtx.Value(txKey{}))
}
