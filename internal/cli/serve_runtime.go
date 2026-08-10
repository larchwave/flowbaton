package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	integrationv1 "github.com/larchwave/flowbaton/contracts/integration/v1"
	"github.com/larchwave/flowbaton/internal/auth"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/server"
	"github.com/larchwave/flowbaton/internal/sessionstore"
	"github.com/larchwave/flowbaton/internal/transport"
	"github.com/larchwave/flowbaton/internal/version"
)

type ServeOptions struct {
	Address           string
	DatabaseURL       string
	TLSCertificate    string
	TLSPrivateKey     string
	ClientCA          string
	SigningKey        string
	SigningKeyID      string
	NodeID            string
	PublicAddress     string
	Inventory         string
	WorkerConcurrency int
	WorkerPoll        time.Duration
	WorkerClaim       time.Duration
	WorkerTimeout     time.Duration
	NodeHeartbeat     time.Duration
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
		runner.Serve = (ServeBootstrap{}).Run
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
	flags.StringVar(&options.NodeID, "node-id", "", "stable worker node identifier")
	flags.StringVar(&options.PublicAddress, "public-address", "", "advertised node URL")
	flags.StringVar(&options.Inventory, "inventory", "", "strict JSON device inventory")
	flags.IntVar(&options.WorkerConcurrency, "worker-concurrency", 1, "owner worker count")
	flags.DurationVar(&options.WorkerPoll, "worker-poll", 250*time.Millisecond, "durable work poll interval")
	flags.DurationVar(&options.WorkerClaim, "worker-claim", 30*time.Second, "pending work claim lifetime")
	flags.DurationVar(&options.WorkerTimeout, "worker-timeout", 20*time.Second, "device operation timeout")
	flags.DurationVar(&options.NodeHeartbeat, "node-heartbeat", 5*time.Second, "node heartbeat interval")
	if err := flags.Parse(args); err != nil {
		return ServeOptions{}, err
	}
	if flags.NArg() != 0 || options.DatabaseURL == "" || options.TLSCertificate == "" || options.TLSPrivateKey == "" || options.ClientCA == "" || options.SigningKey == "" || options.SigningKeyID == "" {
		return ServeOptions{}, errors.New("serve requires database, TLS, client CA, and signing key options")
	}
	if options.WorkerConcurrency < 1 || options.WorkerConcurrency > 32 || options.WorkerPoll <= 0 || options.WorkerClaim <= 0 || options.WorkerTimeout <= 0 || options.NodeHeartbeat <= 0 {
		return ServeOptions{}, errors.New("serve worker options must be positive and concurrency must not exceed 32")
	}
	return options, nil
}

type serveInventory struct {
	Devices []serveDevice `json:"devices"`
}

type serveDevice struct {
	TenantID        string   `json:"tenant_id"`
	ResourceID      string   `json:"resource_id"`
	Platform        string   `json:"platform"`
	Device          string   `json:"device"`
	Port            int      `json:"port"`
	ReinstallDriver bool     `json:"reinstall_driver"`
	Capabilities    []string `json:"capabilities"`
}

type ServeBootstrap struct {
	OpenStore   func(context.Context, string) (*sessionstore.Postgres, error)
	BuildDriver func(serveDevice) (device.Driver, error)
	Executable  func() (string, error)
	ProcessID   func() int
}

func DefaultServeRunner() ServeRunner {
	bootstrap := ServeBootstrap{}
	return ServeRunner{Serve: bootstrap.Run}
}

func (bootstrap ServeBootstrap) Run(ctx context.Context, options ServeOptions) error {
	if options.NodeID == "" || options.PublicAddress == "" || options.Inventory == "" {
		return errors.New("serve bootstrap requires node-id, public-address, and inventory")
	}
	if options.WorkerConcurrency == 0 {
		options.WorkerConcurrency = 1
	}
	if options.WorkerPoll == 0 {
		options.WorkerPoll = 250 * time.Millisecond
	}
	if options.WorkerClaim == 0 {
		options.WorkerClaim = 30 * time.Second
	}
	if options.WorkerTimeout == 0 {
		options.WorkerTimeout = 20 * time.Second
	}
	if options.NodeHeartbeat == 0 {
		options.NodeHeartbeat = 5 * time.Second
	}
	if options.WorkerConcurrency < 1 || options.WorkerConcurrency > 32 || options.WorkerPoll <= 0 || options.WorkerClaim <= 0 || options.WorkerTimeout <= 0 || options.NodeHeartbeat <= 0 {
		return errors.New("serve worker options are invalid")
	}
	inventory, err := loadServeInventory(options.Inventory)
	if err != nil {
		return err
	}
	openStore := bootstrap.OpenStore
	if openStore == nil {
		openStore = sessionstore.Open
	}
	store, err := openStore(ctx, options.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.ApplySchema(ctx); err != nil {
		return fmt.Errorf("apply PostgreSQL schema: %w", err)
	}
	tlsConfig, err := transport.LoadServerTLS(options.TLSCertificate, options.TLSPrivateKey, options.ClientCA)
	if err != nil {
		return fmt.Errorf("load serve TLS: %w", err)
	}
	keyID, privateKey, err := auth.LoadPrivateKey(options.SigningKey, options.SigningKeyID)
	if err != nil {
		return fmt.Errorf("load session signing key: %w", err)
	}
	now := time.Now().UTC()
	if err := store.RegisterNode(ctx, options.NodeID, options.PublicAddress, now); err != nil {
		return fmt.Errorf("register worker node: %w", err)
	}
	buildDriver := bootstrap.BuildDriver
	if buildDriver == nil {
		buildDriver = defaultServeDriver
	}
	executors := make(map[string]server.DeviceExecutor, len(inventory.Devices))
	var opened []device.Driver
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		for _, driver := range opened {
			_ = driver.Close(cleanupCtx)
		}
	}()
	capabilitySet := map[string]bool{}
	for _, entry := range inventory.Devices {
		driver, err := buildDriver(entry)
		if err != nil {
			return fmt.Errorf("construct device %s: %w", entry.ResourceID, err)
		}
		if err := driver.Open(ctx); err != nil {
			return fmt.Errorf("open device %s: %w", entry.ResourceID, err)
		}
		opened = append(opened, driver)
		if err := validateServeCapabilities(driver, entry.Capabilities); err != nil {
			return fmt.Errorf("device %s: %w", entry.ResourceID, err)
		}
		if err := store.RegisterDevice(ctx, entry.TenantID, entry.ResourceID, options.NodeID, entry.Capabilities); err != nil {
			return fmt.Errorf("register device %s: %w", entry.ResourceID, err)
		}
		executors[entry.ResourceID] = server.DriverExecutor{Driver: driver}
		for _, capability := range entry.Capabilities {
			capabilitySet[capability] = true
		}
	}
	capabilities := make([]string, 0, len(capabilitySet))
	for capability := range capabilitySet {
		capabilities = append(capabilities, capability)
	}
	sort.Strings(capabilities)
	integration, err := bootstrap.integrationDocument(capabilities)
	if err != nil {
		return err
	}
	issuer := auth.Issuer{KeyID: keyID, PrivateKey: privateKey, TTL: 5 * time.Minute}
	verifier := auth.Verifier{Keys: map[string]ed25519.PublicKey{keyID: privateKey.Public().(ed25519.PublicKey)}}
	handler, err := server.New(server.Config{Store: store, Issuer: issuer, Verifier: verifier, Integration: integration})
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, options.WorkerConcurrency+1)
	for index := 0; index < options.WorkerConcurrency; index++ {
		worker := &server.Worker{NodeID: options.NodeID, Store: store, Executors: executors, PollInterval: options.WorkerPoll, ClaimDuration: options.WorkerClaim, ExecutionTimeout: options.WorkerTimeout, HeartbeatInterval: options.NodeHeartbeat}
		go func() { results <- worker.Run(runCtx) }()
	}
	go func() {
		results <- server.Run(runCtx, server.RuntimeConfig{Address: options.Address, TLSConfig: tlsConfig, Handler: handler})
	}()
	err = <-results
	cancel()
	for index := 0; index < options.WorkerConcurrency; index++ {
		err = errors.Join(err, <-results)
	}
	return err
}

func (bootstrap ServeBootstrap) integrationDocument(capabilities []string) (integrationv1.Document, error) {
	executable := bootstrap.Executable
	if executable == nil {
		executable = os.Executable
	}
	path, err := executable()
	if err != nil {
		return integrationv1.Document{}, fmt.Errorf("locate executable: %w", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return integrationv1.Document{}, fmt.Errorf("hash executable: %w", err)
	}
	digest := sha256.Sum256(contents)
	processID := os.Getpid()
	if bootstrap.ProcessID != nil {
		processID = bootstrap.ProcessID()
	}
	return integrationv1.NewDocument(integrationv1.Executable{Version: version.Version, BinarySHA256: hex.EncodeToString(digest[:]), License: "Apache-2.0", ProcessID: processID}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, capabilities)
}

func loadServeInventory(path string) (serveInventory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return serveInventory{}, fmt.Errorf("read device inventory: %w", err)
	}
	var inventory serveInventory
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inventory); err != nil {
		return serveInventory{}, fmt.Errorf("decode device inventory: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return serveInventory{}, errors.New("decode device inventory: trailing JSON")
	}
	seen := map[string]bool{}
	for _, entry := range inventory.Devices {
		if entry.TenantID == "" || entry.ResourceID == "" || entry.Platform == "" || entry.Device == "" || entry.Port <= 0 || len(entry.Capabilities) == 0 || seen[entry.ResourceID] {
			return serveInventory{}, errors.New("device inventory contains an incomplete or duplicate device")
		}
		seen[entry.ResourceID] = true
	}
	if len(inventory.Devices) == 0 {
		return serveInventory{}, errors.New("device inventory contains no devices")
	}
	return inventory, nil
}

func defaultServeDriver(entry serveDevice) (device.Driver, error) {
	return newDriver(TestOptions{Platform: entry.Platform, ReinstallDriver: entry.ReinstallDriver}, entry.Device, entry.Port, 1)
}

func validateServeCapabilities(driver device.Driver, requested []string) error {
	if driver == nil {
		return errors.New("driver is required")
	}
	for _, capability := range requested {
		if !serveCommand(capability) {
			return fmt.Errorf("capability %q is unavailable", capability)
		}
	}
	return nil
}

func serveCommand(command string) bool {
	switch command {
	case "tap", "input-text", "press-key", "swipe", "set-orientation":
		return true
	default:
		return false
	}
}
