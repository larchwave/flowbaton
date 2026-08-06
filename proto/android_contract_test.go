package proto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type androidDescriptor struct {
	SchemaVersion    int                     `json:"schema_version"`
	ContractVersion  string                  `json:"contract_version"`
	DeclarationBasis androidDeclarationBasis `json:"declaration_basis"`
	Proto            protoIdentity           `json:"proto"`
	RPCs             []rpcDescriptor         `json:"rpcs"`
	Messages         []messageShape          `json:"messages"`
	ErrorContract    grpcErrorContract       `json:"error_contract"`
}

type androidDeclarationBasis struct {
	ContractKind     string            `json:"contract_kind"`
	Specifications   []string          `json:"specifications"`
	ProductDecisions []productDecision `json:"product_decisions"`
}

type productDecision struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
}

type protoIdentity struct {
	File    string `json:"file"`
	Syntax  string `json:"syntax"`
	Package string `json:"package"`
	Service string `json:"service"`
}

type rpcDescriptor struct {
	Name            string `json:"name"`
	Request         string `json:"request"`
	Response        string `json:"response"`
	ClientStreaming bool   `json:"client_streaming"`
	ServerStreaming bool   `json:"server_streaming"`
}

type messageShape struct {
	Name   string          `json:"name"`
	Fields []protobufField `json:"fields"`
}

type protobufField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Number   int    `json:"number"`
	Repeated bool   `json:"repeated"`
}

type grpcErrorContract struct {
	HandlerStatus     string   `json:"handler_status"`
	Trailers          []string `json:"trailers"`
	TransportStatuses []string `json:"transport_statuses"`
}

func TestAndroidV0ProtoMatchesMachineReadableDescriptor(t *testing.T) {
	descriptorPath := filepath.Join("..", "contracts", "v0", "android-grpc.json")
	descriptorData, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatalf("read Android descriptor: %v", err)
	}
	var want androidDescriptor
	decoder := json.NewDecoder(strings.NewReader(string(descriptorData)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&want); err != nil {
		t.Fatalf("decode Android descriptor: %v", err)
	}

	protoData, err := os.ReadFile("flowbaton_android.proto")
	if err != nil {
		t.Fatalf("read canonical Android proto: %v", err)
	}
	got := parseCanonicalProto(t, string(protoData))

	if want.SchemaVersion != 1 || want.ContractVersion != "v0" {
		t.Fatalf("descriptor version = schema %d contract %q, want 1/v0", want.SchemaVersion, want.ContractVersion)
	}
	if want.DeclarationBasis.ContractKind != "flowbaton-wire-contract" || !reflect.DeepEqual(want.DeclarationBasis.Specifications, []string{"specs/04-wire-protocols.md section 1"}) {
		t.Fatalf("descriptor declaration basis = %#v, want FlowBaton contract specifications", want.DeclarationBasis)
	}
	assertArgumentValueDecisionIsExplicit(t, want.DeclarationBasis.ProductDecisions)

	if !reflect.DeepEqual(got, androidDescriptor{
		Proto:    want.Proto,
		RPCs:     want.RPCs,
		Messages: want.Messages,
	}) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantShape, _ := json.MarshalIndent(androidDescriptor{Proto: want.Proto, RPCs: want.RPCs, Messages: want.Messages}, "", "  ")
		t.Fatalf("canonical proto drifted from descriptor\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantShape)
	}

	if len(want.RPCs) != 12 {
		t.Fatalf("RPC count = %d, want exactly 12", len(want.RPCs))
	}
	streaming := 0
	for _, rpc := range want.RPCs {
		if rpc.ServerStreaming {
			t.Fatalf("RPC %s unexpectedly enables server streaming", rpc.Name)
		}
		if rpc.ClientStreaming {
			streaming++
			if rpc.Name != "addMedia" {
				t.Fatalf("RPC %s is client-streaming; only addMedia may stream", rpc.Name)
			}
		}
	}
	if streaming != 1 {
		t.Fatalf("client-streaming RPC count = %d, want exactly addMedia", streaming)
	}
}

func TestAndroidV0ErrorContractIsExact(t *testing.T) {
	descriptorData, err := os.ReadFile(filepath.Join("..", "contracts", "v0", "android-grpc.json"))
	if err != nil {
		t.Fatalf("read Android descriptor: %v", err)
	}
	var descriptor androidDescriptor
	if err := json.Unmarshal(descriptorData, &descriptor); err != nil {
		t.Fatalf("decode Android descriptor: %v", err)
	}
	want := grpcErrorContract{
		HandlerStatus:     "INTERNAL",
		Trailers:          []string{"error-type", "error-message", "error-cause"},
		TransportStatuses: []string{"UNAVAILABLE", "DEADLINE_EXCEEDED"},
	}
	if !reflect.DeepEqual(descriptor.ErrorContract, want) {
		t.Fatalf("error contract = %#v, want %#v", descriptor.ErrorContract, want)
	}
}

func parseCanonicalProto(t *testing.T, source string) androidDescriptor {
	t.Helper()
	withoutComments := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(source, "")

	syntaxPattern := regexp.MustCompile(`(?m)^\s*syntax\s*=\s*"([^"]+)"\s*;`)
	packagePattern := regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;`)
	servicePattern := regexp.MustCompile(`(?s)service\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{(.*?)\}`)
	messagePattern := regexp.MustCompile(`(?s)message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{(.*?)\}`)

	syntax := captureOne(t, withoutComments, syntaxPattern.String())
	packageName := captureOne(t, withoutComments, packagePattern.String())
	serviceMatches := servicePattern.FindAllStringSubmatch(withoutComments, -1)
	if len(serviceMatches) != 1 {
		t.Fatalf("canonical proto service count = %d, want exactly 1", len(serviceMatches))
	}
	serviceMatch := serviceMatches[0]

	rpcPattern := regexp.MustCompile(`rpc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*(stream\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\)\s*returns\s*\(\s*(stream\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\)\s*;`)
	rpcMatches := rpcPattern.FindAllStringSubmatch(serviceMatch[2], -1)
	remainingService := strings.TrimSpace(rpcPattern.ReplaceAllString(serviceMatch[2], ""))
	if remainingService != "" {
		t.Fatalf("unparsed service declaration content: %q", remainingService)
	}
	rpcs := make([]rpcDescriptor, 0, len(rpcMatches))
	for _, match := range rpcMatches {
		rpcs = append(rpcs, rpcDescriptor{
			Name:            match[1],
			Request:         match[3],
			Response:        match[5],
			ClientStreaming: strings.TrimSpace(match[2]) == "stream",
			ServerStreaming: strings.TrimSpace(match[4]) == "stream",
		})
	}

	fieldPattern := regexp.MustCompile(`(?m)^\s*(repeated\s+)?([A-Za-z_][A-Za-z0-9_.]*)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([0-9]+)\s*;\s*$`)
	messageMatches := messagePattern.FindAllStringSubmatch(withoutComments, -1)
	messages := make([]messageShape, 0, len(messageMatches))
	for _, messageMatch := range messageMatches {
		fieldMatches := fieldPattern.FindAllStringSubmatch(messageMatch[2], -1)
		remainingMessage := strings.TrimSpace(fieldPattern.ReplaceAllString(messageMatch[2], ""))
		if remainingMessage != "" {
			t.Fatalf("message %s has unparsed content: %q", messageMatch[1], remainingMessage)
		}
		fields := make([]protobufField, 0, len(fieldMatches))
		seenNumbers := make(map[int]string, len(fieldMatches))
		for _, fieldMatch := range fieldMatches {
			number, err := strconv.Atoi(fieldMatch[4])
			if err != nil {
				t.Fatalf("parse %s.%s field number: %v", messageMatch[1], fieldMatch[3], err)
			}
			if previous, exists := seenNumbers[number]; exists {
				t.Fatalf("message %s reuses field %d for %s and %s", messageMatch[1], number, previous, fieldMatch[3])
			}
			seenNumbers[number] = fieldMatch[3]
			fields = append(fields, protobufField{
				Name:     fieldMatch[3],
				Type:     fieldMatch[2],
				Number:   number,
				Repeated: strings.TrimSpace(fieldMatch[1]) == "repeated",
			})
		}
		messages = append(messages, messageShape{Name: messageMatch[1], Fields: fields})
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].Name < messages[j].Name })

	topLevelRemainder := syntaxPattern.ReplaceAllString(withoutComments, "")
	topLevelRemainder = packagePattern.ReplaceAllString(topLevelRemainder, "")
	topLevelRemainder = servicePattern.ReplaceAllString(topLevelRemainder, "")
	topLevelRemainder = messagePattern.ReplaceAllString(topLevelRemainder, "")
	if remainder := strings.TrimSpace(topLevelRemainder); remainder != "" {
		t.Fatalf("canonical proto has unparsed top-level content: %q", remainder)
	}

	return androidDescriptor{
		Proto: protoIdentity{
			File:    "proto/flowbaton_android.proto",
			Syntax:  syntax,
			Package: packageName,
			Service: serviceMatch[1],
		},
		RPCs:     rpcs,
		Messages: messages,
	}
}

func captureOne(t *testing.T, source, pattern string) string {
	t.Helper()
	match := regexp.MustCompile(pattern).FindStringSubmatch(source)
	if match == nil {
		t.Fatalf("source did not match %s", pattern)
	}
	return match[1]
}

func assertArgumentValueDecisionIsExplicit(t *testing.T, decisions []productDecision) {
	t.Helper()
	for _, decision := range decisions {
		if decision.ID == "argument-value-field-numbers" {
			for _, fragment := range []string{"key=1", "value=2", "type=3"} {
				if !strings.Contains(decision.Statement, fragment) {
					t.Fatalf("ArgumentValue product decision %q omits %q", decision.Statement, fragment)
				}
			}
			return
		}
	}
	t.Fatal("missing explicit ArgumentValue field-number product decision")
}
