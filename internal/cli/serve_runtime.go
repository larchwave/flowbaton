package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

type ServeOptions struct {
	Address        string
	DatabaseURL    string
	TLSCertificate string
	TLSPrivateKey  string
	ClientCA       string
	SigningKey     string
	SigningKeyID   string
}

// ServeRunner parses the public serve command and delegates construction to
// the runtime bootstrap owned by main. The field keeps secrets and listeners
// out of argument parsing tests.
type ServeRunner struct {
	Serve func(context.Context, ServeOptions) error
}

func (runner ServeRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, err := parseServeOptions(args, stderr)
	if err != nil {
		return ExitInvalid
	}
	if runner.Serve == nil {
		fmt.Fprintln(stderr, "serve: runtime bootstrap is not wired")
		return ExitFailure
	}
	if err := runner.Serve(ctx, options); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return ExitFailure
	}
	return ExitOK
}

func parseServeOptions(args []string, stderr io.Writer) (ServeOptions, error) {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options ServeOptions
	flags.StringVar(&options.Address, "address", "127.0.0.1:7443", "TLS listen address")
	flags.StringVar(&options.DatabaseURL, "database-url", "", "PostgreSQL URL")
	flags.StringVar(&options.TLSCertificate, "tls-cert", "", "server certificate PEM")
	flags.StringVar(&options.TLSPrivateKey, "tls-key", "", "server private key PEM")
	flags.StringVar(&options.ClientCA, "client-ca", "", "client CA PEM")
	flags.StringVar(&options.SigningKey, "signing-key", "", "Ed25519 signing key")
	flags.StringVar(&options.SigningKeyID, "signing-key-id", "", "signing key identifier")
	if err := flags.Parse(args); err != nil {
		return ServeOptions{}, err
	}
	if flags.NArg() != 0 || options.DatabaseURL == "" || options.TLSCertificate == "" || options.TLSPrivateKey == "" || options.ClientCA == "" || options.SigningKey == "" || options.SigningKeyID == "" {
		return ServeOptions{}, errors.New("serve requires database, TLS, client CA, and signing key options")
	}
	return options, nil
}
