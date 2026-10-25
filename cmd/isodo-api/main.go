package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/injection"
	"github.com/DROWNING2003/Isodo/internal/control/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/control/postgres"
	"github.com/DROWNING2003/Isodo/internal/control/preview"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/control/sandboxworker"
	"github.com/DROWNING2003/Isodo/internal/control/template"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("isodo-api stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	databaseURL := os.Getenv("ISODO_DATABASE_URL")
	issuer := os.Getenv("ISODO_CASDOOR_ISSUER")
	clientID := os.Getenv("ISODO_CASDOOR_CLIENT_ID")
	if databaseURL == "" || issuer == "" || clientID == "" {
		return errors.New("ISODO_DATABASE_URL, ISODO_CASDOOR_ISSUER, and ISODO_CASDOOR_CLIENT_ID are required")
	}
	listenAddr := os.Getenv("ISODO_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8080"
	}
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()
	store, err := postgres.New(initCtx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	casdoor, err := auth.NewCasdoor(initCtx, issuer, clientID, store, nil)
	if err != nil {
		return err
	}
	web, err := auth.NewWeb(initCtx, store, issuer, clientID, os.Getenv("ISODO_CASDOOR_CLIENT_SECRET"), os.Getenv("ISODO_WEB_ORIGIN"))
	if err != nil {
		return err
	}
	var nodeClient *orchestrator.Client
	nodeFiles := orchestrator.TLSFiles{
		Certificate: os.Getenv("ISODO_CONTROL_TLS_CERT"),
		Key:         os.Getenv("ISODO_CONTROL_TLS_KEY"),
		CA:          os.Getenv("ISODO_NODE_SERVER_CA"),
		ServerName:  os.Getenv("ISODO_NODE_TLS_SERVER_NAME"),
		NodeID:      os.Getenv("ISODO_NODE_ID"),
	}
	nodeTarget := os.Getenv("ISODO_NODE_RPC_TARGET")
	if nodeTarget != "" || nodeFiles.Certificate != "" || nodeFiles.Key != "" || nodeFiles.CA != "" || nodeFiles.ServerName != "" || nodeFiles.NodeID != "" {
		nodeClient, err = orchestrator.NewClient(nodeTarget, nodeFiles)
		if err != nil {
			return fmt.Errorf("configure sandbox application Node client: %w", err)
		}
		defer nodeClient.Close()
	}
	access := &auth.Access{Bearer: casdoor, Keys: store, Web: web}
	sandboxHandler := sandbox.NewHandlerForBackendWithWebOrigin(store, os.Getenv("ISODO_NODE_BACKEND"), os.Getenv("ISODO_WEB_ORIGIN"), nodeClient)
	if nodeClient != nil {
		sandboxHandler.SetRuntime(nodeClient)
		sandboxHandler.SetFiles(nodeClient)
	}
	var relay preview.RelayDialer
	if nodeClient != nil && os.Getenv("ISODO_NODE_BACKEND") != "lima" {
		relay = nodeClient
	}
	publisher, err := preview.New(preview.Config{
		Secret:    os.Getenv("ISODO_PREVIEW_SECRET"),
		Domain:    os.Getenv("ISODO_PREVIEW_DOMAIN"),
		Scheme:    os.Getenv("ISODO_PREVIEW_SCHEME"),
		APIPort:   listenPort(listenAddr),
		AppOrigin: os.Getenv("ISODO_WEB_ORIGIN"),
		Relay:     relay,
	})
	if err != nil {
		return fmt.Errorf("configure sandbox preview gateway: %w", err)
	}
	if publisher != nil {
		sandboxHandler.SetAppLinkPublisher(publisher)
	}
	injectionKey, err := injection.LoadKey(os.Getenv("ISODO_INJECTION_KEY"), envOr("ISODO_INJECTION_KEY_FILE", "./certs/injection.key"))
	if err != nil {
		return err
	}
	sealer, err := injection.NewSealer(injectionKey)
	if err != nil {
		return err
	}
	injectionHandler := injection.NewHandler(injection.NewPostgresStore(store.Pool()), sealer)
	if nodeClient != nil {
		injectionHandler.SetRuntimeRevoker(nodeClient)
	}
	publicHandler := httpapi.NewHandlerWithInjections(web, auth.NewAPIKeyHandler(store), sandboxHandler, access, injectionHandler, template.NewHandler(store))
	if publisher != nil {
		publicHandler = publisher.Wrap(publicHandler)
	}
	// No server-wide read/write deadline: the preview gateway carries
	// long-lived HTTP streams and WebSockets (T3 /ws, terminal, file transfer).
	// Per-request handlers keep their own bounded timeouts.
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           securityHeaders(publicHandler),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("isodo-api listening", "address", listenAddr)
		serverErrors <- server.ListenAndServe()
	}()

	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if nodeClient != nil {
		worker, err := sandboxworker.New(store, nodeClient, os.Getenv("ISODO_NODE_ID"), os.Getenv("ISODO_NODE_BACKEND"), logger)
		if err != nil {
			return fmt.Errorf("configure sandbox create worker: %w", err)
		}
		go worker.Run(shutdownCtx)
	} else {
		logger.Warn("sandbox create worker is disabled; configure the Node gRPC client to run created sandboxes")
	}
	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-shutdownCtx.Done():
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func listenPort(address string) string {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	return port
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
