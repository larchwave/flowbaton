package hierarchy

import "github.com/larchwave/flowbaton/internal/device"

// Element is a normalized hierarchy node with explicit geometry, ancestry, and
// stable preorder identity. Node.Children is cleared; Children is authoritative.
type Element struct {
	Node      device.TreeNode
	Bounds    device.Bounds
	HasBounds bool
	Parent    *Element
	Children  []*Element
	Order     int
	// OnScreen records what FilterVisible decided about this node, so a
	// later step can tell a match the user can see from an ancestor kept
	// only to carry a visible descendant. It stays false on an unfiltered
	// tree, where nothing has decided yet.
	OnScreen bool
}

// New converts a frozen TreeNode DTO into a normalized hierarchy.
func New(root device.TreeNode) (*Element, error) {
	order := 0
	return newElement(root, nil, &order)
}

func newElement(node device.TreeNode, parent *Element, order *int) (*Element, error) {
	element := &Element{
		Node:     cloneNodeWithoutChildren(node),
		Parent:   parent,
		Order:    *order,
		Children: make([]*Element, 0, len(node.Children)),
	}
	(*order)++
	if rawBounds, exists := node.Attributes["bounds"]; exists {
		bounds, err := ParseBounds(rawBounds)
		if err != nil {
			return nil, err
		}
		element.Bounds = bounds
		element.HasBounds = true
	}
	for _, childNode := range node.Children {
		child, err := newElement(childNode, element, order)
		if err != nil {
			return nil, err
		}
		element.Children = append(element.Children, child)
	}
	return element, nil
}

// Walk returns elements in stable preorder.
func Walk(root *Element) []*Element {
	if root == nil {
		return nil
	}
	result := make([]*Element, 0)
	stack := []*Element{root}
	for len(stack) > 0 {
		index := len(stack) - 1
		element := stack[index]
		stack = stack[:index]
		result = append(result, element)
		for childIndex := len(element.Children) - 1; childIndex >= 0; childIndex-- {
			stack = append(stack, element.Children[childIndex])
		}
	}
	return result
}

// FilterVisible returns a pruned copy. Nodes without bounds are retained, and
// an otherwise invisible node remains when at least one child remains visible.
func FilterVisible(root *Element, viewport device.Bounds) *Element {
	return FilterVisibleWithin(root, viewport, nil)
}

// FilterVisibleWithin is FilterVisible where some nodes clip what is drawn
// inside them: a descendant of a node clips accepts is measured against the
// viewport narrowed to that node's bounds. An iPhone-only app on an iPad draws
// in a window smaller than the screen, and a node below the window's edge is
// inside the screen and still not visible (issue #44). A nil clips is
// FilterVisible.
func FilterVisibleWithin(root *Element, viewport device.Bounds, clips func(device.TreeNode) bool) *Element {
	return filterVisible(root, nil, viewport, clips)
}

// ClippedViewport is the viewport an element is visible in: the screen
// narrowed by every ancestor clips accepts.
func ClippedViewport(element *Element, viewport device.Bounds, clips func(device.TreeNode) bool) device.Bounds {
	if clips == nil || element == nil {
		return viewport
	}
	for ancestor := element.Parent; ancestor != nil; ancestor = ancestor.Parent {
		viewport = clipTo(ancestor, viewport, clips)
	}
	return viewport
}

func clipTo(element *Element, viewport device.Bounds, clips func(device.TreeNode) bool) device.Bounds {
	if clips == nil || !element.HasBounds || Area(element.Bounds) == 0 || !clips(element.Node) {
		return viewport
	}
	return Intersection(viewport, element.Bounds)
}

func filterVisible(source, parent *Element, viewport device.Bounds, clips func(device.TreeNode) bool) *Element {
	if source == nil {
		return nil
	}
	clone := &Element{
		Node:      cloneNodeWithoutChildren(source.Node),
		Bounds:    source.Bounds,
		HasBounds: source.HasBounds,
		Parent:    parent,
		Children:  make([]*Element, 0, len(source.Children)),
		Order:     source.Order,
	}
	inner := clipTo(source, viewport, clips)
	for _, child := range source.Children {
		if retained := filterVisible(child, clone, inner, clips); retained != nil {
			clone.Children = append(clone.Children, retained)
		}
	}
	clone.OnScreen = !clone.HasBounds || IsVisible(clone.Bounds, viewport)
	if clone.OnScreen || len(clone.Children) > 0 {
		return clone
	}
	return nil
}

func cloneNodeWithoutChildren(node device.TreeNode) device.TreeNode {
	clone := node
	clone.Children = nil
	if node.Attributes != nil {
		clone.Attributes = make(map[string]string, len(node.Attributes))
		for key, value := range node.Attributes {
			clone.Attributes[key] = value
		}
	}
	return clone
}
