package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// The Viewer HTTP server is spec 03's optional read-only window onto the device
// hierarchy. These tests drive its handlers with httptest, injecting a fake
// hierarchy source, so no device is needed.

func viewerFetching(fetch func(ctx context.Context, platform, udid string, _ []string, _ string) (device.TreeNode, error)) http.Handler {
	return viewerHandler(HierarchyRunner{Fetch: fetch})
}

func TestViewerHierarchyEndpointReturnsTheTree(t *testing.T) {
	t.Parallel()

	var gotPlatform, gotUDID string
	handler := viewerFetching(func(_ context.Context, platform, udid string, _ []string, _ string) (device.TreeNode, error) {
		gotPlatform, gotUDID = platform, udid
		return device.TreeNode{Attributes: map[string]string{"text": "Login"}}, nil
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hierarchy.json?platform=ios&udid=AAAA", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotPlatform != "ios" || gotUDID != "AAAA" {
		t.Fatalf("fetch got platform=%q udid=%q, want ios/AAAA", gotPlatform, gotUDID)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Login")) {
		t.Fatalf("tree not returned: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}

func TestViewerHierarchyEndpointRequiresAKnownPlatform(t *testing.T) {
	t.Parallel()

	called := false
	handler := viewerFetching(func(context.Context, string, string, []string, string) (device.TreeNode, error) {
		called = true
		return device.TreeNode{}, nil
	})

	for _, target := range []string{"/hierarchy.json", "/hierarchy.json?platform=windows"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want %d", target, rec.Code, http.StatusBadRequest)
		}
	}
	if called {
		t.Fatalf("fetch was called for an invalid platform")
	}
}

func TestViewerHierarchyEndpointSurfacesAFetchError(t *testing.T) {
	t.Parallel()

	handler := viewerFetching(func(context.Context, string, string, []string, string) (device.TreeNode, error) {
		return device.TreeNode{}, errors.New("runner not reachable")
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hierarchy.json?platform=android&udid=x", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("runner not reachable")) {
		t.Fatalf("fetch error not surfaced: %s", rec.Body.String())
	}
}

func TestViewerPageRenders(t *testing.T) {
	t.Parallel()

	handler := viewerFetching(func(context.Context, string, string, []string, string) (device.TreeNode, error) {
		return device.TreeNode{}, nil
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("FlowBaton Viewer")) {
		t.Fatalf("page did not render: %s", rec.Body.String())
	}
}

func TestViewerUnknownPathIs404(t *testing.T) {
	t.Parallel()

	handler := viewerFetching(func(context.Context, string, string, []string, string) (device.TreeNode, error) {
		return device.TreeNode{}, nil
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestStartViewerBindsAndServes(t *testing.T) {
	t.Parallel()

	addr, stop, err := startViewer(0, HierarchyRunner{
		Fetch: func(context.Context, string, string, []string, string) (device.TreeNode, error) {
			return device.TreeNode{Attributes: map[string]string{"text": "Login"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("startViewer: %v", err)
	}
	defer stop()

	resp, err := http.Get("http://" + addr + "/hierarchy.json?platform=ios&udid=AAAA")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestParseMCPArgsReadsViewerFlags(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	got, code := parseMCPArgs([]string{"--viewer-port", "41999", "--base-dir=/tmp/x"}, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d, want %d; stderr: %s", code, ExitOK, stderr.String())
	}
	if got.viewerPort != 41999 || got.baseDir != "/tmp/x" || got.noViewer {
		t.Fatalf("parsed = %+v, want port 41999, base-dir /tmp/x, noViewer false", got)
	}

	got, code = parseMCPArgs([]string{"--no-viewer"}, &stderr)
	if code != ExitOK || !got.noViewer {
		t.Fatalf("--no-viewer: parsed = %+v code = %d", got, code)
	}

	if _, code := parseMCPArgs([]string{"--viewer-port", "notaport"}, &stderr); code != ExitInvalid {
		t.Fatalf("bad port: code = %d, want %d", code, ExitInvalid)
	}
}
