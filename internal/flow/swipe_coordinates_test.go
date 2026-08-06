package flow

import "testing"

// specs/01-core-engine.md:28 defines swipe as direction, absolute coordinates,
// relative percentage coordinates, or element plus direction. start and end
// select relative parsing when either contains %, otherwise absolute text is
// deferred to runtime. Both endpoints must use the same variant.
//
//	contains % -> relative, parsed at syntax time:
//	    "50%,50%"    accept      "150%,50%"   refuse (>100)
//	    "50%,100"    accept      "50%,101"    refuse (>100)
//	    "100,50%"    accept      "50%,-1"     refuse (<0)
//	                             "50.5%,50%"  refuse (not an integer)
//	                             "${x}%,50%"  refuse (not an integer)
//
//	no % -> absolute, deferred to runtime:
//	    "100,200"    accept      "abc,20"     accept
//	    "-10,20"     accept      "${x},${y}"  accept
//	    " 10 , 20 "  accept      "100000,1"   accept
//
//	the endpoints must use the same variant:
//	    "100,200" + "300,400"    accept
//	    "50%,50%" + "90%,90%"    accept
//	    "100,200" + "90%,90%"    refuse
//
// Selector point is independent scalar text and does not use this grammar.

func swipeParses(t *testing.T, start, end string) error {
	t.Helper()
	yaml := "appId: com.example.app\n---\n- swipe:\n    start: \"" + start + "\"\n    end: \"" + end + "\"\n"
	_, err := ParseBytes("/swipe.yaml", []byte(yaml))
	return err
}

func TestSwipeRelativeCoordinatesAreStrictlyBounded(t *testing.T) {
	t.Parallel()

	for _, start := range []string{"150%,50%", "50%,101", "50%,-1", "50.5%,50%", "${x}%,50%"} {
		if err := swipeParses(t, start, "90%,90%"); err == nil {
			t.Fatalf("swipe accepted invalid relative start %q", start)
		}
	}
	for _, start := range []string{"50%,50%", "50%,100", "100,50%", "0%,0%", "100%,100%"} {
		if err := swipeParses(t, start, "90%,90%"); err != nil {
			t.Fatalf("swipe rejected valid relative start %q: %v", start, err)
		}
	}
}

// Absolute coordinate text is resolved at runtime, including interpolation.
func TestSwipeAbsoluteCoordinatesAreDeferred(t *testing.T) {
	t.Parallel()

	for _, start := range []string{"100,200", "-10,20", "abc,20", "${x},${y}", " 10 , 20 ", "100000,200000"} {
		if err := swipeParses(t, start, "30,40"); err != nil {
			t.Fatalf("swipe rejected absolute start %q: %v", start, err)
		}
	}
}

func TestSwipeEndpointsMustBeTheSameVariant(t *testing.T) {
	t.Parallel()

	for _, pair := range [][2]string{
		{"100,200", "90%,90%"},
		{"50%,50%", "100,200"},
	} {
		if err := swipeParses(t, pair[0], pair[1]); err == nil {
			t.Fatalf("swipe accepted mixed variants %q -> %q", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{
		{"100,200", "300,400"},
		{"50%,50%", "90%,90%"},
		{"100,50%", "90%,90%"},
	} {
		if err := swipeParses(t, pair[0], pair[1]); err != nil {
			t.Fatalf("swipe refused the matched pair %q -> %q: %v", pair[0], pair[1], err)
		}
	}
}

// Selector point remains independent scalar text.
func TestSelectorPointIsNotTheSwipeGrammar(t *testing.T) {
	t.Parallel()

	for _, point := range []string{"150%,50%", "50.5%,50%", "${x},${y}", "100,200"} {
		yaml := "appId: com.example.app\n---\n- tapOn:\n    point: \"" + point + "\"\n"
		if _, err := ParseBytes("/point.yaml", []byte(yaml)); err != nil {
			t.Fatalf("selector point rejected %q: %v", point, err)
		}
	}
}
