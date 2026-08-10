package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"sync/atomic"
	"time"

	integrationv1 "github.com/larchwave/flowbaton/contracts/integration/v1"
	"github.com/larchwave/flowbaton/internal/auth"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/drivercontract"
	"github.com/larchwave/flowbaton/internal/model"
	"github.com/larchwave/flowbaton/internal/server"
	"github.com/larchwave/flowbaton/internal/sessionstore"
	"github.com/larchwave/flowbaton/internal/strictjson"
	"github.com/larchwave/flowbaton/internal/transport"
	"github.com/larchwave/flowbaton/internal/version"
)

const (
	maxServeInventoryBytes = 1 << 20
	maxServeDevices        = 256
	maxServeCapabilities   = 5
	maxServeIdentifier     = 128
	maxServeWorkerTimeout  = 10 * time.Minute
	minServeClaimMargin    = 5 * time.Second
	minServeNodeHeartbeat  = 100 * time.Millisecond
	maxServeNodeHeartbeat  = time.Minute
	nodeLeaseHeartbeats    = 3
)

var serveIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

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
	if flags.NArg() != 0 {
		return ServeOptions{}, errors.New("serve does not accept positional arguments")
	}
	if err := validateServeOptions(options); err != nil {
		return ServeOptions{}, err
	}
	return options, nil
}

func validateServeOptions(options ServeOptions) error {
	if options.DatabaseURL == "" || options.TLSCertificate == "" || options.TLSPrivateKey == "" ||
		options.ClientCA == "" || options.SigningKey == "" || options.SigningKeyID == "" ||
		options.NodeID == "" || options.PublicAddress == "" || options.Inventory == "" {
		return errors.New("serve requires database, TLS, client CA, signing key, node identity, public address, and inventory options")
	}
	if err := validateServeIdentifier("node-id", options.NodeID); err != nil {
		return err
	}
	if err := validateServeIdentifier("signing-key-id", options.SigningKeyID); err != nil {
		return err
	}
	address, err := url.Parse(options.PublicAddress)
	if err != nil || address.Scheme != "https" || address.Host == "" || address.User != nil ||
		address.RawQuery != "" || address.Fragment != "" {
		return errors.New("serve public-address must be an absolute credential-free HTTPS URL")
	}
	if _, _, err := net.SplitHostPort(options.Address); err != nil {
		return fmt.Errorf("serve address must be host:port: %w", err)
	}
	if options.WorkerConcurrency < 1 || options.WorkerConcurrency > 32 || options.WorkerPoll <= 0 ||
		options.WorkerClaim <= 0 || options.WorkerTimeout <= 0 || options.NodeHeartbeat <= 0 ||
		options.WorkerTimeout > maxServeWorkerTimeout || options.WorkerClaim > maxServeWorkerTimeout+minServeClaimMargin ||
		options.NodeHeartbeat < minServeNodeHeartbeat || options.NodeHeartbeat > maxServeNodeHeartbeat {
		return errors.New("serve worker durations must be positive and bounded, and concurrency must be 1 through 32")
	}
	if options.WorkerClaim-options.WorkerTimeout < minServeClaimMargin {
		return errors.New("serve worker-claim must exceed worker-timeout by at least 5s")
	}
	return nil
}

func validateServeIdentifier(field, value string) error {
	if len(value) == 0 || len(value) > maxServeIdentifier || !serveIdentifierPattern.MatchString(value) {
		return fmt.Errorf("serve %s must be a bounded identifier", field)
	}
	return nil
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
	OpenStore      func(context.Context, string) (serveRuntimeStore, error)
	BuildDriver    func(context.Context, serveDevice) (device.Driver, error)
	LoadTLS        func(string, string, string) (*tls.Config, error)
	LoadPrivateKey func(string, string) (string, ed25519.PrivateKey, error)
	RunServer      func(context.Context, server.RuntimeConfig) error
	Executable     func() (string, error)
	ProcessID      func() int
}

type serveRuntimeStore interface {
	sessionstore.Store
	server.WorkerStore
	ApplySchema(context.Context) error
	RegisterNode(context.Context, string, string, time.Duration) (sessionstore.NodeLease, error)
	ActivateNode(context.Context, sessionstore.NodeLease) error
	DeactivateNode(context.Context, sessionstore.NodeLease) error
	HeartbeatNode(context.Context, sessionstore.NodeLease, time.Duration) error
	RegisterDevice(context.Context, string, string, sessionstore.NodeLease, []string) error
	RecoverAmbiguousInputs(context.Context, sessionstore.NodeLease, time.Duration) (int64, error)
	WaitForExecutionQuiescence(context.Context, sessionstore.NodeLease, time.Duration, time.Duration) error
	RequireReadyNode(context.Context, sessionstore.NodeLease) error
	ExpireSessions(context.Context, int) (int64, error)
	Close()
}

type serveNodeSupervisorStore interface {
	HeartbeatNode(context.Context, sessionstore.NodeLease, time.Duration) error
	RecoverAmbiguousInputs(context.Context, sessionstore.NodeLease, time.Duration) (int64, error)
	ExpireSessions(context.Context, int) (int64, error)
}

func DefaultServeRunner() ServeRunner {
	bootstrap := ServeBootstrap{}
	return ServeRunner{Serve: bootstrap.Run}
}

func (bootstrap ServeBootstrap) Run(ctx context.Context, options ServeOptions) (resultErr error) {
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
	if options.Address == "" {
		options.Address = "127.0.0.1:7443"
	}
	if err := validateServeOptions(options); err != nil {
		return err
	}
	inventory, err := loadServeInventory(options.Inventory)
	if err != nil {
		return err
	}
	capabilitySet := map[string]bool{}
	for _, entry := range inventory.Devices {
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
	loadTLS := bootstrap.LoadTLS
	if loadTLS == nil {
		loadTLS = transport.LoadServerTLS
	}
	tlsConfig, err := loadTLS(options.TLSCertificate, options.TLSPrivateKey, options.ClientCA)
	if err != nil {
		return fmt.Errorf("load serve TLS: %w", err)
	}
	loadPrivateKey := bootstrap.LoadPrivateKey
	if loadPrivateKey == nil {
		loadPrivateKey = auth.LoadPrivateKey
	}
	keyID, privateKey, err := loadPrivateKey(options.SigningKey, options.SigningKeyID)
	if err != nil {
		return fmt.Errorf("load session signing key: %w", err)
	}
	defer clear(privateKey)
	buildDriver := bootstrap.BuildDriver
	if buildDriver == nil {
		buildDriver = defaultServeDriver
	}
	type configuredDriver struct {
		entry  serveDevice
		driver device.Driver
	}
	configured := make([]configuredDriver, 0, len(inventory.Devices))
	for _, entry := range inventory.Devices {
		driver, err := buildDriver(ctx, entry)
		if err != nil {
			return fmt.Errorf("construct device %s: %w", entry.ResourceID, err)
		}
		if err := validateServeCapabilities(driver, entry); err != nil {
			return fmt.Errorf("device %s: %w", entry.ResourceID, err)
		}
		configured = append(configured, configuredDriver{entry: entry, driver: driver})
	}
	openStore := bootstrap.OpenStore
	if openStore == nil {
		openStore = func(ctx context.Context, databaseURL string) (serveRuntimeStore, error) {
			return sessionstore.Open(ctx, databaseURL)
		}
	}
	store, err := openStore(ctx, options.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.ApplySchema(ctx); err != nil {
		return fmt.Errorf("apply PostgreSQL schema: %w", err)
	}
	leaseFor := time.Duration(nodeLeaseHeartbeats) * options.NodeHeartbeat
	nodeLease, err := store.RegisterNode(ctx, options.NodeID, options.PublicAddress, leaseFor)
	if err != nil {
		return fmt.Errorf("register worker node: %w", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	var opened []device.Driver
	var nodeReady atomic.Bool
	runtimeResults := make(chan error, options.WorkerConcurrency+1)
	runtimeCount := 0
	supervisorReady := make(chan error, 1)
	supervisorResult := make(chan error, 1)
	go func() {
		supervisorErr := superviseServeNode(
			runCtx, store, nodeLease, options.NodeHeartbeat, leaseFor,
			options.WorkerTimeout, supervisorReady,
		)
		if supervisorErr != nil {
			cancel()
		}
		supervisorResult <- supervisorErr
	}()
	if supervisorErr := <-supervisorReady; supervisorErr != nil {
		cancel()
		<-supervisorResult
		deactivateErr := deactivateServeNode(ctx, store, nodeLease)
		return errors.Join(supervisorErr, deactivateErr)
	}
	defer func() {
		nodeReady.Store(false)
		cancel()
		var runtimeErr error
		for index := 0; index < runtimeCount; index++ {
			runtimeErr = errors.Join(runtimeErr, <-runtimeResults)
		}
		supervisorErr := <-supervisorResult
		deactivateErr := deactivateServeNode(ctx, store, nodeLease)
		if errors.Is(deactivateErr, sessionstore.ErrFenced) {
			deactivateErr = nil
		}
		var closeErr error
		for index := len(opened) - 1; index >= 0; index-- {
			cleanupCtx, cancelCleanup := context.WithTimeout(
				context.WithoutCancel(ctx), 15*time.Second,
			)
			driverErr := opened[index].Close(cleanupCtx)
			cancelCleanup()
			if driverErr != nil {
				closeErr = errors.Join(closeErr, fmt.Errorf(
					"close serve driver %s: %w", opened[index].Name(), driverErr,
				))
			}
		}
		resultErr = errors.Join(resultErr, runtimeErr, supervisorErr, deactivateErr, closeErr)
	}()
	expiryCtx, cancelExpiry := context.WithTimeout(runCtx, 15*time.Second)
	err = expireServeSessions(expiryCtx, store)
	cancelExpiry()
	if err != nil {
		return fmt.Errorf("expire device sessions before inventory registration: %w", err)
	}
	for _, entry := range inventory.Devices {
		if err := store.RegisterDevice(
			runCtx, entry.TenantID, entry.ResourceID, nodeLease, entry.Capabilities,
		); err != nil {
			return fmt.Errorf("register device %s: %w", entry.ResourceID, err)
		}
	}
	if err := store.WaitForExecutionQuiescence(
		runCtx, nodeLease, options.WorkerTimeout, options.WorkerPoll,
	); err != nil {
		return fmt.Errorf("wait for prior device input: %w", err)
	}
	issuer := auth.Issuer{KeyID: keyID, PrivateKey: privateKey, TTL: 5 * time.Minute}
	verifier := auth.Verifier{Keys: map[string]ed25519.PublicKey{keyID: privateKey.Public().(ed25519.PublicKey)}}
	handler, err := server.New(server.Config{
		Store: store, Issuer: issuer, Verifier: verifier, Integration: integration,
		Readiness: func(ctx context.Context) error {
			if !nodeReady.Load() {
				return errors.New("worker node is not ready")
			}
			return store.RequireReadyNode(ctx, nodeLease)
		},
	})
	if err != nil {
		return err
	}
	runServer := bootstrap.RunServer
	if runServer == nil {
		runServer = server.Run
	}
	serverStarted := make(chan struct{}, 1)
	runtimeCount++
	go func() {
		serverErr := runServer(runCtx, server.RuntimeConfig{
			Address: options.Address, TLSConfig: tlsConfig, Handler: handler,
			Started: func() {
				select {
				case serverStarted <- struct{}{}:
				default:
				}
			},
		})
		if serverErr == nil && runCtx.Err() == nil {
			serverErr = errors.New("runtime server stopped unexpectedly")
		}
		if serverErr != nil {
			cancel()
		}
		runtimeResults <- serverErr
	}()
	select {
	case <-serverStarted:
		select {
		case serverErr := <-runtimeResults:
			runtimeCount--
			return fmt.Errorf("start runtime server: %w", serverErr)
		default:
		}
	case serverErr := <-runtimeResults:
		runtimeCount--
		return fmt.Errorf("start runtime server: %w", serverErr)
	}
	executors := make(map[string]server.DeviceExecutor, len(inventory.Devices))
	for _, configuredDriver := range configured {
		entry, driver := configuredDriver.entry, configuredDriver.driver
		if err := runCtx.Err(); err != nil {
			return fmt.Errorf("runtime server stopped before device %s opened: %w", entry.ResourceID, err)
		}
		if err := driver.Open(runCtx); err != nil {
			return fmt.Errorf("open device %s: %w", entry.ResourceID, err)
		}
		opened = append(opened, driver)
		executors[entry.ResourceID] = server.DriverExecutor{Driver: driver}
	}
	if err := store.ActivateNode(runCtx, nodeLease); err != nil {
		return fmt.Errorf("activate worker node: %w", err)
	}
	nodeReady.Store(true)
	for index := 0; index < options.WorkerConcurrency; index++ {
		worker := &server.Worker{NodeLease: nodeLease, Store: store, Executors: executors, PollInterval: options.WorkerPoll, ClaimDuration: options.WorkerClaim, ExecutionTimeout: options.WorkerTimeout}
		runtimeCount++
		go func() { runtimeResults <- worker.Run(runCtx) }()
	}
	err = <-runtimeResults
	runtimeCount--
	cancel()
	return err
}

func expireServeSessions(ctx context.Context, store interface {
	ExpireSessions(context.Context, int) (int64, error)
}) error {
	for {
		expired, err := store.ExpireSessions(ctx, 64)
		if err != nil {
			return err
		}
		if expired < 64 {
			return nil
		}
	}
}

func deactivateServeNode(
	ctx context.Context,
	store serveRuntimeStore,
	lease sessionstore.NodeLease,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := store.DeactivateNode(cleanupCtx, lease); err != nil {
		return fmt.Errorf("deactivate worker node: %w", err)
	}
	return nil
}

func superviseServeNode(
	ctx context.Context,
	store serveNodeSupervisorStore,
	lease sessionstore.NodeLease,
	heartbeatInterval time.Duration,
	leaseFor time.Duration,
	executionTimeout time.Duration,
	ready chan<- error,
) error {
	callTimeout := min(heartbeatInterval, 5*time.Second)
	callCtx, cancelCall := context.WithTimeout(ctx, callTimeout)
	err := store.HeartbeatNode(callCtx, lease, leaseFor)
	cancelCall()
	if err != nil {
		err = fmt.Errorf("heartbeat worker node: %w", err)
		ready <- err
		return err
	}
	ready <- nil
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	maintenanceCtx, cancelMaintenance := context.WithCancel(ctx)
	maintenanceResult := make(chan error, 1)
	go func() {
		maintenanceResult <- superviseServeMaintenance(
			maintenanceCtx, store, lease, heartbeatInterval, executionTimeout, callTimeout,
		)
	}()
	for {
		select {
		case <-ctx.Done():
			cancelMaintenance()
			<-maintenanceResult
			return nil
		case err := <-maintenanceResult:
			cancelMaintenance()
			if err != nil {
				return err
			}
			return nil
		case <-ticker.C:
			callCtx, cancelCall := context.WithTimeout(ctx, callTimeout)
			err := store.HeartbeatNode(callCtx, lease, leaseFor)
			cancelCall()
			if err != nil {
				cancelMaintenance()
				maintenanceErr := <-maintenanceResult
				return errors.Join(fmt.Errorf("heartbeat worker node: %w", err), maintenanceErr)
			}
		}
	}
}

func superviseServeMaintenance(
	ctx context.Context,
	store serveNodeSupervisorStore,
	lease sessionstore.NodeLease,
	interval time.Duration,
	executionTimeout time.Duration,
	callTimeout time.Duration,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			callCtx, cancelCall := context.WithTimeout(ctx, callTimeout)
			_, err := store.RecoverAmbiguousInputs(callCtx, lease, executionTimeout)
			cancelCall()
			if err != nil {
				return fmt.Errorf("recover ambiguous device input: %w", err)
			}
			callCtx, cancelCall = context.WithTimeout(ctx, callTimeout)
			_, err = store.ExpireSessions(callCtx, 64)
			cancelCall()
			if err != nil {
				return fmt.Errorf("expire device sessions: %w", err)
			}
		}
	}
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
	file, err := os.Open(path)
	if err != nil {
		return integrationv1.Document{}, fmt.Errorf("hash executable: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return integrationv1.Document{}, errors.New("hash executable: executable is not a regular file")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return integrationv1.Document{}, fmt.Errorf("hash executable: %w", err)
	}
	processID := os.Getpid()
	if bootstrap.ProcessID != nil {
		processID = bootstrap.ProcessID()
	}
	return integrationv1.NewDocument(integrationv1.Executable{Version: version.Version, BinarySHA256: hex.EncodeToString(digest.Sum(nil)), License: "Apache-2.0", ProcessID: processID}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, capabilities)
}

func loadServeInventory(path string) (serveInventory, error) {
	file, err := os.Open(path)
	if err != nil {
		return serveInventory{}, fmt.Errorf("read device inventory: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxServeInventoryBytes {
		return serveInventory{}, errors.New("read device inventory: file must be regular, non-empty, and at most 1 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxServeInventoryBytes+1))
	if err != nil || len(data) > maxServeInventoryBytes {
		return serveInventory{}, errors.New("read device inventory: file exceeds the 1 MiB limit")
	}
	var inventory serveInventory
	if err := strictjson.Decode(data, &inventory); err != nil {
		return serveInventory{}, fmt.Errorf("decode device inventory: %w", err)
	}
	if len(inventory.Devices) == 0 || len(inventory.Devices) > maxServeDevices {
		return serveInventory{}, fmt.Errorf("device inventory must contain 1 through %d devices", maxServeDevices)
	}
	seen := map[string]bool{}
	for index := range inventory.Devices {
		entry := &inventory.Devices[index]
		if err := validateServeDevice(*entry); err != nil {
			return serveInventory{}, fmt.Errorf("device inventory entry %d: %w", index, err)
		}
		if seen[entry.ResourceID] {
			return serveInventory{}, errors.New("device inventory contains a duplicate resource_id")
		}
		seen[entry.ResourceID] = true
		sort.Strings(entry.Capabilities)
	}
	return inventory, nil
}

func validateServeDevice(entry serveDevice) error {
	for field, value := range map[string]string{
		"tenant_id": entry.TenantID, "resource_id": entry.ResourceID, "device": entry.Device,
	} {
		if err := validateServeIdentifier(field, value); err != nil {
			return err
		}
	}
	if entry.Port < 1 || entry.Port > 65535 {
		return errors.New("port must be 1 through 65535")
	}
	document, found := serveDriverContract(entry.Platform)
	if !found {
		return errors.New("platform must be android, ios, ios-physical, or web")
	}
	if entry.Platform == "web" && entry.ReinstallDriver {
		return errors.New("web inventory entries cannot reinstall a device driver")
	}
	if len(entry.Capabilities) == 0 || len(entry.Capabilities) > maxServeCapabilities {
		return fmt.Errorf("capabilities must contain 1 through %d commands", maxServeCapabilities)
	}
	seen := make(map[string]bool, len(entry.Capabilities))
	for _, capability := range entry.Capabilities {
		command, found := serveCommandKeyword(capability)
		if !found || !document.SupportsCommand(command) {
			return fmt.Errorf("capability %q is unavailable on %s", capability, entry.Platform)
		}
		if seen[capability] {
			return fmt.Errorf("capability %q is duplicated", capability)
		}
		seen[capability] = true
	}
	return nil
}

func defaultServeDriver(ctx context.Context, entry serveDevice) (device.Driver, error) {
	return newDriver(ctx, TestOptions{Platform: entry.Platform, ReinstallDriver: entry.ReinstallDriver}, entry.Device, entry.Port, 1)
}

func validateServeCapabilities(driver device.Driver, entry serveDevice) error {
	if driver == nil {
		return errors.New("driver is required")
	}
	capabilities := driver.Capabilities()
	expected := entry.Platform
	if expected == "ios-physical" {
		// The physical driver executes the shared "ios" surface; the inventory
		// token names the flavor, not a second driver platform.
		expected = "ios"
	}
	if string(capabilities.Platform) != expected {
		return fmt.Errorf("driver platform %q does not match inventory platform %q", capabilities.Platform, entry.Platform)
	}
	for _, capability := range entry.Capabilities {
		command, found := serveCommandKeyword(capability)
		if !found || !capabilities.Features[drivercontract.CommandFeature(string(command))] {
			return fmt.Errorf("capability %q is unavailable from the constructed driver", capability)
		}
	}
	return nil
}

func serveCommandKeyword(command string) (model.CommandKeyword, bool) {
	keywords := map[string]model.CommandKeyword{
		"tap":             model.CommandTapOn,
		"input-text":      model.CommandInputText,
		"press-key":       model.CommandPressKey,
		"swipe":           model.CommandSwipe,
		"set-orientation": model.CommandSetOrientation,
	}
	keyword, found := keywords[command]
	return keyword, found
}

func serveDriverContract(platform string) (drivercontract.Document, bool) {
	switch platform {
	case "android":
		return drivercontract.Android(), true
	case "ios":
		return drivercontract.IOSSimulator(), true
	case "ios-physical":
		return drivercontract.IOSPhysical(), true
	case "web":
		return drivercontract.Web(), true
	default:
		return drivercontract.Document{}, false
	}
}
