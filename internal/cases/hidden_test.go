package cases

import (
	"os"
	"path/filepath"
	"testing"
)

// macOS tar writes an AppleDouble twin (._name) next to every file unless told not to; an install
// shipped that way carried ._example.case into share/fbt/cases, and the registry parsed the
// resource fork as a fixture. Hidden files are never fixtures.
func TestHiddenFilesAreNotFixtures(t *testing.T) {
	dir := t.TempDir()
	good := "[case]\nstatus = active\nprofile = default-latest\ninput = true\nexpect_oracle = pass\n"
	if err := os.WriteFile(filepath.Join(dir, "live.case"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "._live.case"), []byte("\x00\x05\x16\x07 Mac OS X"), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := Registry{Shipped: dir}.List()
	if err != nil {
		t.Fatalf("a hidden twin broke the whole registry: %v", err)
	}
	if len(all) != 1 || all[0].Name != "live.case" {
		t.Errorf("cases = %+v, want only live.case", all)
	}
}
