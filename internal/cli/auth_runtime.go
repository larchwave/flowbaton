package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/larchwave/flowbaton/internal/sessionstore"
)

type AuthRunner struct {
	OpenAdmin func(context.Context, string) (sessionstore.IdentityAdmin, func(), error)
	WriteFile func(string, []byte, os.FileMode) error
}

func (runner AuthRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return authUsage(stderr)
	}
	if args[0] == "keygen" {
		return runner.keygen(args[1:], stdout, stderr)
	}
	if args[0] != "cert-map" || len(args) < 2 {
		return authUsage(stderr)
	}
	return runner.certMap(ctx, args[1:], stdout, stderr)
}

func (runner AuthRunner) keygen(args []string, stdout, stderr io.Writer) int {
	if len(args) != 6 || args[0] != "--key-id" || args[1] == "" || args[2] != "--private-key" || args[3] == "" || args[4] != "--public-key" || args[5] == "" {
		return authUsage(stderr)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(stderr, "auth keygen: %v\n", err)
		return ExitFailure
	}
	write := runner.WriteFile
	if write == nil {
		write = writeExclusive
	}
	privateJSON, _ := json.Marshal(map[string]string{"key_id": args[1], "algorithm": "Ed25519", "private_key": base64.RawStdEncoding.EncodeToString(privateKey)})
	publicJSON, _ := json.Marshal(map[string]string{"key_id": args[1], "algorithm": "Ed25519", "public_key": base64.RawStdEncoding.EncodeToString(publicKey)})
	if err := write(args[3], append(privateJSON, '\n'), 0o600); err != nil {
		fmt.Fprintf(stderr, "auth keygen: %v\n", err)
		return ExitFailure
	}
	if err := write(args[5], append(publicJSON, '\n'), 0o644); err != nil {
		_ = os.Remove(args[3])
		fmt.Fprintf(stderr, "auth keygen: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(stdout, "generated signing key %s\n", args[1])
	return ExitOK
}

func (runner AuthRunner) certMap(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	command := args[0]
	databaseURL, remaining, err := takeDatabaseURL(args[1:])
	if err != nil {
		return authUsage(stderr)
	}
	open := runner.OpenAdmin
	if open == nil {
		open = openPostgresAdmin
	}
	admin, closeStore, err := open(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "auth cert-map: %v\n", err)
		return ExitFailure
	}
	defer closeStore()
	switch command {
	case "add":
		if len(remaining) != 3 {
			return authUsage(stderr)
		}
		if err := admin.UpsertIdentity(ctx, sessionstore.Identity{CertificateFingerprint: remaining[0], TenantID: remaining[1], PrincipalID: remaining[2]}); err != nil {
			fmt.Fprintf(stderr, "auth cert-map add: %v\n", err)
			return ExitFailure
		}
		fmt.Fprintln(stdout, "certificate mapping stored")
	case "revoke":
		if len(remaining) != 1 {
			return authUsage(stderr)
		}
		if err := admin.RevokeIdentity(ctx, remaining[0], time.Now().UTC()); err != nil {
			fmt.Fprintf(stderr, "auth cert-map revoke: %v\n", err)
			return ExitFailure
		}
		fmt.Fprintln(stdout, "certificate mapping revoked")
	case "list":
		if len(remaining) != 0 {
			return authUsage(stderr)
		}
		identities, err := admin.ListIdentities(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "auth cert-map list: %v\n", err)
			return ExitFailure
		}
		encoder := json.NewEncoder(stdout)
		for _, identity := range identities {
			if err := encoder.Encode(identity); err != nil {
				return ExitFailure
			}
		}
	default:
		return authUsage(stderr)
	}
	return ExitOK
}

func takeDatabaseURL(args []string) (string, []string, error) {
	for index := 0; index < len(args); index++ {
		if args[index] == "--database-url" && index+1 < len(args) && args[index+1] != "" {
			return args[index+1], append(append([]string(nil), args[:index]...), args[index+2:]...), nil
		}
	}
	return "", nil, errors.New("database URL is required")
}

func openPostgresAdmin(ctx context.Context, databaseURL string) (sessionstore.IdentityAdmin, func(), error) {
	store, err := sessionstore.Open(ctx, databaseURL)
	if err != nil {
		return nil, func() {}, err
	}
	return store, store.Close, nil
}
func writeExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
func authUsage(stderr io.Writer) int {
	fmt.Fprintln(stderr, "usage: flowbaton auth keygen --key-id ID --private-key FILE --public-key FILE | auth cert-map add|revoke|list --database-url URL [FINGERPRINT TENANT PRINCIPAL]")
	return ExitInvalid
}
