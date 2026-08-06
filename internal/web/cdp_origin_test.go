package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Android WebView targets may report an empty origin. The target list succeeds
// while the WebSocket upgrade requires the browser's accepted origin:
//
//	web cdp: dialing "ws://127.0.0.1:62659/devtools/page/34042B…":
//	websocket.Dial …: bad status
//
// The endpoint's own answer, by hand, with everything else held equal:
//
//	$ curl -i … -H 'Origin: http://127.0.0.1:9444' …/devtools/page/$ID  → 403
//	$ curl -i …                     (no Origin header)  …               → upgraded
//
// DevTools refuses a handshake carrying an Origin it was not told to allow. On
// desktop Chrome we launch the browser and can pass --remote-allow-origins=*;
// an Android WebView is launched by the app under test, so there is no flag to
// pass and the only handshake it will accept is one with no Origin at all —
// which is what a non-browser client should have been sending anyway.

// acceptWithoutOrigin makes an x/net/websocket server accept the handshake a
// real DevTools endpoint accepts. That library's SERVER rejects a request with
// no Origin ("missing or bad WebSocket-Origin", hybi.go:539) where Chrome and
// Android's WebView accept exactly that one and refuse all the rest, so the
// stub, not the client, is the thing out of step with the device. It is put
// back in step by filling the header in before the upgrade.
func acceptWithoutOrigin(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Origin") == "" {
			request.Header.Set("Origin", "http://"+request.Host)
		}
		handler.ServeHTTP(writer, request)
	})
}

// The handshake request itself is what gets asserted, rather than a server
// that upgrades only without an Origin: x/net/websocket's SERVER refuses an
// Origin-less handshake ("missing or bad WebSocket-Origin"), which no Go stub
// can talk us out of, so a stub cannot model the device. The device's answer is
// recorded above; what this repo controls is the header it sends.
func TestTheHandshakeCarriesNoOrigin(t *testing.T) {
	t.Parallel()

	seen := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			select {
			case seen <- request.Header.Clone():
			default:
			}
			// 403 is the device's own answer to an unwanted Origin, so the dial
			// fails either way; the header is the assertion.
			http.Error(writer, "forbidden origin", http.StatusForbidden)
		}))
	t.Cleanup(server.Close)
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/ABC"

	if _, err := dialEndpoint(context.Background(), endpoint); err == nil {
		t.Fatal("dialEndpoint() succeeded against a 403")
	}
	header := <-seen
	if origin := header.Get("Origin"); origin != "" {
		t.Fatalf("the handshake sent Origin: %q; a live Android WebView answers 403 to any", origin)
	}
	// The rest of the handshake still has to be a handshake, or the assertion
	// above would pass on a request that never tried to upgrade.
	if got := strings.ToLower(header.Get("Upgrade")); got != "websocket" {
		t.Fatalf("Upgrade = %q, want the websocket handshake", got)
	}
}
