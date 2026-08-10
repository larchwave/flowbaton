package integrationv1

import "testing"

func TestNewDocumentProducesAValidatedPublicHandshake(t *testing.T) {
	document, err := NewDocument(Executable{Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", License: "Apache-2.0", ProcessID: 42}, []string{"authenticated-remote-ipc"}, Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []AuthProfile{RemoteCloudMacProfile()}, []string{"tap"})
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	if document.ContractVersion != "v1" || document.AuthProfiles[0].ProfileID != "remote-cloud-mac-v1" {
		t.Fatalf("NewDocument() = %#v", document)
	}
}
