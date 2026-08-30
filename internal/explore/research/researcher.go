package research

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/explore"
)

// Cache stores researched UI maps by screen key. Nil on the Researcher
// disables caching.
type Cache interface {
	Get(key string) (*explore.UIMap, bool)
	Put(key string, m *explore.UIMap)
}

// Researcher turns an observed screen into a validated UI map. One worker
// call proposes sections over the element table; unknown element indexes
// get exactly one correction round in the same conversation; locators are
// assigned deterministically from the flattened tree; an optional vision
// call adds per-element visual notes.
type Researcher struct {
	Models explore.ModelSet
	// Cache short-circuits research for screens already mapped.
	Cache Cache
	// Clock supplies map timestamps; nil means wall time.
	Clock func() time.Time
}

type sectionsReply struct {
	Sections []sectionReply `json:"sections"`
}

type sectionReply struct {
	Name     string         `json:"name"`
	Notes    string         `json:"notes"`
	Elements []elementReply `json:"elements"`
}

type elementReply struct {
	EIDX  int    `json:"eidx"`
	Role  string `json:"role"`
	Label string `json:"label"`
	Notes string `json:"notes"`
}

type visionReply struct {
	Elements []visionElement `json:"elements"`
}

type visionElement struct {
	EIDX  int    `json:"eidx"`
	Label string `json:"label"`
	Notes string `json:"notes"`
}

// Research maps one screen. The input state is never mutated.
func (r *Researcher) Research(ctx context.Context, state *explore.ScreenState) (*explore.UIMap, error) {
	if state == nil {
		return nil, errors.New("research: nil screen state")
	}
	if r.Models.Worker == nil {
		return nil, errors.New("research: no worker model configured")
	}
	key := state.Signature.Key()
	if r.Cache != nil {
		if cached, ok := r.Cache.Get(key); ok && cached != nil {
			return copyUIMap(cached), nil
		}
	}
	table := elementTable(state.Elements)
	reply, err := r.proposeSections(ctx, state, table)
	if err != nil {
		return nil, err
	}
	sections := buildSections(reply, state.Elements)
	if r.Models.Vision != nil && len(state.ScreenshotPNG) > 0 {
		if err := r.mergeVisualNotes(ctx, state, table, sections); err != nil {
			return nil, err
		}
	}
	uiMap := &explore.UIMap{
		Screen:    state.Signature,
		Sections:  sections,
		CreatedAt: r.now(),
	}
	uiMap.Markdown = renderMarkdown(key, sections)
	if r.Cache != nil {
		r.Cache.Put(key, copyUIMap(uiMap))
	}
	return uiMap, nil
}

// proposeSections runs the worker conversation: one proposal call, and one
// correction round in the same conversation when unknown element indexes
// appear. Entries still unknown after the correction are dropped.
func (r *Researcher) proposeSections(ctx context.Context, state *explore.ScreenState, table string) (sectionsReply, error) {
	known := knownIndexes(state.Elements)
	messages := []explore.Message{
		{Role: explore.RoleSystem, Text: workerSystemPrompt},
		{Role: explore.RoleUser, Text: workerTaskPrompt(state.Signature.Key(), table)},
	}
	reply := sectionsReply{}
	response, err := explore.ChatJSON(
		ctx, r.Models.Worker, explore.ChatRequest{Messages: messages}, &reply)
	if err != nil {
		return sectionsReply{}, fmt.Errorf("research: section proposal: %w", err)
	}
	unknown := unknownIndexes(reply, known)
	if len(unknown) == 0 {
		return reply, nil
	}
	messages = append(messages, response.Message, explore.Message{
		Role: explore.RoleUser,
		Text: correctionPrompt(unknown),
	})
	if _, err := explore.ChatJSON(
		ctx, r.Models.Worker, explore.ChatRequest{Messages: messages}, &reply); err != nil {
		return sectionsReply{}, fmt.Errorf("research: section correction: %w", err)
	}
	return dropUnknown(reply, known), nil
}

// askVision makes the vision call and decodes it. A vision model corrupts a
// reply now and then (a stray quote after a two-digit index, seen live), and
// the notes it carries are enrichment, not something worth losing the
// session over -- which is what explore.ChatJSON does for every model call.
func (r *Researcher) askVision(ctx context.Context, state *explore.ScreenState, table string) (visionReply, error) {
	reply := visionReply{}
	_, err := explore.ChatJSON(ctx, r.Models.Vision, explore.ChatRequest{Messages: []explore.Message{
		{Role: explore.RoleSystem, Text: visionSystemPrompt},
		{
			Role:     explore.RoleUser,
			Text:     visionTaskPrompt(table),
			ImagePNG: state.ScreenshotPNG,
		},
	}}, &reply)
	if err != nil {
		return visionReply{}, fmt.Errorf("research: visual pass: %w", err)
	}
	return reply, nil
}

// mergeVisualNotes runs one vision call over the screenshot and merges the
// per-element findings into the sections in place. Findings for unknown
// element indexes are dropped.
func (r *Researcher) mergeVisualNotes(ctx context.Context, state *explore.ScreenState, table string, sections []explore.Section) error {
	reply, err := r.askVision(ctx, state, table)
	if err != nil {
		return err
	}
	byIndex := map[int]visionElement{}
	for _, finding := range reply.Elements {
		byIndex[finding.EIDX] = finding
	}
	for si := range sections {
		for ei := range sections[si].Elements {
			element := &sections[si].Elements[ei]
			finding, ok := byIndex[element.EIDX]
			if !ok {
				continue
			}
			if strings.TrimSpace(finding.Label) != "" {
				element.Label = finding.Label
			}
			if strings.TrimSpace(finding.Notes) != "" {
				if element.Notes != "" {
					element.Notes += "; " + finding.Notes
				} else {
					element.Notes = finding.Notes
				}
			}
		}
	}
	return nil
}

func (r *Researcher) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now()
}

// buildSections resolves proposed rows against the flattened elements,
// assigning deterministic locators and falling back to tree facts for
// blank role or label fields.
func buildSections(reply sectionsReply, elements []explore.FlatElement) []explore.Section {
	byIndex := map[int]explore.FlatElement{}
	labelCounts := map[string]int{}
	for _, element := range elements {
		byIndex[element.EIDX] = element
		if label := nodeLabel(element.Node); label != "" {
			labelCounts[label]++
		}
	}
	sections := make([]explore.Section, 0, len(reply.Sections))
	for _, proposed := range reply.Sections {
		section := explore.Section{Name: proposed.Name, Notes: proposed.Notes}
		for _, row := range proposed.Elements {
			flat, ok := byIndex[row.EIDX]
			if !ok {
				continue
			}
			role := row.Role
			if role == "" {
				role = nodeRole(flat.Node)
			}
			label := row.Label
			if label == "" {
				label = nodeLabel(flat.Node)
			}
			section.Elements = append(section.Elements, explore.MappedElement{
				EIDX:     row.EIDX,
				Role:     role,
				Label:    label,
				Locators: elementLocators(flat, labelCounts),
				Notes:    row.Notes,
			})
		}
		sections = append(sections, section)
	}
	return sections
}

func knownIndexes(elements []explore.FlatElement) map[int]bool {
	known := make(map[int]bool, len(elements))
	for _, element := range elements {
		known[element.EIDX] = true
	}
	return known
}

func unknownIndexes(reply sectionsReply, known map[int]bool) []int {
	seen := map[int]bool{}
	unknown := []int{}
	for _, section := range reply.Sections {
		for _, row := range section.Elements {
			if !known[row.EIDX] && !seen[row.EIDX] {
				seen[row.EIDX] = true
				unknown = append(unknown, row.EIDX)
			}
		}
	}
	sort.Ints(unknown)
	return unknown
}

// dropUnknown returns a copy of the reply without rows whose element index
// is not on the screen.
func dropUnknown(reply sectionsReply, known map[int]bool) sectionsReply {
	out := sectionsReply{Sections: make([]sectionReply, 0, len(reply.Sections))}
	for _, section := range reply.Sections {
		kept := sectionReply{Name: section.Name, Notes: section.Notes}
		for _, row := range section.Elements {
			if known[row.EIDX] {
				kept.Elements = append(kept.Elements, row)
			}
		}
		out.Sections = append(out.Sections, kept)
	}
	return out
}

func copyUIMap(source *explore.UIMap) *explore.UIMap {
	out := &explore.UIMap{
		Screen:    source.Screen,
		CreatedAt: source.CreatedAt,
		Markdown:  source.Markdown,
	}
	out.Screen.Salient = append([]string(nil), source.Screen.Salient...)
	out.Sections = make([]explore.Section, len(source.Sections))
	for si, section := range source.Sections {
		copied := section
		copied.Elements = make([]explore.MappedElement, len(section.Elements))
		for ei, element := range section.Elements {
			copiedElement := element
			copiedElement.Locators = append([]explore.Locator(nil), element.Locators...)
			copied.Elements[ei] = copiedElement
		}
		out.Sections[si] = copied
	}
	return out
}
