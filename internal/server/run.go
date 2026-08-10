package server

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

type RuntimeConfig struct {
	Address   string
	TLSConfig *tls.Config
	Handler   http.Handler
	Listener  net.Listener
	Started   func()
}

// Run serves the runtime until context cancellation, then performs a bounded shutdown.
func Run(ctx context.Context, config RuntimeConfig) error {
	if config.Handler == nil {
		return errors.New("server handler is required")
	}
	if config.TLSConfig == nil || config.TLSConfig.MinVersion < tls.VersionTLS13 || config.TLSConfig.ClientAuth != tls.RequireAndVerifyClientCert || config.TLSConfig.ClientCAs == nil {
		return errors.New("server requires TLS 1.3 mutual TLS with a client CA")
	}
	http2Only := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ProtoMajor != 2 {
			writer.Header().Set("Connection", "close")
			http.Error(writer, "FlowBaton remote transport requires HTTP/2", http.StatusUpgradeRequired)
			return
		}
		config.Handler.ServeHTTP(writer, request)
	})
	server := &http.Server{Addr: config.Address, Handler: http2Only, TLSConfig: config.TLSConfig, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	if err := http2.ConfigureServer(server, &http2.Server{}); err != nil {
		return err
	}
	listener := config.Listener
	var err error
	if listener == nil {
		listener, err = net.Listen("tcp", config.Address)
		if err != nil {
			return err
		}
	}
	if config.TLSConfig != nil {
		listener = tls.NewListener(listener, config.TLSConfig)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if config.Started != nil {
		config.Started()
	}
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
