package cases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func selectFixture(t *testing.T) []Case {
	t.Helper()
	dir := t.TempDir()
	for name, status := range map[string]string{
		"fuzz_seed43_idx11.case": "pending", "fuzz_seed7_idx2.case": "active", "example.case": "example", "live.case": "active",
	} {
		body := "[case]\nstatus = " + status + "\nprofile = default-latest\ninput = true\nexpect_oracle = pass\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	all, err := Registry{Shipped: dir}.List()
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func names(cs []Case) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return strings.Join(out, ",")
}

func TestSelectMatchesTheBasenameWithOrWithoutTheSuffix(t *testing.T) {
	all := selectFixture(t)
	for _, glob := range []string{"fuzz_seed7_idx2", "fuzz_seed7_idx2.case", "fuzz_*", "*"} {
		got, err := Select(all, glob)
		if err != nil {
			t.Errorf("%q: %v", glob, err)
			continue
		}
		if !strings.Contains(names(got), "fuzz_seed7_idx2.case") {
			t.Errorf("%q selected %s", glob, names(got))
		}
		for _, c := range got {
			if !c.Sweepable() {
				t.Errorf("%q selected %s, which is %s", glob, c.Name, c.Status)
			}
		}
	}
}

func TestSelectRefusesAGlobThatMatchesNothingActive(t *testing.T) {
	all := selectFixture(t)
	if _, err := Select(all, "fuzz_seed43*"); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Errorf("a glob that matches only a pending fixture must say so: %v", err)
	}
	if _, err := Select(all, "nothing*"); err == nil || !strings.Contains(err.Error(), "matches no case") {
		t.Errorf("a glob that matches nothing must say so: %v", err)
	}
}

func TestASymlinkedCaseIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	elsewhere := t.TempDir()
	body := "[case]\nstatus = active\nprofile = default-latest\ninput = true\nexpect_oracle = pass\n"
	if err := os.WriteFile(filepath.Join(elsewhere, "far.case"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "far.case"), filepath.Join(dir, "near.case")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	all, err := Registry{Shipped: dir}.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("a symlinked .case was listed: %s", names(all))
	}
}
