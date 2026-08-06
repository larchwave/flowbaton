package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

// mcp_viewer is spec 03's optional Viewer HTTP server: a read-only window onto
// the current device UI hierarchy that runs alongside the MCP stdio server. It
// serves one JSON endpoint (the tree) and one HTML page that renders it. It taps
// nothing — the same HierarchyRunner the CLI and MCP `hierarchy` tool use is the
// only device access, injected so tests drive it without a device.

// viewerHandler builds the Viewer's routes over an injected hierarchy source.
// /hierarchy.json?platform=&udid= returns the tree as JSON; / renders the page.
func viewerHandler(hier HierarchyRunner) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/hierarchy.json", func(w http.ResponseWriter, r *http.Request) {
		platform := r.URL.Query().Get("platform")
		udid := r.URL.Query().Get("udid")
		switch platform {
		case "ios", "android":
		default:
			http.Error(w, "platform query parameter is required: ios or android", http.StatusBadRequest)
			return
		}
		tree, err := hier.fetch()(r.Context(), platform, udid, appIDFilter(r.URL.Query().Get("appId")))
		if err != nil {
			// The device is outside the viewer, so a fetch failure is a bad
			// gateway, not the client's fault.
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(tree); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(viewerPage))
	})

	return mux
}

// startViewer binds 127.0.0.1:<port> (port 0 → an OS-assigned free port, which
// is the spec default) and serves the viewer in a goroutine. The bind is
// synchronous so a port clash surfaces to the caller instead of vanishing into
// the goroutine. The returned stop shuts the server down cleanly.
func startViewer(port int, hier HierarchyRunner) (addr string, stop func(), err error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", nil, err
	}
	server := &http.Server{Handler: viewerHandler(hier)}
	go func() { _ = server.Serve(listener) }()
	return listener.Addr().String(), func() { _ = server.Shutdown(context.Background()) }, nil
}

// viewerPage is a self-contained page that reads platform/udid from its own
// query string, fetches the JSON tree, and renders it as indented text. No
// framework, no assets — the JSON endpoint is the real interface; this is a
// human-legible view of it.
const viewerPage = `<!doctype html>
<meta charset="utf-8">
<title>FlowBaton Viewer</title>
<body style="font-family:system-ui,sans-serif;margin:1rem">
<h1>FlowBaton Viewer</h1>
<form id="f">
  <label>platform <input name="platform" value="ios"></label>
  <label>udid <input name="udid"></label>
  <button>load hierarchy</button>
</form>
<pre id="out" style="white-space:pre-wrap;background:#f4f4f4;padding:1rem"></pre>
<script>
const f = document.getElementById('f'), out = document.getElementById('out');
const q = new URLSearchParams(location.search);
if (q.get('platform')) f.platform.value = q.get('platform');
if (q.get('udid')) f.udid.value = q.get('udid');
async function load() {
  const p = new URLSearchParams({platform: f.platform.value, udid: f.udid.value});
  out.textContent = 'loading...';
  try {
    const r = await fetch('/hierarchy.json?' + p);
    out.textContent = await r.text();
  } catch (e) { out.textContent = String(e); }
}
f.addEventListener('submit', e => { e.preventDefault(); load(); });
if (q.get('platform')) load();
</script>
</body>
`
