package integrationv1

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestValidateJSONAcceptsCoveredAuthenticatedTransports(t *testing.T) {
	data := readHandshake(t)
	if err := ValidateJSON(data); err != nil {
		t.Fatalf("valid handshake rejected: %v", err)
	}
}

func TestValidateJSONRejectsDuplicateObjectKeys(t *testing.T) {
	data := string(readHandshake(t))
	field := `"schema_version": 1,`
	duplicate := strings.Replace(data, field, field+"\n  "+field, 1)
	if err := ValidateJSON([]byte(duplicate)); err == nil {
		t.Fatal("handshake with a duplicate schema_version was accepted")
	}
}

func TestValidateJSONRejectsMissingOrUnadvertisedAuthProfiles(t *testing.T) {
	var document handshakeDocument
	if err := decodeStrict(readHandshake(t), &document); err != nil {
		t.Fatal(err)
	}
	document.AuthProfiles = document.AuthProfiles[:1]
	if err := ValidateJSON(mustHandshakeJSON(t, document)); err == nil {
		t.Fatal("advertised remote IPC without a remote auth profile was accepted")
	}

	document.Transports = []string{"cli-stdio", "authenticated-local-ipc"}
	document.AuthProfiles = append(document.AuthProfiles, remoteCloudMacV1)
	if err := ValidateJSON(mustHandshakeJSON(t, document)); err == nil {
		t.Fatal("auth profile for an unadvertised transport was accepted")
	}
}

func TestValidateJSONRejectsDuplicateProfilesAndTransports(t *testing.T) {
	var document handshakeDocument
	if err := decodeStrict(readHandshake(t), &document); err != nil {
		t.Fatal(err)
	}
	document.AuthProfiles = append(document.AuthProfiles, document.AuthProfiles[0])
	if err := ValidateJSON(mustHandshakeJSON(t, document)); err == nil {
		t.Fatal("duplicate auth profile was accepted")
	}

	if err := decodeStrict(readHandshake(t), &document); err != nil {
		t.Fatal(err)
	}
	document.Transports = append(document.Transports, document.Transports[0])
	if err := ValidateJSON(mustHandshakeJSON(t, document)); err == nil {
		t.Fatal("duplicate transport was accepted")
	}
}

func readHandshake(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("example.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustHandshakeJSON(t *testing.T, document handshakeDocument) []byte {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
