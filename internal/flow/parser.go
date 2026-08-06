// Package flow parses FlowBaton's isolated YAML contracts into model nodes.
package flow

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/model"
	"go.yaml.in/yaml/v3"
)

var yamlErrorLinePattern = regexp.MustCompile(`line ([0-9]+)`)

// Parse reads exactly one two-document flow from reader. It performs syntax
// parsing only; linked paths are retained for later graph validation.
func Parse(path string, reader io.Reader) (model.Flow, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return model.Flow{}, diagnostic(path, "read_error", fmt.Sprintf("read flow: %v", err), nil, nil)
	}
	return ParseBytes(path, data)
}

// ParseBytes parses exactly one two-document flow from data.
func ParseBytes(path string, data []byte) (model.Flow, error) {
	documents, err := decodeDocuments(path, data)
	if err != nil {
		return model.Flow{}, err
	}
	if len(documents) != 2 {
		return model.Flow{}, diagnostic(
			path,
			"invalid_document_count",
			fmt.Sprintf("flow must contain exactly two YAML documents; found %d", len(documents)),
			firstDocumentNode(documents),
			data,
		)
	}
	if documents[0].Kind != yaml.MappingNode {
		return model.Flow{}, diagnostic(path, "config_object_required", "first YAML document must be a config object", documents[0], data)
	}
	if documents[1].Kind != yaml.SequenceNode {
		return model.Flow{}, diagnostic(path, "commands_array_required", "second YAML document must be a command array", documents[1], data)
	}

	config, err := parseConfig(path, data, documents[0])
	if err != nil {
		return model.Flow{}, err
	}
	commands, err := parseCommandSequence(path, data, documents[1])
	if err != nil {
		return model.Flow{}, err
	}

	return model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          path,
		Config:        config,
		Commands:      commands,
		Source:        spanSource(path, documents[0], documents[1], data),
	}, nil
}

func decodeDocuments(path string, data []byte) ([]*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	documents := make([]*yaml.Node, 0, 2)
	for {
		var document yaml.Node
		err := decoder.Decode(&document)
		if err == io.EOF {
			break
		}
		if err != nil {
			line := 1
			if match := yamlErrorLinePattern.FindStringSubmatch(err.Error()); len(match) == 2 {
				if parsed, parseErr := strconv.Atoi(match[1]); parseErr == nil {
					line = parsed
				}
			}
			source := &yaml.Node{Line: line, Column: 1}
			return nil, diagnostic(path, "malformed_yaml", err.Error(), source, data)
		}
		if len(document.Content) == 0 {
			documents = append(documents, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Line: 1, Column: 1})
			continue
		}
		documents = append(documents, document.Content[0])
	}
	return documents, nil
}

func firstDocumentNode(documents []*yaml.Node) *yaml.Node {
	if len(documents) == 0 {
		return &yaml.Node{Line: 1, Column: 1}
	}
	return documents[0]
}

func parseConfig(path string, data []byte, node *yaml.Node) (model.Config, error) {
	pairs, err := mappingPairs(path, data, node, "duplicate_config_field")
	if err != nil {
		return model.Config{}, err
	}
	config := model.Config{
		Ext:          make(map[string]any),
		Source:       sourceInfo(path, node, data),
		FieldSources: make(map[string]model.SourceInfo, len(pairs)),
	}
	var aliasAppID string
	// An explicitly empty string is a present app target. A null value is missing.
	hasAppTarget := false
	for _, pair := range pairs {
		config.FieldSources[pair.key.Value] = sourceInfo(path, pair.key, data)
		switch pair.key.Value {
		case "name":
			config.Name, err = scalarString(path, data, pair.value, "config_field_type", "name")
		case "appId":
			hasAppTarget = hasAppTarget || !isNullNode(pair.value)
			config.AppID, err = scalarString(path, data, pair.value, "config_field_type", "appId")
		case "_appId":
			hasAppTarget = hasAppTarget || !isNullNode(pair.value)
			aliasAppID, err = scalarString(path, data, pair.value, "config_field_type", "_appId")
		case "url":
			hasAppTarget = hasAppTarget || !isNullNode(pair.value)
			config.URL, err = scalarString(path, data, pair.value, "config_field_type", "url")
		case "tags":
			config.Tags, err = stringList(path, data, pair.value, false, "config_field_type", "tags")
		case "env":
			config.Env, err = stringMap(path, data, pair.value, "config_field_type", "env")
		case "onFlowStart":
			config.OnFlowStart, err = parseCommandSequence(path, data, pair.value)
		case "onFlowComplete":
			config.OnFlowComplete, err = parseCommandSequence(path, data, pair.value)
		case "properties":
			config.Properties, err = stringMap(path, data, pair.value, "config_field_type", "properties")
		case "jsEngine":
			config.Ext[pair.key.Value], err = scalarString(path, data, pair.value, "config_field_type", "jsEngine")
		default:
			config.Ext[pair.key.Value], err = normalizeNode(path, data, pair.value)
		}
		if err != nil {
			return model.Config{}, err
		}
	}
	if config.AppID == "" {
		config.AppID = aliasAppID
	}
	// A blank app target is present but empty. Commands that need an app validate
	// the value at execution; the flow header must still declare an app-target key.
	if !hasAppTarget {
		return model.Config{}, diagnostic(path, "missing_app_target", "config requires appId, _appId, or url", node, data)
	}
	if len(config.Ext) == 0 {
		config.Ext = nil
	}
	return config, nil
}

func parseCommandSequence(path string, data []byte, node *yaml.Node) ([]model.Command, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, diagnostic(path, "commands_array_required", "commands must be an array", node, data)
	}
	commands := make([]model.Command, 0, len(node.Content))
	for _, child := range node.Content {
		command, err := parseCommand(path, data, child)
		if err != nil {
			return nil, err
		}
		commands = append(commands, command)
	}
	return commands, nil
}

func parseCommand(path string, data []byte, node *yaml.Node) (model.Command, error) {
	if node == nil || isNullNode(node) {
		return model.Command{}, diagnostic(path, "empty_command", "command entry must not be empty", node, data)
	}
	if node.Kind == yaml.ScalarNode {
		if node.Tag != "!!str" {
			return model.Command{}, diagnostic(path, "command_type_required", "command must be a keyword string or a one-field object", node, data)
		}
		keyword, suggestion, ok := commandKeyword(node.Value)
		if !ok {
			return model.Command{}, model.Diagnostic{
				Code:       "unknown_command",
				Message:    fmt.Sprintf("unknown command %s", node.Value),
				Suggestion: suggestion,
				Source:     sourceInfo(path, node, data),
			}
		}
		if err := validateBareCommand(path, data, keyword, node); err != nil {
			return model.Command{}, err
		}
		return model.Command{
			Kind:   keyword,
			Form:   model.CommandFormScalar,
			Source: sourceInfo(path, node, data),
		}, nil
	}
	if node.Kind != yaml.MappingNode {
		return model.Command{}, diagnostic(path, "command_type_required", "command must be a keyword string or a one-field object", node, data)
	}
	if len(node.Content) == 0 {
		return model.Command{}, diagnostic(path, "empty_command", "command object must not be empty", node, data)
	}
	if len(node.Content) != 2 {
		return model.Command{}, diagnostic(path, "multiple_command_fields", "command object must contain exactly one command field", node, data)
	}
	key, value := node.Content[0], node.Content[1]
	if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
		return model.Command{}, diagnostic(path, "command_type_required", "command name must be a string", key, data)
	}
	keyword, suggestion, ok := commandKeyword(key.Value)
	if !ok {
		return model.Command{}, model.Diagnostic{
			Code:       "unknown_command",
			Message:    fmt.Sprintf("unknown command %s", key.Value),
			Suggestion: suggestion,
			Source:     sourceInfo(path, key, data),
		}
	}
	if err := validateCommandValue(path, data, keyword, value); err != nil {
		return model.Command{}, err
	}
	arguments, err := normalizeNode(path, data, value)
	if err != nil {
		return model.Command{}, err
	}
	command := model.Command{
		Kind:      keyword,
		Form:      model.CommandFormObject,
		Arguments: coerceScalarText(keyword, value, arguments),
		Source:    spanSource(path, key, value, data),
	}
	if err := populateTypedCommand(path, data, value, &command); err != nil {
		return model.Command{}, err
	}
	return command, nil
}

func populateTypedCommand(path string, data []byte, value *yaml.Node, command *model.Command) error {
	if isSelectorCommand(command.Kind) {
		selector, err := parseSelector(path, data, value)
		if err != nil {
			return err
		}
		command.Selector = selector
		command.Optional = selector.Optional
		command.Label = selector.Label
	}

	if value.Kind == yaml.MappingNode {
		pairs, err := mappingPairs(path, data, value, "duplicate_command_argument")
		if err != nil {
			return err
		}
		for _, pair := range pairs {
			switch pair.key.Value {
			case "element":
				if command.Kind == model.CommandScrollUntilVisible {
					command.Selector, err = parseSelector(path, data, pair.value)
				}
			case "cropOn":
				if command.Kind == model.CommandAssertScreenshot {
					command.Selector, err = parseSelector(path, data, pair.value)
				}
			case "from":
				if command.Kind == model.CommandSwipe {
					command.Selector, err = parseSelector(path, data, pair.value)
				}
			case "visible", "notVisible":
				if command.Kind == model.CommandExtendedWaitUntil {
					if command.Condition == nil {
						command.Condition = &model.Condition{
							Source:       sourceInfo(path, value, data),
							FieldSources: make(map[string]model.SourceInfo),
						}
					}
					command.Condition.FieldSources[pair.key.Value] = sourceInfo(path, pair.key, data)
					if pair.key.Value == "visible" {
						command.Condition.Visible, err = parseSelector(path, data, pair.value)
					} else {
						command.Condition.NotVisible, err = parseSelector(path, data, pair.value)
					}
				}
			case "when":
				if command.Kind == model.CommandRunFlow || command.Kind == model.CommandRunScript {
					command.Condition, err = parseCondition(path, data, pair.value)
				}
			case "while":
				if command.Kind == model.CommandRepeat {
					command.Condition, err = parseCondition(path, data, pair.value)
				}
			case "commands":
				if command.Kind == model.CommandRepeat || command.Kind == model.CommandRetry || command.Kind == model.CommandRunFlow {
					command.Children, err = parseCommandSequence(path, data, pair.value)
				}
			case "label":
				if !isSelectorCommand(command.Kind) {
					var label string
					label, err = scalarString(path, data, pair.value, "command_argument_type", "label")
					command.Label = &label
				}
			case "optional":
				if !isSelectorCommand(command.Kind) {
					var optional bool
					optional, err = coercedBoolValue(path, data, pair.value, "command_argument_type", "optional")
					command.Optional = &optional
				}
			}
			if err != nil {
				return err
			}
		}
	}

	links, err := commandLinks(path, data, command.Kind, value)
	if err != nil {
		return err
	}
	command.Links = links
	return nil
}

func isSelectorCommand(keyword model.CommandKeyword) bool {
	switch keyword {
	case model.CommandTapOn,
		model.CommandDoubleTapOn,
		model.CommandLongPressOn,
		model.CommandAssertVisible,
		model.CommandAssertNotVisible,
		model.CommandCopyTextFrom:
		return true
	default:
		return false
	}
}

func commandLinks(path string, data []byte, keyword model.CommandKeyword, value *yaml.Node) ([]model.FileLink, error) {
	switch keyword {
	case model.CommandRunFlow, model.CommandRetry:
		return linkFromScalarOrField(path, data, model.FileLinkFlow, value, "file")
	case model.CommandRunScript:
		return linkFromScalarOrField(path, data, model.FileLinkScript, value, "file")
	case model.CommandAddMedia:
		return mediaLinks(path, data, value)
	default:
		return nil, nil
	}
}

func linkFromScalarOrField(path string, data []byte, kind model.FileLinkKind, node *yaml.Node, field string) ([]model.FileLink, error) {
	if isNullNode(node) {
		return nil, nil
	}
	if node.Kind == yaml.ScalarNode {
		link, err := fileLink(path, data, kind, node)
		if err != nil {
			return nil, err
		}
		return []model.FileLink{link}, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, nil
	}
	pairs, err := mappingPairs(path, data, node, "duplicate_command_argument")
	if err != nil {
		return nil, err
	}
	for _, pair := range pairs {
		if pair.key.Value == field {
			link, err := fileLink(path, data, kind, pair.value)
			if err != nil {
				return nil, err
			}
			return []model.FileLink{link}, nil
		}
	}
	return nil, nil
}

func mediaLinks(path string, data []byte, node *yaml.Node) ([]model.FileLink, error) {
	if node.Kind == yaml.ScalarNode {
		link, err := fileLink(path, data, model.FileLinkMedia, node)
		if err != nil {
			return nil, err
		}
		return []model.FileLink{link}, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, nil
	}
	links := make([]model.FileLink, 0, len(node.Content))
	for _, child := range node.Content {
		link, err := fileLink(path, data, model.FileLinkMedia, child)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, nil
}

func fileLink(path string, data []byte, kind model.FileLinkKind, node *yaml.Node) (model.FileLink, error) {
	raw, err := stringValue(path, data, node, "file_link_type", string(kind))
	if err != nil {
		return model.FileLink{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return model.FileLink{}, diagnostic(path, "empty_file_link", "file link must not be empty", node, data)
	}
	resolved := filepath.Clean(raw)
	if !filepath.IsAbs(resolved) && path != "" && path != "-" {
		resolved = filepath.Join(filepath.Dir(path), resolved)
	}
	return model.FileLink{
		Kind:         kind,
		Path:         raw,
		ResolvedPath: resolved,
		Source:       sourceInfo(path, node, data),
	}, nil
}

func commandKeyword(value string) (model.CommandKeyword, string, bool) {
	keywords := model.CommandKeywords()
	best := ""
	bestDistance := -1
	for _, keyword := range keywords {
		candidate := string(keyword)
		if value == candidate {
			return keyword, "", true
		}
		distance := levenshtein(value, candidate)
		if bestDistance == -1 || distance < bestDistance {
			bestDistance = distance
			best = candidate
		}
	}
	if bestDistance > 3 {
		best = ""
	}
	return "", best, false
}

func levenshtein(left, right string) int {
	leftRunes, rightRunes := []rune(left), []rune(right)
	previous := make([]int, len(rightRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range leftRunes {
		current := make([]int, len(rightRunes)+1)
		current[0] = leftIndex + 1
		for rightIndex, rightRune := range rightRunes {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			deletion := previous[rightIndex+1] + 1
			insertion := current[rightIndex] + 1
			substitution := previous[rightIndex] + cost
			current[rightIndex+1] = min(deletion, insertion, substitution)
		}
		previous = current
	}
	return previous[len(rightRunes)]
}
