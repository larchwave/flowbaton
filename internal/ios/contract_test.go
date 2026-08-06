package ios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The route table in client_test.go is hand-written, which is exactly the
// thing that rots when the contract moves. This reads the frozen contract and
// holds the two together: every declared route must be exercised, with the
// declared method and path, and every exercised route must be declared.
//
// The screenshot route is exercised by its own test rather than the table,
// because it is the one route whose request is a query string and whose
// response is bytes rather than JSON.

func TestRouteTableCoversTheFrozenContract(t *testing.T) {
	t.Parallel()

	contract := loadIOSContract(t)
	declared := make(map[string]contractRoute, len(contract.Routes))
	for _, route := range contract.Routes {
		declared[route.Name] = route
	}
	exercised := make(map[string]bool, len(declared))
	for _, test := range routeCases() {
		route, ok := declared[routeNameOf(test)]
		if !ok {
			t.Fatalf("route case %q exercises %s %s, which the contract does not declare",
				test.name, test.wantMethod, test.wantPath)
		}
		if test.wantMethod != route.Method || test.wantPath != route.Path {
			t.Fatalf("route %s: case sends %s %s, contract declares %s %s",
				route.Name, test.wantMethod, test.wantPath, route.Method, route.Path)
		}
		exercised[route.Name] = true
	}
	exercised["screenshot"] = true

	var missing []string
	for name := range declared {
		if !exercised[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("contract routes with no client coverage: %v", missing)
	}
}

func TestTransportMatchesTheFrozenContract(t *testing.T) {
	t.Parallel()

	contract := loadIOSContract(t)
	if contract.Transport.DefaultPort != DefaultPort {
		t.Fatalf("DefaultPort = %d, contract declares %d", DefaultPort, contract.Transport.DefaultPort)
	}
	want := contract.Transport.Scheme + "://" + contract.Transport.BindHost + ":22087"
	if got := DefaultBaseURL(DefaultPort); got != want {
		t.Fatalf("DefaultBaseURL() = %q, want %q from the contract transport block", got, want)
	}
}

func TestErrorCodesMatchTheFrozenContract(t *testing.T) {
	t.Parallel()

	contract := loadIOSContract(t)
	for _, mapping := range contract.ErrorContract.Mappings {
		if got := codeForStatus(mapping.HTTPStatus); string(got) != mapping.Code {
			t.Fatalf("status %d maps to %q, contract declares %q", mapping.HTTPStatus, got, mapping.Code)
		}
	}
	// Every timeout signature the contract pins is non-retryable, so the code
	// they surface as must be non-retryable too.
	for _, signature := range contract.ErrorContract.TimeoutSignatures {
		if signature.Retryable {
			t.Fatalf("contract declares a retryable timeout signature %#v; the client assumes none exist", signature)
		}
	}
	if (&Error{Code: CodeTimeout}).Retryable() {
		t.Fatal("timeout errors report as retryable, contradicting every pinned signature")
	}
}

// routeNameOf derives the contract route name from a case's path. The table's
// case names are prose, so the path is the stable join key.
func routeNameOf(test routeCase) string {
	return test.wantPath[1:]
}

type contractRoute struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

type iosContract struct {
	Transport struct {
		Scheme      string `json:"scheme"`
		BindHost    string `json:"bind_host"`
		DefaultPort int    `json:"default_port"`
	} `json:"transport"`
	Routes        []contractRoute `json:"routes"`
	ErrorContract struct {
		Mappings []struct {
			HTTPStatus int    `json:"http_status"`
			Code       string `json:"code"`
		} `json:"mappings"`
		TimeoutSignatures []struct {
			Domain    string `json:"domain"`
			Code      int    `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"timeout_signatures"`
	} `json:"error_contract"`
}

func loadIOSContract(t testing.TB) iosContract {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "contracts", "v0", "ios-http.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ios contract: %v", err)
	}
	var contract iosContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("decode ios contract: %v", err)
	}
	if len(contract.Routes) == 0 {
		t.Fatal("ios contract declares no routes")
	}
	return contract
}
