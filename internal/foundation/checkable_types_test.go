package foundation_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"
)

var checkableTypeNumbers = regexp.MustCompile(`\b(\d+)\b`)

// readTypeList pulls the element-type numbers out of one declaration line.
func readTypeList(t *testing.T, relative, declaration string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(declaration) + `.*$`).Find(body)
	if line == nil {
		t.Fatalf("%s no longer declares %q; this guard reads that line", relative, declaration)
	}
	numbers := []string{}
	for _, match := range checkableTypeNumbers.FindAllStringSubmatch(string(line), -1) {
		numbers = append(numbers, match[1])
	}
	sort.Strings(numbers)
	return numbers
}

// The two packages that decide what an on/off control is must name the same
// XCUIElementTypes. internal/ios decides which elements get a `checked` value
// read from their accessibility VALUE at all; internal/explore decides which
// rows carry the `on`/`off` mark that value produces. Drop a type from the
// first and the second marks every one of them `off`, which is a false fact
// about the screen; add one to the second alone and the mark never appears.
// Neither side can see the other, so the agreement is pinned here.
func TestCheckableElementTypesAgreeAcrossPackages(t *testing.T) {
	t.Parallel()

	driver := readTypeList(t, "internal/ios/driver.go", "var checkableTypes = map[int]bool{")
	explore := readTypeList(t, "internal/explore/flatten.go", "var iosCheckableTypes = map[string]bool{")
	if !reflect.DeepEqual(driver, explore) {
		t.Errorf("internal/ios checkableTypes %v, internal/explore iosCheckableTypes %v", driver, explore)
	}
	if len(driver) == 0 {
		t.Error("no element types read; the guard is reading the wrong line")
	}
}
