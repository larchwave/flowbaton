package research

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/hierarchy"
)

const workerSystemPrompt = "You map mobile application screens for automated exploratory testing. " +
	"You answer with a single JSON object and nothing else: no prose, no code fences."

func workerTaskPrompt(screenKey, table string) string {
	return "Current screen: " + screenKey + "\n\n" +
		"Below is the element table for this screen, one row per interactive element. " +
		"The eidx column is the element's index; use it exactly as printed.\n\n" +
		"Group the elements into named screen regions (navigation, toolbar, list, form, and similar). " +
		"For each region write a short factual note about its purpose, and list its elements " +
		"with their eidx, role, a human-readable label, and a short note when useful.\n\n" +
		"Answer with exactly this JSON shape:\n" +
		`{"sections":[{"name":"...","notes":"...","elements":[{"eidx":0,"role":"...","label":"...","notes":"..."}]}]}` + "\n" +
		"Use only eidx values present in the table. Every element belongs to at most one section.\n\n" +
		table
}

func correctionPrompt(unknown []int) string {
	values := make([]string, len(unknown))
	for i, eidx := range unknown {
		values[i] = strconv.Itoa(eidx)
	}
	return "These eidx values do not exist on this screen: " + strings.Join(values, ", ") + ". " +
		"Send the corrected JSON object again with the same shape, using only eidx values from the table, nothing else."
}

const visionSystemPrompt = "You review mobile application screenshots for automated exploratory testing. " +
	"You answer with a single JSON object and nothing else: no prose, no code fences."

func visionTaskPrompt(table string) string {
	return "The screenshot shows the screen described by the element table below. " +
		"For elements where the table's label is wrong or missing, supply a corrected label. " +
		"Add a short visual note per element when the screenshot reveals something the table cannot show " +
		"(an icon, a highlighted state, a badge).\n\n" +
		"Answer with exactly this JSON shape:\n" +
		`{"elements":[{"eidx":0,"label":"","notes":""}]}` + "\n" +
		"Use only eidx values from the table. Leave label empty when the table's label is already right. " +
		"Omit elements with nothing to add.\n\n" +
		table
}

// elementTable renders the flattened elements as compact markdown for
// model conversations.
func elementTable(elements []explore.FlatElement) string {
	var b strings.Builder
	b.WriteString("| eidx | role | label | bounds | clickable | enabled |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, element := range elements {
		bounds := "-"
		if parsed, ok := explore.ElementBounds(element.Node); ok {
			bounds = hierarchy.FormatBounds(parsed)
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s |\n",
			element.EIDX,
			tableCell(nodeRole(element.Node)),
			tableCell(nodeLabel(element.Node)),
			bounds,
			flagCell(element.Node.Clickable),
			flagCell(element.Node.Enabled),
		)
	}
	return b.String()
}

func tableCell(value string) string {
	value = strings.ReplaceAll(value, "|", `\|`)
	value = strings.ReplaceAll(value, "\n", " ")
	if value == "" {
		return "-"
	}
	return value
}

func flagCell(flag *bool) string {
	switch {
	case flag == nil:
		return "-"
	case *flag:
		return "yes"
	default:
		return "no"
	}
}

func nodeRole(node device.TreeNode) string {
	if role := node.Attributes["class"]; role != "" {
		return role
	}
	return node.Attributes["type"]
}

func nodeLabel(node device.TreeNode) string {
	for _, key := range []string{"text", "label", "name"} {
		if value := strings.TrimSpace(node.Attributes[key]); value != "" {
			return value
		}
	}
	return ""
}

func nodeID(node device.TreeNode) string {
	for _, key := range []string{"resource-id", "id"} {
		if value := strings.TrimSpace(node.Attributes[key]); value != "" {
			return value
		}
	}
	return ""
}

// elementLocators assigns locators deterministically, best first: a stable
// identifier, then visible text when it is unique among the flattened
// elements, then the tree path; a center-point locator is appended when
// the element has bounds.
func elementLocators(element explore.FlatElement, labelCounts map[string]int) []explore.Locator {
	locators := []explore.Locator{}
	switch {
	case nodeID(element.Node) != "":
		locators = append(locators, explore.Locator{Kind: explore.LocatorID, Value: nodeID(element.Node)})
	case nodeLabel(element.Node) != "" && labelCounts[nodeLabel(element.Node)] == 1:
		locators = append(locators, explore.Locator{Kind: explore.LocatorText, Value: nodeLabel(element.Node)})
	default:
		locators = append(locators, explore.Locator{Kind: explore.LocatorPath, Value: element.Path})
	}
	if bounds, ok := explore.ElementBounds(element.Node); ok {
		center := hierarchy.Center(bounds)
		locators = append(locators, explore.Locator{
			Kind:  explore.LocatorPoint,
			Value: formatPoint(center),
		})
	}
	return locators
}

func formatPoint(point device.Point) string {
	return strconv.FormatFloat(point.X, 'f', -1, 64) + "," +
		strconv.FormatFloat(point.Y, 'f', -1, 64)
}

// renderMarkdown renders the researched map for downstream agent
// conversations: sections with notes and one line per element carrying
// its best locator.
func renderMarkdown(screenKey string, sections []explore.Section) string {
	var b strings.Builder
	b.WriteString("# Screen map: " + screenKey + "\n")
	for _, section := range sections {
		b.WriteString("\n## " + section.Name + "\n")
		if section.Notes != "" {
			b.WriteString(section.Notes + "\n")
		}
		if section.Trigger != "" {
			b.WriteString("Revealed by: " + section.Trigger + "\n")
		}
		for _, element := range section.Elements {
			fmt.Fprintf(&b, "- [%d] %s %q", element.EIDX, element.Role, element.Label)
			if len(element.Locators) > 0 {
				best := element.Locators[0]
				fmt.Fprintf(&b, " — %s=%s", best.Kind, best.Value)
			}
			if element.Notes != "" {
				b.WriteString(" (" + element.Notes + ")")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
