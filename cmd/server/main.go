package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/bootstrap"
	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/logger"
)

func init() {
	logger.RegisterCtxField(func(ctx context.Context) []logger.Field {
		if reqId := app.GetRequestId(ctx); reqId != "" {
			return []logger.Field{logger.String("requestId", reqId)}
		}
		return nil
	})
}

func main() {
	logger.InitBasic()

	mode := types.ModeExternal
	if len(os.Args) > 1 {
		mode = types.ServerMode(os.Args[1])
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Log.Fatal("Failed to load config", logger.Error(err))
	}

	application, err := bootstrap.New(cfg, mode)
	if err != nil {
		logger.Log.Fatal("Failed to bootstrap application", logger.Error(err))
	}
	defer func() {
		if err := application.Close(); err != nil {
			logger.Log.Error("Failed to close application", logger.Error(err))
		}
	}()

	logger.Init(cfg.Log)

	port := cfg.App.Port
	if mode == types.ModeInternal {
		port = cfg.App.InternalPort
	}

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           application.Router,
		ReadHeaderTimeout: time.Duration(cfg.Server.ReadHeaderTimeoutMs) * time.Millisecond,
		WriteTimeout:      time.Duration(cfg.Server.WriteTimeoutMs) * time.Millisecond,
		IdleTimeout:       time.Duration(cfg.Server.IdleTimeoutMs) * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	wg.Go(func() {
		logger.Log.Info("Server listening",
			logger.String("mode", string(mode)),
			logger.Int("port", port),
			logger.String("env", cfg.App.Env),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Log.Error("HTTP listener failed", logger.Error(err))
			cancel()
		}
	})

	wg.Go(func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		select {
		case sig := <-sigChan:
			logger.Log.Info("Shutdown signal received", logger.String("signal", sig.String()))
		case <-ctx.Done():
		}

		shutdown(srv, cfg.Server.ShutdownTimeoutMs)
	})

	wg.Wait()
	logger.Log.Info("Shutdown complete")
}

func shutdown(srv *http.Server, timeoutMs int) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	logger.Log.Info("Graceful shutdown initialized")

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Log.Error("HTTP server shutdown error", logger.Error(err))
	}

	logger.Log.Info("HTTP server closed")
}
