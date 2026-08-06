package flow

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nohavewho/flowbaton/internal/model"
	"go.yaml.in/yaml/v3"
)

type mappingPair struct {
	key   *yaml.Node
	value *yaml.Node
}

func mappingPairs(path string, data []byte, node *yaml.Node, duplicateCode string) ([]mappingPair, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, diagnostic(path, "object_required", "expected an object", node, data)
	}
	if len(node.Content)%2 != 0 {
		return nil, diagnostic(path, "malformed_mapping", "object contains an unmatched key", node, data)
	}
	pairs := make([]mappingPair, 0, len(node.Content)/2)
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, value := node.Content[index], node.Content[index+1]
		isDocumentedTrueKey := key.Kind == yaml.ScalarNode && key.Tag == "!!bool" && key.Value == "true"
		if key.Kind != yaml.ScalarNode || (key.Tag != "!!str" && !isDocumentedTrueKey) {
			return nil, diagnostic(path, "object_key_type", "object keys must be strings", key, data)
		}
		if _, exists := seen[key.Value]; exists {
			return nil, diagnostic(path, duplicateCode, fmt.Sprintf("duplicate field %s", key.Value), key, data)
		}
		seen[key.Value] = struct{}{}
		pairs = append(pairs, mappingPair{key: key, value: value})
	}
	return pairs, nil
}

func normalizeNode(path string, data []byte, node *yaml.Node) (any, error) {
	return normalizeNodeActive(path, data, node, make(map[*yaml.Node]struct{}))
}

func normalizeNodeActive(path string, data []byte, node *yaml.Node, active map[*yaml.Node]struct{}) (any, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind == yaml.AliasNode {
		if _, cyclic := active[node.Alias]; cyclic {
			return nil, diagnostic(path, "cyclic_yaml_alias", "YAML alias cycle is not supported", node, data)
		}
		return normalizeNodeActive(path, data, node.Alias, active)
	}
	if _, cyclic := active[node]; cyclic {
		return nil, diagnostic(path, "cyclic_yaml_alias", "YAML alias cycle is not supported", node, data)
	}
	active[node] = struct{}{}
	defer delete(active, node)
	switch node.Kind {
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			return nil, nil
		case "!!bool":
			value, err := strconv.ParseBool(strings.ToLower(node.Value))
			if err != nil {
				return nil, diagnostic(path, "invalid_boolean", err.Error(), node, data)
			}
			return value, nil
		case "!!int":
			var value int64
			if err := node.Decode(&value); err != nil {
				return nil, diagnostic(path, "invalid_integer", err.Error(), node, data)
			}
			return value, nil
		case "!!float":
			var value float64
			if err := node.Decode(&value); err != nil {
				return nil, diagnostic(path, "invalid_number", err.Error(), node, data)
			}
			return value, nil
		default:
			return node.Value, nil
		}
	case yaml.SequenceNode:
		result := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := normalizeNodeActive(path, data, child, active)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	case yaml.MappingNode:
		pairs, err := mappingPairs(path, data, node, "duplicate_object_field")
		if err != nil {
			return nil, err
		}
		result := make(map[string]any, len(pairs))
		for _, pair := range pairs {
			value, err := normalizeNodeActive(path, data, pair.value, active)
			if err != nil {
				return nil, err
			}
			result[pair.key.Value] = value
		}
		return result, nil
	default:
		return nil, diagnostic(path, "unsupported_yaml_node", fmt.Sprintf("unsupported YAML node kind %d", node.Kind), node, data)
	}
}

func stringValue(path string, data []byte, node *yaml.Node, code, field string) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", diagnostic(path, code, fmt.Sprintf("%s must be a string", field), node, data)
	}
	return node.Value, nil
}

// scalarString accepts any scalar node and returns its textual value. This
// permits scalar coercion such as `label: 123`; maps and sequences remain invalid.
func scalarString(path string, data []byte, node *yaml.Node, code, field string) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", diagnostic(path, code, fmt.Sprintf("%s must be a scalar", field), node, data)
	}
	return node.Value, nil
}

func boolValue(path string, data []byte, node *yaml.Node, code, field string) (bool, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false, diagnostic(path, code, fmt.Sprintf("%s must be a boolean", field), node, data)
	}
	value, err := strconv.ParseBool(strings.ToLower(node.Value))
	if err != nil {
		return false, diagnostic(path, code, fmt.Sprintf("%s must be a boolean", field), node, data)
	}
	return value, nil
}

// coercedBoolValue accepts a native YAML boolean or a quoted case-insensitive
// "true" or "false" string. It rejects all other strings and non-scalars.
func coercedBoolValue(path string, data []byte, node *yaml.Node, code, field string) (bool, error) {
	if node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		switch strings.ToLower(node.Value) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return false, diagnostic(path, code, fmt.Sprintf("%s must be a boolean", field), node, data)
	}
	return boolValue(path, data, node, code, field)
}

// coercedIntValue accepts a native YAML integer or a quoted integer string. It
// rejects interpolation and non-integer strings.
func coercedIntValue(path string, data []byte, node *yaml.Node, code, field string) (int, error) {
	if node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		value, err := strconv.Atoi(node.Value)
		if err != nil {
			return 0, diagnostic(path, code, fmt.Sprintf("%s must be an integer", field), node, data)
		}
		return value, nil
	}
	return intValue(path, data, node, code, field)
}

// coercedNumberValue accepts a native YAML number or a quoted numeric string.
// It rejects interpolation and non-numeric strings.
func coercedNumberValue(path string, data []byte, node *yaml.Node, code, field string) (float64, error) {
	if node != nil && node.Kind == yaml.ScalarNode {
		switch node.Tag {
		case "!!str":
			value, err := strconv.ParseFloat(node.Value, 64)
			if err != nil {
				return 0, diagnostic(path, code, fmt.Sprintf("%s must be a number", field), node, data)
			}
			return value, nil
		case "!!int", "!!float":
			var value float64
			if err := node.Decode(&value); err == nil {
				return value, nil
			}
		}
	}
	return 0, diagnostic(path, code, fmt.Sprintf("%s must be a number", field), node, data)
}

func intValue(path string, data []byte, node *yaml.Node, code, field string) (int, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return 0, diagnostic(path, code, fmt.Sprintf("%s must be an integer", field), node, data)
	}
	var value int
	if err := node.Decode(&value); err != nil {
		return 0, diagnostic(path, code, fmt.Sprintf("%s must be an integer", field), node, data)
	}
	return value, nil
}

// stringList reads a sequence of scalars and coerces each entry to text.
// Numeric tag entries such as `tags: [42]` are valid authored values.
func stringList(path string, data []byte, node *yaml.Node, allowScalar bool, code, field string) ([]string, error) {
	if allowScalar && node != nil && node.Kind == yaml.ScalarNode {
		value, err := scalarString(path, data, node, code, field)
		if err != nil {
			return nil, err
		}
		return []string{value}, nil
	}
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, diagnostic(path, code, fmt.Sprintf("%s must be a string array", field), node, data)
	}
	result := make([]string, 0, len(node.Content))
	for _, child := range node.Content {
		value, err := scalarString(path, data, child, code, field)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func stringMap(path string, data []byte, node *yaml.Node, code, field string) (map[string]string, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, diagnostic(path, code, fmt.Sprintf("%s must be a string map", field), node, data)
	}
	pairs, err := mappingPairs(path, data, node, "duplicate_"+field+"_field")
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		// Environment values use the same scalar-to-text coercion as stringList.
		value, err := scalarString(path, data, pair.value, code, field+" value")
		if err != nil {
			return nil, err
		}
		result[pair.key.Value] = value
	}
	return result, nil
}

func isNullNode(node *yaml.Node) bool {
	return node == nil || (node.Kind == yaml.ScalarNode && node.Tag == "!!null")
}

func diagnostic(path, code, message string, node *yaml.Node, data []byte) model.Diagnostic {
	if node == nil {
		node = &yaml.Node{Line: 1, Column: 1}
	}
	return model.Diagnostic{
		Code:    code,
		Message: message,
		Source:  sourceInfo(path, node, data),
	}
}

func sourceInfo(path string, node *yaml.Node, data []byte) model.SourceInfo {
	if node == nil {
		node = &yaml.Node{Line: 1, Column: 1}
	}
	return spanSource(path, node, node, data)
}

func spanSource(path string, startNode, endNode *yaml.Node, data []byte) model.SourceInfo {
	if startNode == nil {
		startNode = &yaml.Node{Line: 1, Column: 1}
	}
	if endNode == nil {
		endNode = startNode
	}
	last := lastNode(endNode)
	start := positionFor(startNode.Line, startNode.Column, data)
	end := positionAtOffset(lexicalEndOffset(last, data), data)
	if end.Offset <= start.Offset {
		end.Offset = min(len(data), start.Offset+1)
		end = positionAtOffset(end.Offset, data)
	}
	return model.SourceInfo{Path: path, Start: start, End: end}
}

func lastNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return &yaml.Node{Line: 1, Column: 1}
	}
	last := node
	for _, child := range node.Content {
		candidate := lastNode(child)
		if candidate.Line > last.Line || (candidate.Line == last.Line && candidate.Column >= last.Column) {
			last = candidate
		}
	}
	return last
}

func positionFor(line, column int, data []byte) model.Position {
	if line < 1 {
		line = 1
	}
	if column < 1 {
		column = 1
	}
	offset := 0
	currentLine := 1
	for offset < len(data) && currentLine < line {
		if data[offset] == '\n' {
			currentLine++
		}
		offset++
	}
	for currentColumn := 1; offset < len(data) && currentColumn < column; currentColumn++ {
		if data[offset] == '\n' {
			break
		}
		_, width := utf8.DecodeRune(data[offset:])
		if width < 1 {
			width = 1
		}
		offset += width
	}
	return model.Position{Line: line, Column: column, Offset: offset}
}

func positionAtOffset(offset int, data []byte) model.Position {
	offset = max(0, min(offset, len(data)))
	line := 1
	column := 1
	for index := 0; index < offset; {
		if data[index] == '\n' {
			line++
			column = 1
			index++
			continue
		}
		_, width := utf8.DecodeRune(data[index:])
		if width < 1 {
			width = 1
		}
		if index+width > offset {
			width = offset - index
		}
		index += width
		column++
	}
	return model.Position{Line: line, Column: column, Offset: offset}
}

func lexicalEndOffset(node *yaml.Node, data []byte) int {
	if node == nil {
		return min(1, len(data))
	}
	start := positionFor(node.Line, node.Column, data).Offset
	if start >= len(data) {
		return len(data)
	}
	if node.Kind == yaml.AliasNode {
		return scanPlainScalarEnd(start, data)
	}
	if node.Kind != yaml.ScalarNode {
		return min(len(data), start+1)
	}
	switch {
	case node.Style&yaml.DoubleQuotedStyle != 0:
		return scanQuotedScalarEnd(start, data, '"')
	case node.Style&yaml.SingleQuotedStyle != 0:
		return scanQuotedScalarEnd(start, data, '\'')
	case node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		return scanBlockScalarEnd(start, node.Value, data)
	case node.Value != "" && bytes.HasPrefix(data[start:], []byte(node.Value)):
		return start + len(node.Value)
	default:
		return scanPlainScalarEnd(start, data)
	}
}

func scanQuotedScalarEnd(start int, data []byte, quote byte) int {
	if start >= len(data) || data[start] != quote {
		return scanPlainScalarEnd(start, data)
	}
	for index := start + 1; index < len(data); {
		if quote == '"' && data[index] == '\\' {
			index++
			if index < len(data) {
				_, width := utf8.DecodeRune(data[index:])
				index += max(1, width)
			}
			continue
		}
		if data[index] == quote {
			if quote == '\'' && index+1 < len(data) && data[index+1] == quote {
				index += 2
				continue
			}
			return index + 1
		}
		_, width := utf8.DecodeRune(data[index:])
		index += max(1, width)
	}
	return len(data)
}

func scanBlockScalarEnd(start int, value string, data []byte) int {
	headerEnd, nextLine := sourceLineBounds(start, data)
	headerTokenEnd := start + 1
	for headerTokenEnd < headerEnd {
		character := data[headerTokenEnd]
		if character != '+' && character != '-' && (character < '1' || character > '9') {
			break
		}
		headerTokenEnd++
	}
	if value == "" || nextLine >= len(data) {
		return headerTokenEnd
	}

	contentIndent := -1
	lastContentEnd := headerTokenEnd
	for lineStart := nextLine; lineStart < len(data); {
		lineEnd, followingLine := sourceLineBounds(lineStart, data)
		contentStart := lineStart
		for contentStart < lineEnd && data[contentStart] == ' ' {
			contentStart++
		}
		if contentStart < lineEnd {
			indent := contentStart - lineStart
			if contentIndent < 0 {
				contentIndent = indent
			}
			if indent < contentIndent {
				break
			}
			lastContentEnd = lineEnd
		} else if contentIndent >= 0 {
			lastContentEnd = lineEnd
		}
		if followingLine <= lineStart {
			break
		}
		lineStart = followingLine
	}
	return lastContentEnd
}

func sourceLineBounds(start int, data []byte) (contentEnd, nextLine int) {
	contentEnd = start
	for contentEnd < len(data) && data[contentEnd] != '\n' && data[contentEnd] != '\r' {
		contentEnd++
	}
	nextLine = contentEnd
	if nextLine < len(data) && data[nextLine] == '\r' {
		nextLine++
	}
	if nextLine < len(data) && data[nextLine] == '\n' {
		nextLine++
	}
	return contentEnd, nextLine
}

func scanPlainScalarEnd(start int, data []byte) int {
	end := start
	for end < len(data) {
		switch data[end] {
		case '\r', '\n', ',', ']', '}':
			return trimHorizontalSpace(start, end, data)
		case ':':
			if end+1 == len(data) || isYAMLSeparator(data[end+1]) {
				return trimHorizontalSpace(start, end, data)
			}
		case '#':
			if end == start || data[end-1] == ' ' || data[end-1] == '\t' {
				return trimHorizontalSpace(start, end, data)
			}
		}
		_, width := utf8.DecodeRune(data[end:])
		end += max(1, width)
	}
	return trimHorizontalSpace(start, end, data)
}

func isYAMLSeparator(character byte) bool {
	switch character {
	case ' ', '\t', '\r', '\n', ',', ']', '}':
		return true
	default:
		return false
	}
}

func trimHorizontalSpace(start, end int, data []byte) int {
	for end > start && (data[end-1] == ' ' || data[end-1] == '\t') {
		end--
	}
	return end
}
