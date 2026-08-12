package foundation_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestModuleIdentity(t *testing.T) {
	contents := readFile(t, "go.mod")
	for _, want := range []string{
		"module github.com/larchwave/flowbaton",
		// go-ios requires Go 1.26, and the CI toolchain (1.26.1) satisfies that
		// minimum. See docs/dependency-policy.md.
		"go 1.26.0",
	} {
		if !strings.Contains(contents, want) {
			t.Errorf("go.mod does not contain %q", want)
		}
	}
}

func TestGovernanceDocuments(t *testing.T) {
	required := map[string][]string{
		"README.md":                             {"FlowBaton", "pre-alpha"},
		"LICENSE":                               {"Apache License", "Version 2.0"},
		"SECURITY.md":                           {"Security Policy", "public"},
		"CODE_OF_CONDUCT.md":                    {"Code of Conduct", "enforcement"},
		"THIRD_PARTY_NOTICES.md":                {"Third-Party Notices", "Apache-2.0"},
		"docs/dependency-policy.md":             {"Dependency Policy", "pinned"},
		"docs/decisions/0001-public-release.md": {"Accepted", "Apache-2.0", "attestation"},
		"docs/release-policy.md":                {"Release Policy", "publicly distributed", "distributed-v1"},
		"docs/support-matrix.md":                {"Support Matrix", "fail before device mutation"},
		"governance/support.json":               {"fail_closed"},
	}

	for path, tokens := range required {
		contents := readFile(t, path)
		for _, token := range tokens {
			if !strings.Contains(contents, token) {
				t.Errorf("%s does not contain %q", path, token)
			}
		}
	}
}

func TestRepoRootIsAbsoluteWithTrimmedSourcePaths(t *testing.T) {
	root := repoRoot(t)
	if !filepath.IsAbs(root) {
		t.Fatalf("repository root = %q, want an absolute path independent of compiler source paths", root)
	}
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read repository go.mod: %v", err)
	}
	if !strings.Contains(string(contents), "module github.com/larchwave/flowbaton") {
		t.Fatalf("repository root %q does not contain the FlowBaton module", root)
	}
}

func TestSupportLedger(t *testing.T) {
	var ledger struct {
		SchemaVersion int    `json:"schema_version"`
		Product       string `json:"product"`
		HostTargets   []struct {
			OS     string `json:"os"`
			Arch   string `json:"arch"`
			Status string `json:"status"`
		} `json:"host_targets"`
		Features     map[string][]string `json:"features"`
		Distribution struct {
			Decision       string `json:"decision"`
			Visibility     string `json:"visibility"`
			License        string `json:"license"`
			StudioIncluded bool   `json:"studio_included"`
		} `json:"distribution"`
	}
	decodeJSON(t, "governance/support.json", &ledger)
	if ledger.SchemaVersion != 1 || ledger.Product != "FlowBaton" {
		t.Fatalf("unexpected support ledger identity: %+v", ledger)
	}

	gotTargets := make([]string, 0, len(ledger.HostTargets))
	for _, target := range ledger.HostTargets {
		if target.Status != "ga-gated" {
			t.Errorf("target %s/%s status = %q, want ga-gated", target.OS, target.Arch, target.Status)
		}
		gotTargets = append(gotTargets, target.OS+"/"+target.Arch)
	}
	sort.Strings(gotTargets)
	wantTargets := []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "windows/amd64"}
	if strings.Join(gotTargets, ",") != strings.Join(wantTargets, ",") {
		t.Fatalf("host targets = %v, want %v", gotTargets, wantTargets)
	}
	for _, class := range []string{"v1", "fail_closed", "post_v1", "excluded"} {
		if len(ledger.Features[class]) == 0 {
			t.Errorf("feature class %q is empty", class)
		}
	}
	if ledger.Distribution.Decision != "docs/decisions/0001-public-release.md" ||
		ledger.Distribution.Visibility != "public" || ledger.Distribution.License != "Apache-2.0" ||
		ledger.Distribution.StudioIncluded {
		t.Errorf("unexpected support distribution boundary: %+v", ledger.Distribution)
	}
}

func TestGitHubActionsAreImmutableAndLeastPrivilege(t *testing.T) {
	workflows, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(workflows) < 2 {
		t.Fatalf("found %d workflows, want CI and release workflows", len(workflows))
	}

	usesLine := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^\s#]+)`)
	immutableRef := regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
	for _, workflow := range workflows {
		contents := readAbsoluteFile(t, workflow)
		if strings.Contains(contents, "pull_request_target") {
			t.Errorf("%s must not use pull_request_target", filepath.Base(workflow))
		}
		if !strings.Contains(contents, "permissions:") {
			t.Errorf("%s has no explicit permissions", filepath.Base(workflow))
		}
		matches := usesLine.FindAllStringSubmatch(contents, -1)
		if len(matches) == 0 {
			t.Errorf("%s uses no pinned actions", filepath.Base(workflow))
		}
		for _, match := range matches {
			if strings.HasPrefix(match[1], "./") {
				continue
			}
			if !immutableRef.MatchString(match[1]) {
				t.Errorf("%s has mutable action pin %q", filepath.Base(workflow), match[1])
			}
		}
	}
}

func TestReleaseSkeletonIsFailClosed(t *testing.T) {
	config := readFile(t, ".goreleaser.yaml")
	for _, want := range []string{
		"version: 2",
		"project_name: flowbaton",
		"CGO_ENABLED=0",
		"goos:",
		"goarch:",
		"-trimpath",
		"checksums.txt",
		"sboms:",
		"draft: true",
	} {
		if !strings.Contains(config, want) {
			t.Errorf(".goreleaser.yaml does not contain %q", want)
		}
	}

	workflow := readFile(t, ".github/workflows/release.yml")
	for _, want := range []string{
		"GORELEASER_VERSION: v2.17.0",
		"SYFT_VERSION: v1.42.3",
		"args: release --snapshot --clean",
		"retention-days: 7",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release workflow does not contain %q", want)
		}
	}
	for _, forbidden := range []string{
		"actions/attest@",
		"artifact-metadata: write",
		"attestations: write",
		"id-token: write",
		"contents: write",
		"GITHUB_TOKEN",
		"private-release:",
		"release --clean",
		"tags: ['v*']",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("release workflow contains an unreviewed publishing capability %q", forbidden)
		}
	}
}

func TestWorkflowsFailClosedUnlessRepositoryIsPublic(t *testing.T) {
	var workflows []string
	for _, extension := range []string{"*.yml", "*.yaml"} {
		matches, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", extension))
		if err != nil {
			t.Fatalf("find %s workflows: %v", extension, err)
		}
		workflows = append(workflows, matches...)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows found")
	}
	for _, workflowPath := range workflows {
		workflow := readAbsoluteFile(t, workflowPath)
		jobs := parseWorkflowJobNeeds(t, workflow)
		path := filepath.ToSlash(workflowPath)
		isCI := filepath.Base(workflowPath) == "ci.yml"
		for _, want := range []string{
			"public-oss-boundary:",
			"github.event.repository.visibility",
			"test \"$REPOSITORY_VISIBILITY\" = 'public'",
		} {
			if !strings.Contains(workflow, want) {
				t.Errorf("%s does not contain public OSS gate %q", path, want)
			}
		}
		if _, ok := jobs["public-oss-boundary"]; !ok {
			t.Errorf("%s has no structurally parsed public-oss-boundary job", path)
		}
		if isCI {
			if got, ok := jobs["repository-language"]; !ok || got != "public-oss-boundary" {
				t.Errorf("%s repository-language job needs %q, present=%t; want public-oss-boundary", path, got, ok)
			}
		}
		for job, needs := range jobs {
			if job == "public-oss-boundary" {
				continue
			}
			if !workflowJobNeedsBoundary(jobs, job, map[string]bool{}) {
				t.Errorf("%s job %s needs %q but has no dependency path to public-oss-boundary", path, job, needs)
			}
		}
		if len(jobs) < 2 {
			t.Errorf("%s parsed only %d job(s), want a visibility guard and at least one gated job", path, len(jobs))
		}
	}
}

func workflowJobNeedsBoundary(jobs map[string]string, job string, visiting map[string]bool) bool {
	if job == "public-oss-boundary" {
		return true
	}
	if visiting[job] {
		return false
	}
	visiting[job] = true
	defer delete(visiting, job)
	for _, dependency := range parseWorkflowNeeds(jobs[job]) {
		if _, exists := jobs[dependency]; exists && workflowJobNeedsBoundary(jobs, dependency, visiting) {
			return true
		}
	}
	return false
}

func parseWorkflowNeeds(value string) []string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	}
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	dependencies := make([]string, 0, len(parts))
	for _, part := range parts {
		if dependency := strings.TrimSpace(part); dependency != "" {
			dependencies = append(dependencies, dependency)
		}
	}
	return dependencies
}

func TestWorkflowJobParserExposesUnknownUngatedJob(t *testing.T) {
	workflow := `name: fixture
jobs:
  public-oss-boundary:
    runs-on: ubuntu-latest
  known:
    needs: public-oss-boundary
    runs-on: ubuntu-latest
  future-job:
    runs-on: ubuntu-latest
`
	jobs := parseWorkflowJobNeeds(t, workflow)
	if got := jobs["known"]; got != "public-oss-boundary" {
		t.Fatalf("known job needs %q, want public-oss-boundary", got)
	}
	if got, ok := jobs["future-job"]; !ok || got != "" {
		t.Fatalf("future ungated job parsed as needs=%q, present=%t; want present with empty needs", got, ok)
	}
}

func TestWorkflowBoundaryDependencyAcceptsFanInAndRejectsCycles(t *testing.T) {
	jobs := map[string]string{
		"public-oss-boundary": "",
		"signed-tag":          "public-oss-boundary",
		"android":             "[public-oss-boundary, signed-tag]",
		"ios":                 "signed-tag",
		"candidate":           "[android, ios]",
		"cycle-a":             "cycle-b",
		"cycle-b":             "cycle-a",
	}
	for _, job := range []string{"signed-tag", "android", "ios", "candidate"} {
		if !workflowJobNeedsBoundary(jobs, job, map[string]bool{}) {
			t.Errorf("%s should have a dependency path to the boundary", job)
		}
	}
	for _, job := range []string{"cycle-a", "cycle-b"} {
		if workflowJobNeedsBoundary(jobs, job, map[string]bool{}) {
			t.Errorf("%s cycle was accepted as boundary-gated", job)
		}
	}
}

func TestPublicDeliverySurfaceManifestIsDecidable(t *testing.T) {
	type probeProfile struct {
		Mode                string   `json:"mode"`
		Tool                string   `json:"tool"`
		CommandTemplate     string   `json:"command_template"`
		CredentialIsolation []string `json:"credential_isolation"`
		RedirectPolicy      string   `json:"redirect_policy"`
		ExpectedOutcomes    []string `json:"expected_outcomes"`
		ForbiddenMetadata   []string `json:"forbidden_metadata"`
	}
	type surface struct {
		ID                       string   `json:"id"`
		Kind                     string   `json:"kind"`
		Coordinate               string   `json:"coordinate"`
		Lifecycle                string   `json:"lifecycle"`
		RequiredForDistributedV1 bool     `json:"required_for_distributed_v1"`
		RequiredVisibility       string   `json:"required_visibility"`
		AnonymousProbeProfiles   []string `json:"anonymous_probe_profiles"`
	}
	var manifest struct {
		SchemaVersion  int                     `json:"schema_version"`
		PolicyVersion  string                  `json:"policy_version"`
		CanonicalOwner string                  `json:"canonical_owner"`
		License        string                  `json:"license"`
		EvidenceFields []string                `json:"evidence_fields"`
		ProbeProfiles  map[string]probeProfile `json:"probe_profiles"`
		Surfaces       []surface               `json:"surfaces"`
	}
	if err := json.Unmarshal([]byte(readFile(t, "governance/public-delivery-surfaces.json")), &manifest); err != nil {
		t.Fatalf("parse public delivery manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 || manifest.PolicyVersion != "public-oss-v1" || manifest.CanonicalOwner != "larchwave" || manifest.License != "Apache-2.0" {
		t.Errorf("unexpected public delivery manifest identity: %+v", manifest)
	}
	requiredEvidence := []string{"manifest_sha256", "surface_id", "profile_id", "tool_version", "timestamp_utc", "exit_code", "response_sha256", "artifact_sha256", "signature_verified", "result"}
	for _, field := range requiredEvidence {
		if !containsString(manifest.EvidenceFields, field) {
			t.Errorf("public delivery evidence_fields omits %q", field)
		}
	}
	seenKinds := map[string]bool{}
	for _, item := range manifest.Surfaces {
		if item.ID == "" || item.Kind == "" || item.Coordinate == "" || item.Lifecycle == "" {
			t.Errorf("public delivery surface has incomplete identity: %+v", item)
			continue
		}
		seenKinds[item.Kind] = true
		if !item.RequiredForDistributedV1 {
			continue
		}
		if item.RequiredVisibility != "public" || len(item.AnonymousProbeProfiles) == 0 {
			t.Errorf("required surface %s has incomplete public probe contract", item.ID)
		}
		for _, profileID := range item.AnonymousProbeProfiles {
			profile, ok := manifest.ProbeProfiles[profileID]
			if !ok {
				t.Errorf("surface %s points to missing probe profile %s", item.ID, profileID)
				continue
			}
			if profile.Mode == "" || profile.Tool == "" || profile.CommandTemplate == "" || len(profile.CredentialIsolation) == 0 || profile.RedirectPolicy == "" || len(profile.ExpectedOutcomes) == 0 || len(profile.ForbiddenMetadata) == 0 {
				t.Errorf("probe profile %s is incomplete: %+v", profileID, profile)
			}
		}
	}
	for _, kind := range []string{"source_repository", "release_assets", "homebrew_tap", "installer", "project_documentation", "attestation"} {
		if !seenKinds[kind] {
			t.Errorf("public delivery surface manifest omits kind %q", kind)
		}
	}
}

func TestCommitHistoryUsesLoreTrailers(t *testing.T) {
	// These exact objects predate the Lore requirement. Keeping the list exact
	// avoids rewriting published objects and makes any policy widening visible.
	grandfathered := map[string]bool{
		"9acd46adfc5889a46ddc029f78e1b389f25c66ac": false,
		"3b3b22b886a27112eac6e2e712b8f86ee7d2d155": false,
		"61cf4b2603d7e407819ca5f18500e6f1223c1cfa": false,
		"433b8b065737afaa0eec019992877ce1c46cfa0b": false,
		"57695eff1cc4c8657f759d7beeb4515fe8dc32a1": false,
		"ed8f151475811148ecfb2e672468fd998c4a2561": false,
		"9a53db626da6c84af05e57a9500d0ccf8e93746c": false,
		"ba1a3b6004f7b5c1b28dc83f616b3649bd453e7e": false,
		"153e578a81cd0ab23b343054ebfc5082bfc4324e": false,
	}
	if len(grandfathered) != 9 {
		t.Fatalf("Lore grandfather set has %d entries, want exactly 9", len(grandfathered))
	}
	hasGitMetadata, err := repositoryHasGitMetadata(repoRoot(t))
	if err != nil {
		t.Fatalf("inspect repository Git metadata: %v", err)
	}
	if !hasGitMetadata {
		t.Skip("source archive has no Git metadata")
	}
	cmd := exec.Command("git", "log", "--format=%H%x00%s%x00%b%x00")
	cmd.Dir = repoRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// An initialized repository with no commits is valid during the RED/GREEN bootstrap.
		if strings.Contains(string(output), "does not have any commits") {
			return
		}
		t.Fatalf("inspect Git history: %v: %s", err, output)
	}
	fields := strings.Split(strings.TrimSuffix(string(output), "\x00\n"), "\x00")
	for len(fields) >= 3 {
		hash, subject, body := fields[0], fields[1], fields[2]
		fields = fields[3:]
		hash = strings.TrimSpace(hash)
		if strings.TrimSpace(subject) == "" {
			t.Errorf("commit %s has an empty intent line", hash)
		}
		if _, ok := grandfathered[hash]; ok {
			grandfathered[hash] = true
			complete := true
			for _, trailer := range []string{"Confidence:", "Scope-risk:", "Tested:", "Not-tested:"} {
				complete = complete && strings.Contains(body, trailer)
			}
			if complete {
				t.Errorf("Lore grandfather entry %s already has every required trailer", hash)
			}
			continue
		}
		for _, trailer := range []string{"Confidence:", "Scope-risk:", "Tested:", "Not-tested:"} {
			if !strings.Contains(body, trailer) {
				t.Errorf("commit %s is missing %s Lore trailer", hash, trailer)
			}
		}
	}
	for hash, seen := range grandfathered {
		if !seen {
			t.Errorf("Lore grandfather entry %s is not present in repository history", hash)
		}
	}
}

func TestHistoryPolicyWorkflowsFetchCompleteHistory(t *testing.T) {
	for _, test := range []struct {
		path string
		job  string
	}{
		{path: ".github/workflows/ci.yml", job: "audit"},
		{path: ".github/workflows/ci.yml", job: "portable-tests"},
		{path: ".github/workflows/release-publish.yml", job: "go-contract-policy"},
	} {
		workflow := readFile(t, test.path)
		pattern := regexp.MustCompile(`(?ms)^  ` + regexp.QuoteMeta(test.job) + `:\n.*?(?:^  [a-zA-Z0-9_-]+:\n|\z)`)
		job := pattern.FindString(workflow)
		if job == "" {
			t.Errorf("%s does not contain job %q", test.path, test.job)
			continue
		}
		if !strings.Contains(job, "fetch-depth: 0") {
			t.Errorf("%s job %q runs history policy without a complete checkout", test.path, test.job)
		}
	}
}

func TestGoRaceWorkflowsExercisePostgres(t *testing.T) {
	const postgresImage = "postgres:17.6-alpine3.22@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94"
	for _, path := range []string{
		".github/workflows/ci.yml",
		".github/workflows/release-publish.yml",
	} {
		workflow := readFile(t, path)
		for _, want := range []string{
			"FLOWBATON_TEST_POSTGRES_URL: postgres://flowbaton_ci:flowbaton_ci@127.0.0.1:5432/flowbaton_test?sslmode=disable",
			"image: " + postgresImage,
			"--health-cmd \"pg_isready -U flowbaton_ci -d flowbaton_test\"",
			"go test -race ./...",
		} {
			if !strings.Contains(workflow, want) {
				t.Errorf("%s does not contain required PostgreSQL race-gate wiring %q", path, want)
			}
		}
	}
}

func TestRepositoryGitMetadataGuard(t *testing.T) {
	archiveRoot := t.TempDir()
	hasGitMetadata, err := repositoryHasGitMetadata(archiveRoot)
	if err != nil {
		t.Fatal(err)
	}
	if hasGitMetadata {
		t.Fatal("empty source archive unexpectedly reports Git metadata")
	}
	if err := os.WriteFile(filepath.Join(archiveRoot, ".git"), []byte("gitdir: /detached/worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hasGitMetadata, err = repositoryHasGitMetadata(archiveRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !hasGitMetadata {
		t.Fatal("worktree-style .git file was not recognized")
	}
}

func parseWorkflowJobNeeds(t *testing.T, workflow string) map[string]string {
	t.Helper()
	jobHeader := regexp.MustCompile(`^  ([A-Za-z0-9_-]+):(?:\s+#.*)?$`)
	jobs := map[string]string{}
	inJobs := false
	currentJob := ""
	for lineNumber, line := range strings.Split(workflow, "\n") {
		if line == "jobs:" {
			if inJobs {
				t.Fatalf("workflow has duplicate top-level jobs map at line %d", lineNumber+1)
			}
			inJobs = true
			continue
		}
		if !inJobs || strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.HasPrefix(line, "\t") {
			t.Fatalf("workflow jobs map uses a tab at line %d", lineNumber+1)
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			break
		}
		if indent == 2 {
			match := jobHeader.FindStringSubmatch(line)
			if match == nil {
				t.Fatalf("workflow job header at line %d is not in canonical two-space form: %q", lineNumber+1, line)
			}
			currentJob = match[1]
			if _, duplicate := jobs[currentJob]; duplicate {
				t.Fatalf("workflow contains duplicate job %q", currentJob)
			}
			jobs[currentJob] = ""
			continue
		}
		if indent == 4 && currentJob != "" {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "needs:") {
				jobs[currentJob] = strings.TrimSpace(strings.TrimPrefix(trimmed, "needs:"))
			}
		}
	}
	if !inJobs || len(jobs) == 0 {
		t.Fatal("workflow has no canonical top-level jobs map")
	}
	return jobs
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatalf("get test working directory: %v", err)
	}
	for {
		modulePath := filepath.Join(current, "go.mod")
		contents, readErr := os.ReadFile(modulePath)
		if readErr == nil && strings.Contains(string(contents), "module github.com/larchwave/flowbaton") {
			return current
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatalf("read %s while locating repository root: %v", modulePath, readErr)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	t.Fatalf("locate FlowBaton repository root from test working directory")
	return ""
}

func repositoryHasGitMetadata(root string) (bool, error) {
	_, err := os.Stat(filepath.Join(root, ".git"))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	return readAbsoluteFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(path)))
}

func readAbsoluteFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(strings.TrimSpace(string(contents))) == 0 {
		t.Fatalf("%s is empty", path)
	}
	return string(contents)
}

func decodeJSON(t *testing.T, path string, target any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(readFile(t, path)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}
