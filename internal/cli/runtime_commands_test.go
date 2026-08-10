package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	devicesessionv1 "github.com/larchwave/flowbaton/contracts/device-session/v1"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/drivercontract"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
	"github.com/larchwave/flowbaton/internal/server"
	"github.com/larchwave/flowbaton/internal/sessionstore"
)

func TestServeRunnerParsesRequiredRuntimeInputs(t *testing.T) {
	var got ServeOptions
	runner := ServeRunner{Serve: func(_ context.Context, options ServeOptions) error { got = options; return nil }}
	args := []string{
		"--database-url", "postgres://db", "--tls-cert", "cert.pem", "--tls-key", "key.pem",
		"--client-ca", "ca.pem", "--signing-key", "signing.json", "--signing-key-id", "key-1",
		"--node-id", "node-1", "--public-address", "https://node.example", "--inventory", "inventory.json",
	}
	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(), args, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if got.DatabaseURL != "postgres://db" || got.Address != "127.0.0.1:7443" {
		t.Fatalf("options=%#v", got)
	}
}

type fakeIdentityAdmin struct{ added sessionstore.Identity }

func (admin *fakeIdentityAdmin) UpsertIdentity(_ context.Context, identity sessionstore.Identity) error {
	admin.added = identity
	return nil
}
func (*fakeIdentityAdmin) RevokeIdentity(context.Context, string, time.Time) error { return nil }
func (*fakeIdentityAdmin) ListIdentities(context.Context) ([]sessionstore.Identity, error) {
	return nil, nil
}

func TestAuthRunnerStoresCertificateMappingAndGeneratesKeys(t *testing.T) {
	admin := &fakeIdentityAdmin{}
	runner := AuthRunner{OpenAdmin: func(context.Context, string) (sessionstore.IdentityAdmin, func(), error) {
		return admin, func() {}, nil
	}}
	var stdout, stderr bytes.Buffer
	args := []string{"cert-map", "add", "--database-url", "postgres://db", "fingerprint", "tenant-1", "principal-1"}
	if code := runner.Run(context.Background(), args, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if admin.added.TenantID != "tenant-1" || admin.added.PrincipalID != "principal-1" {
		t.Fatalf("identity=%#v", admin.added)
	}
	var writes []os.FileMode
	runner.WriteFile = func(_ string, _ []byte, mode os.FileMode) error { writes = append(writes, mode); return nil }
	stdout.Reset()
	stderr.Reset()
	if code := runner.Run(context.Background(), []string{"keygen", "--key-id", "key-1", "--private-key", "private.json", "--public-key", "public.json"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("keygen exit=%d stderr=%s", code, stderr.String())
	}
	if len(writes) != 2 || writes[0] != 0o600 || writes[1] != 0o644 {
		t.Fatalf("key modes=%v", writes)
	}
}

func TestDBRunnerDelegatesSchemaApplication(t *testing.T) {
	called := ""
	runner := DBRunner{ApplySchema: func(_ context.Context, url string) error { called = url; return nil }}
	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(), []string{"apply-schema", "--database-url", "postgres://db"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if called != "postgres://db" {
		t.Fatalf("URL=%q", called)
	}
}

func TestServeBootstrapBuildsExactExecutableIdentityAndStrictInventory(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "flowbaton")
	if err := os.WriteFile(executable, []byte("exact executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	bootstrap := ServeBootstrap{Executable: func() (string, error) { return executable, nil }, ProcessID: func() int { return 42 }}
	document, err := bootstrap.integrationDocument([]string{"tap"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(document)
	if !bytes.Contains(encoded, []byte(`"process_id":42`)) || !bytes.Contains(encoded, []byte(`"version":"dev"`)) {
		t.Fatalf("document=%s", encoded)
	}
	inventoryPath := filepath.Join(directory, "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{"devices":[{"tenant_id":"tenant-1","resource_id":"device-1","platform":"android","device":"serial-1","port":7001,"reinstall_driver":false,"capabilities":["tap"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if inventory, err := loadServeInventory(inventoryPath); err != nil || len(inventory.Devices) != 1 {
		t.Fatalf("inventory=%#v err=%v", inventory, err)
	}
	if err := os.WriteFile(inventoryPath, []byte(`{"devices":[],"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServeInventory(inventoryPath); err == nil {
		t.Fatal("unknown inventory field was accepted")
	}
	if err := os.WriteFile(inventoryPath, []byte(`{"devices":[],"devices":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServeInventory(inventoryPath); err == nil {
		t.Fatal("duplicate inventory field was accepted")
	}
	if err := os.WriteFile(inventoryPath, []byte(`{"devices":[{"tenant_id":"tenant-1","resource_id":"device-1","platform":"web","device":"chrome","port":7001,"reinstall_driver":false,"capabilities":["set-orientation"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServeInventory(inventoryPath); err == nil {
		t.Fatal("platform-impossible inventory capability was accepted")
	}
}

func TestServeOptionsKeepClaimsLongerThanDeviceOperations(t *testing.T) {
	options := ServeOptions{
		Address: "127.0.0.1:7443", DatabaseURL: "postgres://db", TLSCertificate: "cert.pem",
		TLSPrivateKey: "key.pem", ClientCA: "ca.pem", SigningKey: "signing.json", SigningKeyID: "key-1",
		NodeID: "node-1", PublicAddress: "https://node.example", Inventory: "inventory.json",
		WorkerConcurrency: 1, WorkerPoll: time.Second, WorkerClaim: 20 * time.Second,
		WorkerTimeout: 20 * time.Second, NodeHeartbeat: time.Second,
	}
	if err := validateServeOptions(options); err == nil {
		t.Fatal("worker claim that can expire during a device operation was accepted")
	}
	options.WorkerClaim = 25 * time.Second
	if err := validateServeOptions(options); err != nil {
		t.Fatalf("bounded worker options were rejected: %v", err)
	}
}

func TestServeBootstrapRejectsALiveNodeBeforeOpeningADriver(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "flowbaton")
	if err := os.WriteFile(executable, []byte("serve test executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	inventoryPath := filepath.Join(directory, "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{"devices":[{"tenant_id":"tenant-1","resource_id":"device-1","platform":"android","device":"serial-1","port":7001,"reinstall_driver":true,"capabilities":["tap"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{Capabilities: []device.Capabilities{{
		Platform: "android",
		Features: map[string]bool{drivercontract.CommandFeature(string(model.CommandTapOn)): true},
	}}})
	store := &busyServeStore{}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := ServeBootstrap{
		OpenStore: func(context.Context, string) (serveRuntimeStore, error) { return store, nil },
		BuildDriver: func(_ context.Context, _ serveDevice) (device.Driver, error) {
			return driver, nil
		},
		LoadTLS: func(string, string, string) (*tls.Config, error) {
			return &tls.Config{MinVersion: tls.VersionTLS13}, nil
		},
		LoadPrivateKey: func(string, string) (string, ed25519.PrivateKey, error) {
			return "key-1", privateKey, nil
		},
		Executable: func() (string, error) { return executable, nil },
	}
	err = bootstrap.Run(context.Background(), ServeOptions{
		Address: "127.0.0.1:7443", DatabaseURL: "postgres://db", TLSCertificate: "cert.pem",
		TLSPrivateKey: "key.pem", ClientCA: "ca.pem", SigningKey: "signing.json", SigningKeyID: "key-1",
		NodeID: "node-1", PublicAddress: "https://node.example", Inventory: inventoryPath,
	})
	if !errors.Is(err, sessionstore.ErrBusy) {
		t.Fatalf("ServeBootstrap.Run() error = %v, want live-node refusal", err)
	}
	for _, action := range driver.Actions() {
		if action.Method == enginetest.MethodOpen {
			t.Fatalf("driver actions = %v; duplicate node was detected after Open", driver.Actions())
		}
	}
	if store.registerCalls != 1 || store.closed != 1 {
		t.Fatalf("store register calls=%d close calls=%d", store.registerCalls, store.closed)
	}
}

func TestServeNodeSupervisorFailsClosedOnALostEpoch(t *testing.T) {
	store := &fencingSupervisorStore{}
	ready := make(chan error, 1)
	result := make(chan error, 1)
	go func() {
		result <- superviseServeNode(
			context.Background(), store,
			sessionstore.NodeLease{NodeID: "node-1", WorkerEpoch: 7},
			time.Millisecond, 3*time.Millisecond, 20*time.Millisecond, ready,
		)
	}()
	if err := <-ready; err != nil {
		t.Fatalf("initial recovery error = %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, sessionstore.ErrFenced) {
			t.Fatalf("supervisor error = %v, want ErrFenced", err)
		}
	case <-time.After(time.Second):
		t.Fatal("node supervisor did not stop after losing its epoch")
	}
	if store.heartbeatCalls != 2 {
		t.Fatalf("recover calls=%d heartbeat calls=%d", store.recoverCalls, store.heartbeatCalls)
	}
}

func TestServeBootstrapPublishesBeforeOpenAndDeactivatesBeforeClose(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "flowbaton")
	if err := os.WriteFile(executable, []byte("serve lifecycle executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	inventoryPath := filepath.Join(directory, "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{"devices":[{"tenant_id":"tenant-1","resource_id":"device-1","platform":"android","device":"serial-1","port":7001,"reinstall_driver":true,"capabilities":["tap"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	trace := []string{}
	baseDriver := enginetest.NewFakeDriver()
	baseDriver.Enqueue(enginetest.DriverScript{Capabilities: []device.Capabilities{{
		Platform: "android",
		Features: map[string]bool{drivercontract.CommandFeature(string(model.CommandTapOn)): true},
	}}})
	driver := &tracingServeDriver{FakeDriver: baseDriver, trace: &trace}
	ctx, cancel := context.WithCancel(context.Background())
	store := &successfulServeStore{
		busyServeStore: &busyServeStore{},
		trace:          &trace,
		cancel:         cancel,
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := ServeBootstrap{
		OpenStore: func(context.Context, string) (serveRuntimeStore, error) { return store, nil },
		BuildDriver: func(_ context.Context, _ serveDevice) (device.Driver, error) {
			return driver, nil
		},
		LoadTLS: func(string, string, string) (*tls.Config, error) {
			return &tls.Config{
				MinVersion: tls.VersionTLS13,
				ClientAuth: tls.RequireAndVerifyClientCert,
				ClientCAs:  x509.NewCertPool(),
			}, nil
		},
		LoadPrivateKey: func(string, string) (string, ed25519.PrivateKey, error) {
			return "key-1", privateKey, nil
		},
		RunServer: func(ctx context.Context, config server.RuntimeConfig) error {
			trace = append(trace, "server-start")
			config.Started()
			<-ctx.Done()
			trace = append(trace, "server-stop")
			return nil
		},
		Executable: func() (string, error) { return executable, nil },
	}
	err = bootstrap.Run(ctx, ServeOptions{
		Address: "127.0.0.1:0", DatabaseURL: "postgres://db", TLSCertificate: "cert.pem",
		TLSPrivateKey: "key.pem", ClientCA: "ca.pem", SigningKey: "signing.json", SigningKeyID: "key-1",
		NodeID: "node-1", PublicAddress: "https://node.example", Inventory: inventoryPath,
	})
	if err != nil {
		t.Fatalf("ServeBootstrap.Run() error = %v", err)
	}
	want := []string{
		"register-node", "register-device", "recover", "server-start", "driver-open",
		"activate-node", "server-stop", "deactivate-node", "driver-close", "store-close",
	}
	if !equalStrings(trace, want) {
		t.Fatalf("serve lifecycle = %v, want %v", trace, want)
	}
}

type busyServeStore struct {
	registerCalls int
	closed        int
}

func (*busyServeStore) Ping(context.Context) error                     { return nil }
func (*busyServeStore) CurrentTime(context.Context) (time.Time, error) { return time.Now().UTC(), nil }
func (*busyServeStore) ReserveTokenNonce(context.Context, string, string, time.Duration) (sessionstore.TokenWindow, error) {
	now := time.Now().UTC().Truncate(time.Second)
	return sessionstore.TokenWindow{IssuedAt: now, ExpiresAt: now.Add(time.Minute)}, nil
}
func (*busyServeStore) Acquire(context.Context, sessionstore.AcquireInput) (sessionstore.Result, error) {
	return sessionstore.Result{}, nil
}
func (*busyServeStore) Apply(context.Context, sessionstore.MutationInput) (sessionstore.Result, error) {
	return sessionstore.Result{}, nil
}
func (*busyServeStore) Events(context.Context, string, string, string, int64) ([]devicesessionv1.Event, error) {
	return nil, nil
}
func (*busyServeStore) ResolveIdentity(context.Context, string) (sessionstore.Identity, error) {
	return sessionstore.Identity{}, nil
}
func (*busyServeStore) ValidateSessionAccess(context.Context, string, string, string, string, string, time.Time, int64, string) error {
	return nil
}
func (*busyServeStore) ApplySchema(context.Context) error { return nil }
func (store *busyServeStore) RegisterNode(context.Context, string, string, time.Duration) (sessionstore.NodeLease, error) {
	store.registerCalls++
	return sessionstore.NodeLease{}, sessionstore.ErrBusy
}
func (*busyServeStore) ActivateNode(context.Context, sessionstore.NodeLease) error { return nil }
func (*busyServeStore) DeactivateNode(context.Context, sessionstore.NodeLease) error {
	return nil
}
func (*busyServeStore) HeartbeatNode(context.Context, sessionstore.NodeLease, time.Duration) error {
	return nil
}
func (*busyServeStore) RegisterDevice(context.Context, string, string, sessionstore.NodeLease, []string) error {
	return nil
}
func (*busyServeStore) RecoverAmbiguousInputs(context.Context, sessionstore.NodeLease, time.Duration) (int64, error) {
	return 0, nil
}
func (*busyServeStore) WaitForExecutionQuiescence(context.Context, sessionstore.NodeLease, time.Duration, time.Duration) error {
	return nil
}
func (*busyServeStore) RequireReadyNode(context.Context, sessionstore.NodeLease) error { return nil }
func (*busyServeStore) ExpireSessions(context.Context, int) (int64, error)             { return 0, nil }
func (*busyServeStore) ClaimFrame(context.Context, sessionstore.NodeLease, time.Duration) (sessionstore.FrameWork, error) {
	return sessionstore.FrameWork{}, sessionstore.ErrNotFound
}
func (*busyServeStore) CompleteFrame(context.Context, sessionstore.FrameWork, sessionstore.FrameData) error {
	return nil
}
func (*busyServeStore) FailFrame(context.Context, sessionstore.FrameWork, string, bool, string) error {
	return nil
}
func (*busyServeStore) ClaimInput(context.Context, sessionstore.NodeLease, time.Duration) (sessionstore.InputWork, error) {
	return sessionstore.InputWork{}, sessionstore.ErrNotFound
}
func (*busyServeStore) StartInput(context.Context, sessionstore.InputWork) error {
	return nil
}
func (*busyServeStore) CompleteInput(context.Context, sessionstore.InputWork, string, time.Duration, *sessionstore.ExecutionFailure) error {
	return nil
}
func (*busyServeStore) WaitInputActive(context.Context, sessionstore.InputWork, time.Duration) (bool, error) {
	return false, nil
}
func (*busyServeStore) WaitForWork(context.Context, string, time.Duration) error { return nil }
func (store *busyServeStore) Close()                                             { store.closed++ }

type fencingSupervisorStore struct {
	recoverCalls   int
	heartbeatCalls int
}

type successfulServeStore struct {
	*busyServeStore
	trace  *[]string
	cancel context.CancelFunc
}

func (store *successfulServeStore) RegisterNode(
	context.Context, string, string, time.Duration,
) (sessionstore.NodeLease, error) {
	*store.trace = append(*store.trace, "register-node")
	return sessionstore.NodeLease{NodeID: "node-1", WorkerEpoch: 1}, nil
}

func (store *successfulServeStore) RegisterDevice(
	context.Context, string, string, sessionstore.NodeLease, []string,
) error {
	*store.trace = append(*store.trace, "register-device")
	return nil
}

func (store *successfulServeStore) WaitForExecutionQuiescence(
	context.Context, sessionstore.NodeLease, time.Duration, time.Duration,
) error {
	*store.trace = append(*store.trace, "recover")
	return nil
}

func (store *successfulServeStore) ActivateNode(context.Context, sessionstore.NodeLease) error {
	*store.trace = append(*store.trace, "activate-node")
	store.cancel()
	return nil
}

func (store *successfulServeStore) DeactivateNode(context.Context, sessionstore.NodeLease) error {
	*store.trace = append(*store.trace, "deactivate-node")
	return nil
}

func (store *successfulServeStore) Close() {
	*store.trace = append(*store.trace, "store-close")
}

func (store *successfulServeStore) WaitForWork(ctx context.Context, _ string, wait time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

type tracingServeDriver struct {
	*enginetest.FakeDriver
	trace *[]string
}

func (driver *tracingServeDriver) Open(ctx context.Context) error {
	*driver.trace = append(*driver.trace, "driver-open")
	return driver.FakeDriver.Open(ctx)
}

func (driver *tracingServeDriver) Close(ctx context.Context) error {
	*driver.trace = append(*driver.trace, "driver-close")
	return driver.FakeDriver.Close(ctx)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (store *fencingSupervisorStore) RecoverAmbiguousInputs(
	context.Context, sessionstore.NodeLease, time.Duration,
) (int64, error) {
	store.recoverCalls++
	return 0, nil
}

func (store *fencingSupervisorStore) HeartbeatNode(
	context.Context, sessionstore.NodeLease, time.Duration,
) error {
	store.heartbeatCalls++
	if store.heartbeatCalls > 1 {
		return sessionstore.ErrFenced
	}
	return nil
}

func (*fencingSupervisorStore) ExpireSessions(context.Context, int) (int64, error) {
	return 0, nil
}
