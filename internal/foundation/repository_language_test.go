package foundation_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type encodedFamily struct {
	value string
}

var blockedFamilies = []encodedFamily{
	{value: "6d61657374726f"},
	{value: "636c65616e726f6f6d"},
	{value: "636c65616e2d726f6f6d"},
	{value: "636c65616e20726f6f6d"},
	{value: "636c65616e5f726f6f6d"},
	{value: "757073747265616d"},
	{value: "7265666572656e6365"},
	{value: "6f7261636c65"},
	{value: "706172697479"},
	{value: "646966666572656e7469616c"},
	{value: "636f6d706172"},
	{value: "626173656c696e65"},
	{value: "626c61636b2d626f78"},
	{value: "626c61636b626f78"},
	{value: "626c61636b20626f78"},
	{value: "626c61636b5f626f78"},
	{value: "70726f76656e616e6365"},
	{value: "6d6967726174696f6e"},
	{value: "6c6567616379"},
	{value: "686973746f726963616c"},
	{value: "666f726d65726c79"},
}

var blockedNarratives = []encodedFamily{
	{value: "666f756e64206279"},
	{value: "73616d6520666c6f77206f6e20626f746820636c6973"},
	{value: "73616d6520776f726b7370616365206f6e20626f746820636c6973"},
	{value: "70726f626564206f6e"},
	{value: "7573656420746f"},
	{value: "66697273742076657273696f6e"},
	{value: "6c6976652072756e"},
	{value: "7265616c2072756e"},
	{value: "6265686176696f722066696e64696e67"},
	{value: "636f6d7061746962696c6974792072756c65"},
	{value: "766572696669656420616761696e7374"},
	{value: "6d65617375726564206174"},
	{value: "70726f76656e206f6e"},
	{value: "6f62736572766564206f6e20617069"},
	{value: "756e74696c206e6f77"},
	{value: "6e6f206c6f6e676572206d65616e73"},
	{value: "776520776572652061736b6564"},
	{value: "65766964656e63652066696c65"},
	{value: "6369207265636970652075736564"},
	{value: "736f7572636573206469736167726565"},
	{value: "626f6f7465642073696d756c61746f722073686f776564"},
	{value: "6669727374206c697665"},
	{value: "6c6976652063726f73732d6c616e6775616765"},
	{value: "6f6c642073696e676c652d72656164"},
	{value: "74686520636c6f6e6520737065616b73"},
	{value: "636f6e6e65637465642070726f6f66"},
	{value: "74686973206761702077617320666f756e64"},
	{value: "75736564206265666f72652074686973"},
	{value: "686f7374206e6f7720616c736f206c6f6f6b7320666f72"},
	{value: "617320697420616c7761797320646964"},
	{value: "6f6e6c792077617920746f20676574206120646576746f6f6c7320656e64706f696e74206f7574206f66207468697320656d756c61746f72"},
	{value: "72656c65617365206368726f6d65207075626c6973686573206e6f"},
	{value: "636f6e74726f6c2074686174206f6e6c79206c6f6f6b656420666f72"},
	{value: "686164207468652073616d6520627567"},
	{value: "7061746368696e67206f6e6520776f756c64206c65617665"},
	{value: "6265666f7265207468652073776966742072756e6e657220657869737473"},
	{value: "72756e6e657220697473656c6620646f6573206e6f7420657869737420796574"},
	{value: "7768656e207468652073776966742073696465206c616e6473"},
	{value: "66697865642074617267657420726174686572207468616e2061206d6f76696e67206f6e65"},
	{value: "7374696c6c2066616c6c73206261636b"},
	{value: "636f756c64206e6f7420616e73776572206f6e2065697468657220706c6174666f726d"},
	{value: "77726f6e67207175657374696f6e2068657265"},
	{value: "6f776e65722d617070726f766564"},
	{value: "7261697365642066726f6d"},
	{value: "757365642062792067303031"},
	{value: "617574686f7273206e6f2062726f7773657220666c616720746f646179"},
	{value: "6120706f7274206973207265717569726564206e6f77"},
	{value: "73686f727420666c6f7720656e6465642077697468"},
	{value: "636174616c6f67206e6f206c6f6e67657220646566657273"},
}

type legalBlock struct {
	Path        string
	Start       int
	End         int
	StartAnchor []byte
	EndAnchor   []byte
	BlockSum    [sha256.Size]byte
	FileSum     [sha256.Size]byte
	Rationale   string
}

var legalBlocks = []legalBlock{}

type policyFinding struct {
	Surface string
	Subject string
	Offset  int
	Family  string
}

func (f policyFinding) String() string {
	return fmt.Sprintf("%s %q byte %d family %s", f.Surface, f.Subject, f.Offset, f.Family)
}

type blockRange struct {
	start int
	end   int
}

func TestRepositoryLanguageTree(t *testing.T) {
	findings, err := scanIndex(repoRoot(t), legalBlocks)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("tracked tree violates repository language policy:\n%s", formatFindings(findings))
	}
}

func TestRepositoryLanguageHistory(t *testing.T) {
	if os.Getenv("FLOWBATON_LANGUAGE_HISTORY") != "1" {
		t.Skip("set FLOWBATON_LANGUAGE_HISTORY=1 in a clean-root checkout")
	}
	findings, err := scanPublishedHistory(repoRoot(t), legalBlocks)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("published history violates repository language policy:\n%s", formatFindings(findings))
	}
}

func TestLanguagePolicySourceUsesEncodedFamilies(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "foundation", "repository_language_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if findings := scanBytes("content", "internal/foundation/repository_language_test.go", data, nil); len(findings) != 0 {
		t.Fatalf("policy source contains decoded family data:\n%s", formatFindings(findings))
	}
}

func TestLanguagePolicyRejectsEveryFamilyInIndexedData(t *testing.T) {
	for index, family := range decodedFamilies(t) {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			repo := initTempRepository(t)
			writeAndAdd(t, repo, "safe.txt", append([]byte("prefix "), family...))
			findings, err := scanIndex(repo, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertHasSurface(t, findings, "content")
		})
	}
}

func TestLanguagePolicyRejectsEveryBlockedNarrativeInIndexedData(t *testing.T) {
	for index, narrative := range decodedNarratives(t) {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			repo := initTempRepository(t)
			writeAndAdd(t, repo, "safe.txt", append([]byte("prefix "), narrative...))
			findings, err := scanIndex(repo, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertHasSurface(t, findings, "content")
		})
	}
}

func TestLanguagePolicyRejectsBlockedNarrativesAcrossASCIIWhitespace(t *testing.T) {
	separators := [][]byte{
		[]byte("\n"),
		[]byte("\r\n"),
		[]byte("\t"),
		[]byte(" \n\t "),
	}
	for narrativeIndex, narrative := range decodedNarratives(t) {
		space := bytes.IndexByte(narrative, ' ')
		if space < 0 {
			continue
		}
		for separatorIndex, separator := range separators {
			t.Run(strconv.Itoa(narrativeIndex)+"-"+strconv.Itoa(separatorIndex), func(t *testing.T) {
				payload := append([]byte(nil), narrative[:space]...)
				payload = append(payload, separator...)
				payload = append(payload, narrative[space+1:]...)
				findings := scanBytes("content", "safe.txt", payload, nil)
				assertHasSurface(t, findings, "content")
			})
		}
	}
}

func TestLanguagePolicyRejectsReviewerLineBreakCase(t *testing.T) {
	narrative, err := hex.DecodeString("70726f76656e206f6e")
	if err != nil {
		t.Fatal(err)
	}
	repo := initTempRepository(t)
	writeAndAdd(t, repo, "safe.txt", bytes.Replace(narrative, []byte(" "), []byte("\n"), 1))
	findings, err := scanIndex(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertHasSurface(t, findings, "content")
}

func TestLanguagePolicyAcceptsCurrentRuntimeVocabulary(t *testing.T) {
	repo := initTempRepository(t)
	writeAndAdd(t, repo, "safe.txt", []byte("previous snapshot probe source fixture audit evidence; deep-copy clone; sequencing; before action; after action; current status; still running; command refused to start"))
	findings, err := scanIndex(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("current runtime vocabulary produced findings:\n%s", formatFindings(findings))
	}
}

func TestLanguagePolicyAcceptsRuntimeRequirementWording(t *testing.T) {
	sentence := strings.Join([]string{"authorization is required", "now"}, " ")
	if findings := scanBytes("content", "safe.txt", []byte(sentence), nil); len(findings) != 0 {
		t.Fatalf("runtime requirement wording produced findings:\n%s", formatFindings(findings))
	}
}

func TestLanguagePolicyLimitsBlockedNarrativesToProseSurfaces(t *testing.T) {
	repo := initTempRepository(t)
	narrative := decodedNarratives(t)[0]
	writeAndAdd(t, repo, "item-"+string(narrative)+".txt", []byte("safe"))
	findings, err := scanIndex(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("blocked narrative in a path produced findings:\n%s", formatFindings(findings))
	}

	lineBreakPath := "item-" + string(bytes.Replace(narrative, []byte(" "), []byte("\n"), 1)) + ".txt"
	if findings := scanBytes("path", lineBreakPath, []byte(lineBreakPath), nil); len(findings) != 0 {
		t.Fatalf("line-break path produced findings:\n%s", formatFindings(findings))
	}
}

func TestLanguagePolicyFoldsASCIIAndReadsNULBytes(t *testing.T) {
	repo := initTempRepository(t)
	family := bytes.ToUpper(decodedFamilies(t)[0])
	payload := append([]byte{0, 1, 2}, family...)
	payload = append(payload, 0, 3, 4)
	writeAndAdd(t, repo, "payload.dat", payload)
	findings, err := scanIndex(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertHasSurface(t, findings, "content")
}

func TestLanguagePolicyIgnoresHostileAmbientGitState(t *testing.T) {
	target := initTempRepository(t)
	family := decodedFamilies(t)[0]
	writeAndAdd(t, target, "payload.txt", family)

	decoy := initTempRepository(t)
	writeAndAdd(t, decoy, "safe.txt", []byte("safe"))
	for key, value := range map[string]string{
		"GIT_DIR":                          filepath.Join(decoy, ".git"),
		"GIT_WORK_TREE":                    decoy,
		"GIT_INDEX_FILE":                   filepath.Join(decoy, ".git", "index"),
		"GIT_COMMON_DIR":                   filepath.Join(decoy, ".git"),
		"GIT_OBJECT_DIRECTORY":             filepath.Join(decoy, ".git", "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": filepath.Join(decoy, ".git", "objects"),
		"GIT_REPLACE_REF_BASE":             "refs/replace-hostile/",
		"GIT_CONFIG_COUNT":                 "1",
		"GIT_CONFIG_KEY_0":                 "core.bare",
		"GIT_CONFIG_VALUE_0":               "true",
	} {
		t.Setenv(key, value)
	}

	findings, err := scanIndex(target, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertHasSurface(t, findings, "content")
}

func TestLanguagePolicyReadsOriginalObjectsDespiteReplacementRefs(t *testing.T) {
	repo := initTempRepository(t)
	family := decodedFamilies(t)[0]
	writeAndAdd(t, repo, "payload.txt", family)
	blockedID := strings.TrimSpace(string(mustGitOutput(t, repo, nil, "hash-object", "payload.txt")))

	safePath := filepath.Join(repo, "safe-source.txt")
	if err := os.WriteFile(safePath, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	safeID := strings.TrimSpace(string(mustGitOutput(t, repo, nil, "hash-object", "-w", "safe-source.txt")))
	runGit(t, repo, nil, "replace", blockedID, safeID)
	effectiveCommand := exec.Command("git", "-C", repo, "cat-file", "blob", blockedID)
	effectiveCommand.Env = gitEnvironmentWithoutAmbientState()
	effective, effectiveErr := effectiveCommand.CombinedOutput()
	if effectiveErr != nil {
		t.Fatalf("read effective replacement object: %v: %s", effectiveErr, effective)
	}
	if !bytes.Equal(effective, []byte("safe")) {
		t.Fatalf("replacement control is not active: got %q", effective)
	}

	findings, err := scanIndex(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertHasSurface(t, findings, "content")
}

func TestLanguagePolicyRejectsEveryFamilyInIndexedPaths(t *testing.T) {
	for index, family := range decodedFamilies(t) {
		t.Run("file-"+strconv.Itoa(index), func(t *testing.T) {
			repo := initTempRepository(t)
			writeAndAdd(t, repo, "item-"+string(family)+".txt", []byte("safe"))
			findings, err := scanIndex(repo, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertHasSurface(t, findings, "path")
		})
		t.Run("dir-"+strconv.Itoa(index), func(t *testing.T) {
			repo := initTempRepository(t)
			writeAndAdd(t, repo, filepath.Join("item-"+string(family), "safe.txt"), []byte("safe"))
			findings, err := scanIndex(repo, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertHasSurface(t, findings, "path")
		})
	}
}

func TestLanguagePolicyRejectsHistoryMetadata(t *testing.T) {
	family := decodedFamilies(t)[0]
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
		surface string
	}{
		{
			name: "commit-message",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), string(family), nil)
			},
			surface: "commit-message",
		},
		{
			name: "author-name",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), "safe", map[string]string{
					"GIT_AUTHOR_NAME": string(family),
				})
			},
			surface: "author",
		},
		{
			name: "author-email",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), "safe", map[string]string{
					"GIT_AUTHOR_EMAIL": "build-" + string(family) + "@example.invalid",
				})
			},
			surface: "author",
		},
		{
			name: "committer-name",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), "safe", map[string]string{
					"GIT_COMMITTER_NAME": string(family),
				})
			},
			surface: "committer",
		},
		{
			name: "committer-email",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), "safe", map[string]string{
					"GIT_COMMITTER_EMAIL": "build-" + string(family) + "@example.invalid",
				})
			},
			surface: "committer",
		},
		{
			name: "branch-name",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), "safe", nil)
				runGit(t, repo, nil, "branch", "topic-"+string(family))
			},
			surface: "ref-name",
		},
		{
			name: "tag-name",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), "safe", nil)
				runGit(t, repo, nil, "tag", "-a", "release-"+string(family), "-m", "safe")
			},
			surface: "ref-name",
		},
		{
			name: "tagger",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), "safe", nil)
				runGit(t, repo, map[string]string{"GIT_COMMITTER_NAME": string(family)}, "tag", "-a", "release-safe", "-m", "safe")
			},
			surface: "tagger",
		},
		{
			name: "tag-message",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), "safe", nil)
				runGit(t, repo, nil, "tag", "-a", "release-safe", "-m", string(family))
			},
			surface: "tag-message",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := initTempRepository(t)
			test.prepare(t, repo)
			findings, err := scanPublishedHistory(repo, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertHasSurface(t, findings, test.surface)
		})
	}
}

func TestLanguagePolicyRejectsBlockedNarrativesInHistoryProse(t *testing.T) {
	narrative := decodedNarratives(t)[0]
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
		surface string
	}{
		{
			name: "tracked-content",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", narrative, "safe", nil)
			},
			surface: "history-content",
		},
		{
			name: "commit-message",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), string(narrative), nil)
			},
			surface: "commit-message",
		},
		{
			name: "tag-message",
			prepare: func(t *testing.T, repo string) {
				commitFile(t, repo, "safe.txt", []byte("safe"), "safe", nil)
				runGit(t, repo, nil, "tag", "-a", "release-safe", "-m", string(narrative))
			},
			surface: "tag-message",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := initTempRepository(t)
			test.prepare(t, repo)
			findings, err := scanPublishedHistory(repo, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertHasSurface(t, findings, test.surface)
		})
	}
}

func TestLanguagePolicyAcceptsSafeHistory(t *testing.T) {
	repo := initTempRepository(t)
	commitFile(t, repo, "safe.txt", []byte("safe"), "safe", nil)
	runGit(t, repo, nil, "tag", "-a", "release-safe", "-m", "safe")
	findings, err := scanPublishedHistory(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("safe history produced findings:\n%s", formatFindings(findings))
	}
}

func TestLanguagePolicyRejectsTagOnlyTreePath(t *testing.T) {
	repo := initTempRepository(t)
	family := decodedFamilies(t)[4]
	writeAndAdd(t, repo, filepath.Join("item-"+string(family), "file-"+string(family)+".txt"), []byte("safe"))
	treeID := strings.TrimSpace(string(mustGitOutput(t, repo, nil, "write-tree")))
	runGit(t, repo, nil, "tag", "-a", "release-safe", "-m", "safe", treeID)
	findings, err := scanPublishedHistory(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertHasSurface(t, findings, "history-path")
}

func TestLanguagePolicyLegalBlockIsExact(t *testing.T) {
	family := decodedFamilies(t)[0]
	prefix := []byte("start-anchor\n")
	suffix := []byte("\nend-anchor\n")
	data := append(append(append([]byte(nil), prefix...), family...), suffix...)
	entry := legalBlock{
		Path:        "legal.txt",
		Start:       len(prefix),
		End:         len(prefix) + len(family),
		StartAnchor: []byte("anchor\n"),
		EndAnchor:   []byte("\nend"),
		BlockSum:    sha256.Sum256(family),
		FileSum:     sha256.Sum256(data),
		Rationale:   "mandatory external wording",
	}

	t.Run("exact", func(t *testing.T) {
		repo := initTempRepository(t)
		writeAndAdd(t, repo, entry.Path, data)
		findings, err := scanIndex(repo, []legalBlock{entry})
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 0 {
			t.Fatalf("exact legal block produced findings:\n%s", formatFindings(findings))
		}
	})

	tests := []struct {
		name   string
		data   []byte
		mutate func(*legalBlock)
	}{
		{name: "altered-block", data: bytes.Replace(data, family, []byte("changed"), 1)},
		{name: "moved-block", data: append([]byte("x"), data...)},
		{name: "whole-file-change", data: append(append([]byte(nil), data...), 'x')},
		{name: "changed-start-anchor", data: data, mutate: func(item *legalBlock) { item.StartAnchor = []byte("wrong") }},
		{name: "changed-end-anchor", data: data, mutate: func(item *legalBlock) { item.EndAnchor = []byte("wrong") }},
		{name: "changed-range", data: data, mutate: func(item *legalBlock) { item.End-- }},
		{name: "stale", data: bytes.Replace(data, family, []byte("allowed"), 1), mutate: func(item *legalBlock) {
			item.BlockSum = sha256.Sum256([]byte("allowed"))
			item.FileSum = sha256.Sum256(bytes.Replace(data, family, []byte("allowed"), 1))
			item.End = item.Start + len("allowed")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := initTempRepository(t)
			writeAndAdd(t, repo, entry.Path, test.data)
			item := entry
			if test.mutate != nil {
				test.mutate(&item)
			}
			if _, err := scanIndex(repo, []legalBlock{item}); err == nil {
				t.Fatal("invalid legal block was accepted")
			}
		})
	}
}

func scanIndex(repo string, exceptions []legalBlock) ([]policyFinding, error) {
	output, err := gitOutput(repo, nil, "ls-files", "-s", "-z")
	if err != nil {
		return nil, err
	}
	used := make([]bool, len(exceptions))
	var findings []policyFinding
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		tab := bytes.IndexByte(record, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("malformed index record %q", record)
		}
		fields := strings.Fields(string(record[:tab]))
		if len(fields) != 3 || fields[2] != "0" {
			return nil, fmt.Errorf("unsupported index record %q", record)
		}
		path := string(record[tab+1:])
		data, readErr := gitOutput(repo, nil, "cat-file", "blob", fields[1])
		if readErr != nil {
			return nil, readErr
		}
		ranges, rangeErr := verifiedRanges(path, data, exceptions, used)
		if rangeErr != nil {
			return nil, rangeErr
		}
		findings = append(findings, scanBytes("path", path, []byte(path), nil)...)
		findings = append(findings, scanBytes("content", path, data, ranges)...)
	}
	if err := rejectUnusedBlocks(exceptions, used); err != nil {
		return nil, err
	}
	return sortedFindings(findings), nil
}

func scanPublishedHistory(repo string, exceptions []legalBlock) ([]policyFinding, error) {
	refOutput, err := gitOutput(repo, nil, "for-each-ref", "--format=%(refname)%09%(objectname)%09%(objecttype)", "refs/heads", "refs/tags", "refs/remotes")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(refOutput)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, fmt.Errorf("repository has no normal refs")
	}
	var findings []policyFinding
	var revArgs []string
	var commitRevArgs []string
	directTrees := map[string]string{}
	seenTags := map[string]bool{}
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("malformed ref record %q", line)
		}
		refName, objectID, objectType := fields[0], fields[1], fields[2]
		findings = append(findings, scanBytes("ref-name", refName, []byte(refName), nil)...)
		revArgs = append(revArgs, refName)
		peeledOutput, peelErr := gitOutput(repo, nil, "rev-parse", refName+"^{}")
		if peelErr != nil {
			return nil, peelErr
		}
		peeledID := strings.TrimSpace(string(peeledOutput))
		peeledTypeOutput, typeErr := gitOutput(repo, nil, "cat-file", "-t", peeledID)
		if typeErr != nil {
			return nil, typeErr
		}
		peeledType := strings.TrimSpace(string(peeledTypeOutput))
		switch peeledType {
		case "commit":
			commitRevArgs = append(commitRevArgs, refName)
		case "tree":
			directTrees[peeledID] = refName
		case "blob":
		default:
			return nil, fmt.Errorf("unsupported ref target type %q for %s", peeledType, refName)
		}
		if objectType == "tag" {
			seenTags[objectID] = true
			raw, readErr := gitOutput(repo, nil, "cat-file", "tag", objectID)
			if readErr != nil {
				return nil, readErr
			}
			tagger, message := tagMetadata(raw)
			findings = append(findings, scanBytes("tagger", refName, tagger, nil)...)
			findings = append(findings, scanBytes("tag-message", refName, message, nil)...)
		}
	}

	closureArgs := append([]string{"rev-list", "--objects", "--no-object-names"}, revArgs...)
	closureOutput, err := gitOutput(repo, nil, closureArgs...)
	if err != nil {
		return nil, err
	}
	closureIDs := strings.Fields(string(closureOutput))

	var commitIDs []string
	if len(commitRevArgs) != 0 {
		args := append([]string{"rev-list", "--topo-order", "--reverse"}, commitRevArgs...)
		commitOutput, commitErr := gitOutput(repo, nil, args...)
		if commitErr != nil {
			return nil, commitErr
		}
		commitIDs = strings.Fields(string(commitOutput))
	}
	seenBlobs := map[string]bool{}
	usedBlocks := make([]bool, len(exceptions))
	for _, commitID := range commitIDs {
		raw, readErr := gitOutput(repo, nil, "cat-file", "commit", commitID)
		if readErr != nil {
			return nil, readErr
		}
		author, committer, message := commitMetadata(raw)
		findings = append(findings, scanBytes("author", commitID, author, nil)...)
		findings = append(findings, scanBytes("committer", commitID, committer, nil)...)
		findings = append(findings, scanBytes("commit-message", commitID, message, nil)...)

		treeFindings, treeErr := scanHistoryTree(repo, commitID, commitID, exceptions, usedBlocks, seenBlobs)
		if treeErr != nil {
			return nil, treeErr
		}
		findings = append(findings, treeFindings...)
	}
	directTreeIDs := make([]string, 0, len(directTrees))
	for treeID := range directTrees {
		directTreeIDs = append(directTreeIDs, treeID)
	}
	sort.Strings(directTreeIDs)
	for _, treeID := range directTreeIDs {
		treeFindings, treeErr := scanHistoryTree(repo, treeID, directTrees[treeID], exceptions, usedBlocks, seenBlobs)
		if treeErr != nil {
			return nil, treeErr
		}
		findings = append(findings, treeFindings...)
	}
	if err := rejectUnusedBlocks(exceptions, usedBlocks); err != nil {
		return nil, err
	}
	for _, objectID := range closureIDs {
		objectType, typeErr := gitOutput(repo, nil, "cat-file", "-t", objectID)
		if typeErr != nil {
			return nil, typeErr
		}
		switch strings.TrimSpace(string(objectType)) {
		case "blob":
			if seenBlobs[objectID] {
				continue
			}
			data, blobErr := gitOutput(repo, nil, "cat-file", "blob", objectID)
			if blobErr != nil {
				return nil, blobErr
			}
			findings = append(findings, scanBytes("history-content", objectID, data, nil)...)
		case "tag":
			if seenTags[objectID] {
				continue
			}
			raw, readErr := gitOutput(repo, nil, "cat-file", "tag", objectID)
			if readErr != nil {
				return nil, readErr
			}
			tagger, message := tagMetadata(raw)
			findings = append(findings, scanBytes("tagger", objectID, tagger, nil)...)
			findings = append(findings, scanBytes("tag-message", objectID, message, nil)...)
		case "commit", "tree":
		default:
			return nil, fmt.Errorf("unsupported reachable object type %q for %s", bytes.TrimSpace(objectType), objectID)
		}
	}
	return sortedFindings(findings), nil
}

func scanHistoryTree(repo, treeish, subject string, exceptions []legalBlock, used []bool, seenBlobs map[string]bool) ([]policyFinding, error) {
	tree, err := gitOutput(repo, nil, "ls-tree", "-r", "-z", treeish)
	if err != nil {
		return nil, err
	}
	var findings []policyFinding
	for _, record := range bytes.Split(tree, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		tab := bytes.IndexByte(record, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("malformed tree record %q", record)
		}
		fields := strings.Fields(string(record[:tab]))
		if len(fields) != 3 {
			return nil, fmt.Errorf("malformed tree header %q", record[:tab])
		}
		path := string(record[tab+1:])
		findings = append(findings, scanBytes("history-path", subject+":"+path, []byte(path), nil)...)
		if fields[1] != "blob" {
			continue
		}
		seenBlobs[fields[2]] = true
		data, blobErr := gitOutput(repo, nil, "cat-file", "blob", fields[2])
		if blobErr != nil {
			return nil, blobErr
		}
		ranges, rangeErr := verifiedRanges(path, data, exceptions, used)
		if rangeErr != nil {
			return nil, rangeErr
		}
		findings = append(findings, scanBytes("history-content", subject+":"+path, data, ranges)...)
	}
	return findings, nil
}

func verifiedRanges(path string, data []byte, exceptions []legalBlock, used []bool) ([]blockRange, error) {
	var ranges []blockRange
	for index, item := range exceptions {
		if item.Path != path {
			continue
		}
		if item.Rationale == "" || len(item.StartAnchor) == 0 || len(item.EndAnchor) == 0 {
			return nil, fmt.Errorf("legal block %d for %s has incomplete metadata", index, path)
		}
		if sha256.Sum256(data) != item.FileSum {
			return nil, fmt.Errorf("legal block %d for %s has a stale whole-file digest", index, path)
		}
		if item.Start < 0 || item.End <= item.Start || item.End > len(data) {
			return nil, fmt.Errorf("legal block %d for %s has an invalid byte range", index, path)
		}
		if !bytes.HasSuffix(data[:item.Start], item.StartAnchor) || !bytes.HasPrefix(data[item.End:], item.EndAnchor) {
			return nil, fmt.Errorf("legal block %d for %s has stale anchors", index, path)
		}
		if sha256.Sum256(data[item.Start:item.End]) != item.BlockSum {
			return nil, fmt.Errorf("legal block %d for %s has a stale block digest", index, path)
		}
		inner := scanBytes("content", path, data[item.Start:item.End], nil)
		if len(inner) == 0 {
			return nil, fmt.Errorf("legal block %d for %s matches no blocked family", index, path)
		}
		used[index] = true
		ranges = append(ranges, blockRange{start: item.Start, end: item.End})
	}
	return ranges, nil
}

func rejectUnusedBlocks(exceptions []legalBlock, used []bool) error {
	for index := range exceptions {
		if !used[index] {
			return fmt.Errorf("legal block %d for %s is stale", index, exceptions[index].Path)
		}
	}
	return nil
}

func scanBytes(surface, subject string, data []byte, allowed []blockRange) []policyFinding {
	folded := asciiFold(data)
	findings := scanEncodedValues(surface, subject, folded, allowed, blockedFamilies, false)
	if isProseSurface(surface) {
		normalized, originalOffsets := normalizeASCIIWhitespace(folded)
		findings = append(findings, scanEncodedProseValues(surface, subject, normalized, originalOffsets, allowed, blockedNarratives)...)
	}
	return findings
}

func scanEncodedProseValues(surface, subject string, normalized []byte, originalOffsets []int, allowed []blockRange, values []encodedFamily) []policyFinding {
	var findings []policyFinding
	for _, family := range values {
		decoded, err := hex.DecodeString(family.value)
		if err != nil {
			panic(err)
		}
		for cursor := 0; cursor <= len(normalized)-len(decoded); {
			relative := bytes.Index(normalized[cursor:], decoded)
			if relative < 0 {
				break
			}
			offset := cursor + relative
			end := offset + len(decoded)
			originalStart := originalOffsets[offset]
			originalEnd := originalOffsets[end-1] + 1
			if hasWordBoundaries(normalized, offset, end) && !insideAllowedRange(originalStart, originalEnd, allowed) {
				findings = append(findings, policyFinding{Surface: surface, Subject: subject, Offset: originalStart, Family: family.value})
			}
			cursor = offset + 1
		}
	}
	return findings
}

func scanEncodedValues(surface, subject string, folded []byte, allowed []blockRange, values []encodedFamily, wholeWords bool) []policyFinding {
	var findings []policyFinding
	for _, family := range values {
		decoded, err := hex.DecodeString(family.value)
		if err != nil {
			panic(err)
		}
		for cursor := 0; cursor <= len(folded)-len(decoded); {
			relative := bytes.Index(folded[cursor:], decoded)
			if relative < 0 {
				break
			}
			offset := cursor + relative
			if (!wholeWords || hasWordBoundaries(folded, offset, offset+len(decoded))) && !insideAllowedRange(offset, offset+len(decoded), allowed) {
				findings = append(findings, policyFinding{Surface: surface, Subject: subject, Offset: offset, Family: family.value})
			}
			cursor = offset + 1
		}
	}
	return findings
}

func hasWordBoundaries(data []byte, start, end int) bool {
	return (start == 0 || !isASCIIWordByte(data[start-1])) && (end == len(data) || !isASCIIWordByte(data[end]))
}

func isASCIIWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
}

func isProseSurface(surface string) bool {
	switch surface {
	case "content", "history-content", "commit-message", "tag-message":
		return true
	default:
		return false
	}
}

func insideAllowedRange(start, end int, ranges []blockRange) bool {
	for _, item := range ranges {
		if start >= item.start && end <= item.end {
			return true
		}
	}
	return false
}

func asciiFold(data []byte) []byte {
	folded := append([]byte(nil), data...)
	for index, value := range folded {
		if value >= 'A' && value <= 'Z' {
			folded[index] = value + ('a' - 'A')
		}
	}
	return folded
}

func normalizeASCIIWhitespace(data []byte) ([]byte, []int) {
	normalized := make([]byte, 0, len(data))
	originalOffsets := make([]int, 0, len(data))
	inWhitespace := false
	for offset, value := range data {
		if isASCIIWhitespace(value) {
			if !inWhitespace {
				normalized = append(normalized, ' ')
				originalOffsets = append(originalOffsets, offset)
				inWhitespace = true
			}
			continue
		}
		normalized = append(normalized, value)
		originalOffsets = append(originalOffsets, offset)
		inWhitespace = false
	}
	return normalized, originalOffsets
}

func isASCIIWhitespace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

func commitMetadata(raw []byte) (author, committer, message []byte) {
	header, message, _ := bytes.Cut(raw, []byte("\n\n"))
	for _, line := range bytes.Split(header, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("author ")) {
			author = append([]byte(nil), line[len("author "):]...)
		}
		if bytes.HasPrefix(line, []byte("committer ")) {
			committer = append([]byte(nil), line[len("committer "):]...)
		}
	}
	return author, committer, message
}

func tagMetadata(raw []byte) (tagger, message []byte) {
	header, message, _ := bytes.Cut(raw, []byte("\n\n"))
	for _, line := range bytes.Split(header, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("tagger ")) {
			tagger = append([]byte(nil), line[len("tagger "):]...)
		}
	}
	return tagger, message
}

func sortedFindings(findings []policyFinding) []policyFinding {
	sort.Slice(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.Surface != right.Surface {
			return left.Surface < right.Surface
		}
		if left.Subject != right.Subject {
			return left.Subject < right.Subject
		}
		if left.Offset != right.Offset {
			return left.Offset < right.Offset
		}
		return left.Family < right.Family
	})
	return findings
}

func formatFindings(findings []policyFinding) string {
	lines := make([]string, len(findings))
	for index, finding := range findings {
		lines[index] = finding.String()
	}
	return strings.Join(lines, "\n")
}

func decodedFamilies(t *testing.T) [][]byte {
	t.Helper()
	result := make([][]byte, len(blockedFamilies))
	for index, family := range blockedFamilies {
		decoded, err := hex.DecodeString(family.value)
		if err != nil {
			t.Fatal(err)
		}
		result[index] = decoded
	}
	return result
}

func decodedNarratives(t *testing.T) [][]byte {
	t.Helper()
	result := make([][]byte, len(blockedNarratives))
	for index, narrative := range blockedNarratives {
		decoded, err := hex.DecodeString(narrative.value)
		if err != nil {
			t.Fatal(err)
		}
		result[index] = decoded
	}
	return result
}

func assertHasSurface(t *testing.T, findings []policyFinding, surface string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Surface == surface {
			return
		}
	}
	t.Fatalf("findings do not include surface %q: %v", surface, findings)
}

func initTempRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, nil, "init", "-q", "-b", "main")
	runGit(t, repo, nil, "config", "user.name", "FlowBaton Builder")
	runGit(t, repo, nil, "config", "user.email", "builder@example.invalid")
	runGit(t, repo, nil, "config", "commit.gpgsign", "false")
	return repo
}

func writeAndAdd(t *testing.T, repo, path string, data []byte) {
	t.Helper()
	absolute := filepath.Join(repo, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, nil, "add", "--", path)
}

func commitFile(t *testing.T, repo, path string, data []byte, message string, env map[string]string) {
	t.Helper()
	writeAndAdd(t, repo, path, data)
	runGit(t, repo, env, "commit", "-q", "-m", message)
}

func runGit(t *testing.T, repo string, env map[string]string, args ...string) {
	t.Helper()
	if _, err := gitOutput(repo, env, args...); err != nil {
		t.Fatal(err)
	}
}

func mustGitOutput(t *testing.T, repo string, env map[string]string, args ...string) []byte {
	t.Helper()
	output, err := gitOutput(repo, env, args...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func gitOutput(repo string, env map[string]string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", repo}, args...)
	cmd := exec.Command("git", commandArgs...)
	cmd.Env = sanitizedGitEnvironment()
	for key, value := range env {
		if !allowedMetadataEnvironmentKey(key) {
			return nil, fmt.Errorf("git environment override %q is not allowed", key)
		}
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(output))
	}
	return output, nil
}

func allowedMetadataEnvironmentKey(key string) bool {
	switch key {
	case "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE":
		return true
	default:
		return false
	}
}

func sanitizedGitEnvironment() []string {
	result := gitEnvironmentWithoutAmbientState()
	result = append(result, "GIT_NO_REPLACE_OBJECTS=1")
	return result
}

func gitEnvironmentWithoutAmbientState() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "GIT_") {
			continue
		}
		result = append(result, item)
	}
	return result
}
