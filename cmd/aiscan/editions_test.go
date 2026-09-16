package main

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
)

// editionsFile is the tag contract shared with the Makefile and the CI
// workflows; it is resolved relative to this package directory.
const editionsFile = "../../editions.env"

func readEditions(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(editionsFile))
	if err != nil {
		t.Fatalf("read %s: %v", editionsFile, err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("%s: %q is not a KEY=VALUE line", editionsFile, line)
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values
}

func editionValue(t *testing.T, values map[string]string, key string) string {
	t.Helper()
	value, ok := values[key]
	if !ok {
		t.Fatalf("%s does not declare %s", editionsFile, key)
	}
	return value
}

func sortedTags(value string) []string {
	tags := strings.Fields(value)
	slices.Sort(tags)
	return tags
}

func buildSetting(t *testing.T, key string) string {
	t.Helper()
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("the test binary carries no build info")
	}
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}

// assertEditionTags compares the tags this test binary was built with against
// the declared sets for one edition. It is an exact comparison on purpose: a
// tag that goes missing does not break a build, it silently stops the files it
// gates from compiling, so the suites those files carry disappear while CI
// stays green. Comparing the whole set is the only thing that notices.
//
// Two spellings are legitimate — the edition's full tag set, and the
// capability-only set the CI suites use, which omits the base policy tags.
func assertEditionTags(t *testing.T, edition string) {
	t.Helper()
	values := readEditions(t)
	got := sortedTags(strings.ReplaceAll(buildSetting(t, "-tags"), ",", " "))

	var declared []string
	for _, suffix := range []string{"_TAGS", "_CAPS_TAGS"} {
		key := edition + suffix
		want := sortedTags(editionValue(t, values, key))
		if slices.Equal(got, want) {
			return
		}
		declared = append(declared, key+" = ["+strings.Join(want, " ")+"]")
	}
	t.Fatalf("built with tags [%s], which is not a declared %s set\n  %s",
		strings.Join(got, " "), edition, strings.Join(declared, "\n  "))
}

// assertEditionCGO checks the declared CGO_ENABLED for an edition that requires
// cgo. `full` enables the native libcstx runtime and the static RE2 backend,
// both of which exist only under cgo, so a full build that quietly ran with
// CGO_ENABLED=0 would select different files and test a different binary — the
// same reason ci.yml refuses to build full with cgo off.
func assertEditionCGO(t *testing.T, edition string) {
	t.Helper()
	want := editionValue(t, readEditions(t), edition+"_CGO")
	if got := buildSetting(t, "CGO_ENABLED"); got != want {
		t.Fatalf("CGO_ENABLED = %q, want %q for the %s edition", got, want, edition)
	}
}

// TestEditionsAreConsistent covers what editions.env cannot express for itself:
// the file is read without expansion, so every complete tag set is spelled out
// and has to be kept in step with the base and capability groups by hand.
func TestEditionsAreConsistent(t *testing.T) {
	values := readEditions(t)
	base := sortedTags(editionValue(t, values, "BASE_TAGS"))

	for _, edition := range []string{"STANDARD", "FULL", "RECORD"} {
		full := sortedTags(editionValue(t, values, edition+"_TAGS"))
		caps := sortedTags(editionValue(t, values, edition+"_CAPS_TAGS"))

		want := slices.Concat(base, caps)
		slices.Sort(want)
		if !slices.Equal(full, want) {
			t.Errorf("%s_TAGS = [%s], want BASE_TAGS + %s_CAPS_TAGS = [%s]",
				edition, strings.Join(full, " "), edition, strings.Join(want, " "))
		}
	}

	// RECORD_TAGS is the widest set, so it is the yardstick for catching a tag
	// that was misspelled into matching nothing anywhere.
	widest := sortedTags(editionValue(t, values, "RECORD_TAGS"))
	for _, key := range []string{
		"BASE_TAGS", "STANDARD_CAPS_TAGS", "FULL_CAPS_TAGS", "RECORD_CAPS_TAGS",
		"STANDARD_TAGS", "FULL_TAGS", "RE2_CGO_TAGS",
	} {
		for _, tag := range sortedTags(editionValue(t, values, key)) {
			if !slices.Contains(widest, tag) {
				t.Errorf("%s contains %q, which no edition enables", key, tag)
			}
		}
	}
}
