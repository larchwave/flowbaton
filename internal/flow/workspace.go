package flow

import (
	"fmt"
	"io"

	"github.com/nohavewho/flowbaton/internal/model"
	"go.yaml.in/yaml/v3"
)

// ParseWorkspace reads a single workspace configuration document.
func ParseWorkspace(path string, reader io.Reader) (model.WorkspaceConfig, []model.Diagnostic, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return model.WorkspaceConfig{}, nil, diagnostic(path, "read_error", fmt.Sprintf("read workspace config: %v", err), nil, nil)
	}
	return ParseWorkspaceBytes(path, data)
}

// ParseWorkspaceBytes parses a single workspace configuration document.
func ParseWorkspaceBytes(path string, data []byte) (model.WorkspaceConfig, []model.Diagnostic, error) {
	documents, err := decodeDocuments(path, data)
	if err != nil {
		return model.WorkspaceConfig{}, nil, err
	}
	if len(documents) != 1 {
		return model.WorkspaceConfig{}, nil, diagnostic(
			path,
			"invalid_workspace_document_count",
			fmt.Sprintf("workspace config must contain one YAML document; found %d", len(documents)),
			firstDocumentNode(documents),
			data,
		)
	}
	root := documents[0]
	if root.Kind != yaml.MappingNode {
		return model.WorkspaceConfig{}, nil, diagnostic(path, "workspace_object_required", "workspace config must be an object", root, data)
	}
	pairs, err := mappingPairs(path, data, root, "duplicate_workspace_field")
	if err != nil {
		return model.WorkspaceConfig{}, nil, err
	}
	workspace := model.WorkspaceConfig{
		Flows:   []string{"*"},
		Unknown: make(map[string]any),
		Source:  sourceInfo(path, root, data),
	}
	warnings := make([]model.Diagnostic, 0)
	for _, pair := range pairs {
		switch pair.key.Value {
		case "flows":
			workspace.Flows, err = stringList(path, data, pair.value, true, "workspace_field_type", "flows")
		case "includeTags":
			workspace.IncludeTags, err = stringList(path, data, pair.value, true, "workspace_field_type", "includeTags")
		case "excludeTags":
			workspace.ExcludeTags, err = stringList(path, data, pair.value, true, "workspace_field_type", "excludeTags")
		case "executionOrder":
			workspace.ExecutionOrder, err = parseExecutionOrder(path, data, pair.value)
		case "targetBranch":
			workspace.TargetBranch, err = stringValue(path, data, pair.value, "workspace_field_type", "targetBranch")
		case "notifications":
			var value any
			value, err = normalizeNode(path, data, pair.value)
			if err == nil {
				var ok bool
				workspace.Notifications, ok = value.(map[string]any)
				if !ok {
					err = diagnostic(path, "workspace_field_type", "notifications must be an object", pair.value, data)
				}
			}
		case "disableRetries":
			var value bool
			value, err = boolValue(path, data, pair.value, "workspace_field_type", "disableRetries")
			workspace.DisableRetries = &value
		case "platform":
			workspace.Platform, err = parseWorkspacePlatform(path, data, pair.value)
		case "testOutputDir":
			workspace.TestOutputDir, err = stringValue(path, data, pair.value, "workspace_field_type", "testOutputDir")
		default:
			workspace.Unknown[pair.key.Value], err = normalizeNode(path, data, pair.value)
			warnings = append(warnings, model.Diagnostic{
				Code:    "unknown_workspace_key",
				Message: fmt.Sprintf("unknown workspace key %s", pair.key.Value),
				Source:  sourceInfo(path, pair.key, data),
			})
		}
		if err != nil {
			return model.WorkspaceConfig{}, nil, err
		}
	}
	if len(workspace.Unknown) == 0 {
		workspace.Unknown = nil
	}
	return workspace, warnings, nil
}

func parseExecutionOrder(path string, data []byte, node *yaml.Node) (*model.WorkspaceExecutionOrder, error) {
	pairs, err := mappingPairs(path, data, node, "duplicate_execution_order_field")
	if err != nil {
		return nil, err
	}
	result := &model.WorkspaceExecutionOrder{}
	for _, pair := range pairs {
		switch pair.key.Value {
		case "continueOnFailure":
			result.ContinueOnFailure, err = boolValue(path, data, pair.value, "workspace_field_type", "continueOnFailure")
		case "flowsOrder":
			result.FlowsOrder, err = stringList(path, data, pair.value, false, "workspace_field_type", "flowsOrder")
		default:
			return nil, diagnostic(path, "unknown_execution_order_field", fmt.Sprintf("unknown executionOrder field %s", pair.key.Value), pair.key, data)
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func parseWorkspacePlatform(path string, data []byte, node *yaml.Node) (*model.WorkspacePlatformConfig, error) {
	pairs, err := mappingPairs(path, data, node, "duplicate_workspace_platform_field")
	if err != nil {
		return nil, err
	}
	result := &model.WorkspacePlatformConfig{}
	for _, pair := range pairs {
		switch pair.key.Value {
		case "android":
			result.Android, err = parseAndroidWorkspace(path, data, pair.value)
		case "ios":
			result.IOS, err = parseIOSWorkspace(path, data, pair.value)
		default:
			return nil, diagnostic(path, "unknown_workspace_platform", fmt.Sprintf("unknown workspace platform %s", pair.key.Value), pair.key, data)
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func parseAndroidWorkspace(path string, data []byte, node *yaml.Node) (*model.WorkspaceAndroidConfig, error) {
	pairs, err := mappingPairs(path, data, node, "duplicate_android_workspace_field")
	if err != nil {
		return nil, err
	}
	result := &model.WorkspaceAndroidConfig{}
	for _, pair := range pairs {
		if pair.key.Value != "disableAnimations" {
			return nil, diagnostic(path, "unknown_android_workspace_field", fmt.Sprintf("unknown Android workspace field %s", pair.key.Value), pair.key, data)
		}
		result.DisableAnimations, err = boolValue(path, data, pair.value, "workspace_field_type", "disableAnimations")
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func parseIOSWorkspace(path string, data []byte, node *yaml.Node) (*model.WorkspaceIOSConfig, error) {
	pairs, err := mappingPairs(path, data, node, "duplicate_ios_workspace_field")
	if err != nil {
		return nil, err
	}
	result := &model.WorkspaceIOSConfig{}
	for _, pair := range pairs {
		switch pair.key.Value {
		case "disableAnimations":
			result.DisableAnimations, err = boolValue(path, data, pair.value, "workspace_field_type", "disableAnimations")
		case "snapshotKeyHonorModalViews":
			result.SnapshotKeyHonorModalViews, err = boolValue(path, data, pair.value, "workspace_field_type", "snapshotKeyHonorModalViews")
		default:
			return nil, diagnostic(path, "unknown_ios_workspace_field", fmt.Sprintf("unknown iOS workspace field %s", pair.key.Value), pair.key, data)
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
