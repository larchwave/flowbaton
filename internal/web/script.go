package web

// contentDescriptionScript returns the page-side hierarchy walker.
//
// Spec 02-device-drivers.md §4 defines it: an injected script exposing
// `window.flowbaton.getContentDescription()` that walks the DOM, skips the tags
// that carry no on-screen text of their own, gives each <option> a synthetic
// node in a +100000 offset space (options are not laid out, so they have no
// real rectangle to match against), takes text from
// value|placeholder|ariaLabel|selectedOptions|text nodes, measures with
// getBoundingClientRect including iframe offsets, and emits Android-style
// "[l,t][r,b]" bounds so every platform parses one format.
//
// It is returned as one immediately-invoked expression because CDP's
// Runtime.evaluate takes an expression and returns its value: a statement-only
// body would evaluate to undefined and every settle would see an empty page.
// It is also idempotent — re-injection replaces the namespace rather than
// appending to it, since the driver re-evaluates on every hierarchy read.
func contentDescriptionScript() string {
	return scriptPrologue + hierarchyEpilogue
}

// querySelectorScript resolves a CSS expression to the same node shape the
// hierarchy walker emits, so a query result and a hierarchy node describe an
// element identically.
//
// selectorJSON must already be a JSON string literal: the expression comes from
// a flow, and interpolating it raw would let a selector close the quote and run
// as code in the page.
func querySelectorScript(selectorJSON string) string {
	return scriptPrologue + `
  var matches = [];
  try { matches = Array.prototype.slice.call(document.querySelectorAll(` + selectorJSON + `)); }
  catch (e) { throw new Error('invalid css selector: ' + e.message); }
  var described = [];
  for (var i = 0; i < matches.length; i++) { described.push(describe(matches[i], 0, 0)); }
  return JSON.stringify(described);
})()`
}

// scriptPrologue is everything both scripts share: the DOM walker and the node
// describer. Each script appends its own epilogue, so the hierarchy and a css
// query describe an element by exactly the same rules.
const scriptPrologue = `(function () {
  var SKIPPED = {
    'noscript': true, 'script': true, 'br': true, 'img': true,
    'svg': true, 'g': true, 'path': true, 'style': true
  };
  // Spec §4: <option> elements are not laid out, so they get synthetic nodes
  // in an offset space no real element occupies.
  var OPTION_OFFSET = 100000;

  function bounds(left, top, right, bottom) {
    return '[' + Math.round(left) + ',' + Math.round(top) + ']' +
      '[' + Math.round(right) + ',' + Math.round(bottom) + ']';
  }

  function ownText(element) {
    var text = '';
    for (var i = 0; i < element.childNodes.length; i++) {
      var child = element.childNodes[i];
      if (child.nodeType === 3 && child.nodeValue) { text += child.nodeValue; }
    }
    return text.trim();
  }

  // Spec §4 precedence: value, then placeholder, then aria-label, then the
  // selected options of a <select>, then the element's own text nodes.
  function textOf(element) {
    var tag = element.tagName ? element.tagName.toLowerCase() : '';
    if (tag === 'select' && element.selectedOptions && element.selectedOptions.length) {
      var picked = [];
      for (var i = 0; i < element.selectedOptions.length; i++) {
        picked.push((element.selectedOptions[i].textContent || '').trim());
      }
      return picked.join(', ');
    }
    if (typeof element.value === 'string' && element.value !== '') { return element.value; }
    var placeholder = element.getAttribute && element.getAttribute('placeholder');
    if (placeholder) { return placeholder; }
    var aria = element.getAttribute && element.getAttribute('aria-label');
    if (aria) { return aria; }
    return ownText(element);
  }

  function hintOf(element) {
    var placeholder = element.getAttribute && element.getAttribute('placeholder');
    return placeholder || '';
  }

  // A stable path so a matched node can be pointed back at the DOM.
  function cssPathOf(element) {
    if (element.id) { return '#' + element.id; }
    var parts = [];
    var current = element;
    while (current && current.nodeType === 1 && parts.length < 12) {
      var tag = current.tagName.toLowerCase();
      if (current.id) { parts.unshift('#' + current.id); break; }
      var parent = current.parentNode;
      if (parent && parent.children && parent.children.length > 1) {
        var index = 1;
        for (var i = 0; i < parent.children.length; i++) {
          if (parent.children[i] === current) { break; }
          if (parent.children[i].tagName === current.tagName) { index++; }
        }
        tag += ':nth-of-type(' + index + ')';
      }
      parts.unshift(tag);
      current = parent;
    }
    return parts.join(' > ');
  }

  function put(attributes, key, value) {
    if (value !== undefined && value !== null && value !== '') { attributes[key] = String(value); }
  }

  function describe(element, offsetX, offsetY) {
    var rect = element.getBoundingClientRect();
    var attributes = {};
    var tag = element.tagName.toLowerCase();
    put(attributes, 'tagName', tag);
    put(attributes, 'bounds', bounds(
      rect.left + offsetX, rect.top + offsetY,
      rect.right + offsetX, rect.bottom + offsetY));
    put(attributes, 'css', cssPathOf(element));
    put(attributes, 'resource-id', element.id);
    put(attributes, 'text', textOf(element));
    put(attributes, 'hintText', hintOf(element));
    var aria = element.getAttribute && element.getAttribute('aria-label');
    put(attributes, 'accessibilityText', aria || element.title || '');
    put(attributes, 'name', element.getAttribute && element.getAttribute('name'));

    // Tri-state on purpose: only elements that actually carry the state emit it.
    if (typeof element.disabled === 'boolean') {
      attributes['enabled'] = element.disabled ? 'false' : 'true';
    }
    if (document.activeElement === element) { attributes['focused'] = 'true'; }
    else if (element.tabIndex >= 0 || typeof element.disabled === 'boolean') { attributes['focused'] = 'false'; }
    if (typeof element.checked === 'boolean' &&
        (element.type === 'checkbox' || element.type === 'radio')) {
      attributes['checked'] = element.checked ? 'true' : 'false';
    }
    if (typeof element.selected === 'boolean') {
      attributes['selected'] = element.selected ? 'true' : 'false';
    }
    return { attributes: attributes, children: [] };
  }

  function walk(element, offsetX, offsetY, depth) {
    if (!element || element.nodeType !== 1 || depth > 60) { return null; }
    var tag = element.tagName.toLowerCase();
    if (SKIPPED[tag]) { return null; }

    var node = describe(element, offsetX, offsetY);

    // An <iframe>'s children are measured in the frame's own viewport, so its
    // origin is added to everything inside it.
    if (tag === 'iframe') {
      var inner = null;
      try { inner = element.contentDocument; } catch (e) { inner = null; }
      if (inner && inner.body) {
        var frameRect = element.getBoundingClientRect();
        var child = walk(inner.body, offsetX + frameRect.left, offsetY + frameRect.top, depth + 1);
        if (child) { node.children.push(child); }
      }
      return node;
    }

    if (tag === 'select' && element.options) {
      for (var o = 0; o < element.options.length; o++) {
        var option = element.options[o];
        var synthetic = { attributes: {}, children: [] };
        put(synthetic.attributes, 'tagName', 'option');
        put(synthetic.attributes, 'text', (option.textContent || '').trim());
        put(synthetic.attributes, 'css', cssPathOf(element) + ' > option:nth-of-type(' + (o + 1) + ')');
        put(synthetic.attributes, 'resource-id', option.id);
        synthetic.attributes['selected'] = option.selected ? 'true' : 'false';
        synthetic.attributes['bounds'] = bounds(
          OPTION_OFFSET, OPTION_OFFSET + o * 20,
          OPTION_OFFSET + 200, OPTION_OFFSET + o * 20 + 20);
        node.children.push(synthetic);
      }
      return node;
    }

    for (var i = 0; i < element.children.length; i++) {
      var walked = walk(element.children[i], offsetX, offsetY, depth + 1);
      if (walked) { node.children.push(walked); }
    }
    return node;
  }

`

// hierarchyEpilogue exposes the spec-named entry point and returns the tree.
const hierarchyEpilogue = `
  window.flowbaton = window.flowbaton || {};
  window.flowbaton.getContentDescription = function () {
    var root = walk(document.body || document.documentElement, 0, 0, 0);
    return root || { attributes: { tagName: 'body', bounds: '[0,0][0,0]' }, children: [] };
  };
  return JSON.stringify(window.flowbaton.getContentDescription());
})()`
