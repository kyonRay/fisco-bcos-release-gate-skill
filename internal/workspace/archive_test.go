package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// layoutRun lays out what a finished round leaves behind.
func layoutRun(t *testing.T, failures string) Workspace {
	t.Helper()
	w, err := Create(t.TempDir(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]os.FileMode{
		"127.0.0.1/fisco-bcos":            0o755,
		"127.0.0.1/stop_all.sh":           0o755,
		"127.0.0.1/node0/fisco-bcos.old":  0o755,
		"127.0.0.1/node0/config.ini":      0o644,
		"127.0.0.1/node0/log/log_1.log":   0o644,
		"127.0.0.1/node0/nohup.out":       0o644,
		"127.0.0.1/node0/data/000001.sst": 0o644,
		"127.0.0.1/node0/conf/node.pem":   0o600,
		"ca/accounts/0xabc.pem":           0o600,
		"sdk/sdk.key":                     0o600,
		"127.0.0.1/node0/loose.key":       0o600,
		"malformed_x.log":                 0o644,
	}
	for rel, mode := range files {
		p := filepath.Join(w.Cluster, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
	}
	if failures != "" {
		if err := os.WriteFile(w.ClusterFailures, []byte(failures), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(w.Events, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return w
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestArchiveOfAPassingRunLeavesOnlyTheTranscript(t *testing.T) {
	w := layoutRun(t, "")
	if err := w.Archive(false); err != nil {
		t.Fatal(err)
	}
	if exists(w.Cluster) {
		t.Error("the cluster directory survived")
	}
	if !exists(w.Events) {
		t.Error("the transcript was removed")
	}
	if exists(w.Failures) {
		t.Error("a run with no defects has a failures.jsonl")
	}
	if left, _ := os.ReadDir(w.Evidence); len(left) != 0 {
		t.Errorf("a passing run kept evidence: %v", left)
	}
}

func TestArchiveOfAFailedRunKeepsLogsAndNeverBinariesDataOrKeys(t *testing.T) {
	w := layoutRun(t, "")
	if err := w.Archive(true); err != nil {
		t.Fatal(err)
	}
	if exists(w.Cluster) {
		t.Error("the cluster directory survived")
	}
	for _, rel := range []string{"127.0.0.1/node0/log/log_1.log", "127.0.0.1/node0/nohup.out",
		"127.0.0.1/node0/config.ini", "malformed_x.log"} {
		if !exists(filepath.Join(w.Evidence, rel)) {
			t.Errorf("%s was not kept", rel)
		}
	}
	_ = filepath.WalkDir(w.Evidence, func(p string, d os.DirEntry, _ error) error {
		rel, _ := filepath.Rel(w.Evidence, p)
		for _, banned := range []string{"fisco-bcos", "stop_all", "data", "conf", "ca", "sdk", ".pem", ".key"} {
			if strings.Contains(rel, banned) && rel != "127.0.0.1/node0/config.ini" {
				t.Errorf("%s was kept", rel)
			}
		}
		return nil
	})
}

// The engine writes failures.jsonl inside the directory Archive removes, and each row's evidence
// field names that directory. Deleting without moving would lose every defect from `fbt report`
// and the defect ledger.
func TestDefectsSurviveAndTheirEvidencePointsAtTheKeptLogs(t *testing.T) {
	w, err := Create(t.TempDir(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(w.Cluster, "127.0.0.1/node0/log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Cluster, "127.0.0.1/node0/log/log_1.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	row := `{"oracle":"crash","repro":"bash run_case.sh c.case","evidence":"` + w.Cluster + `/127.0.0.1","reported":false}` + "\n"
	if err := os.WriteFile(w.ClusterFailures, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := w.DefectsPath(); got != w.ClusterFailures {
		t.Errorf("before Archive DefectsPath = %s", got)
	}
	// keepLogs false: a recorded defect keeps its logs by itself, because it names them.
	if err := w.Archive(false); err != nil {
		t.Fatal(err)
	}
	if got := w.DefectsPath(); got != w.Failures {
		t.Errorf("after Archive DefectsPath = %s", got)
	}
	raw, err := os.ReadFile(w.Failures)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Oracle, Repro, Evidence string }
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Oracle != "crash" || got.Repro != "bash run_case.sh c.case" {
		t.Errorf("the row changed: %+v", got)
	}
	if got.Evidence != filepath.Join(w.Evidence, "127.0.0.1") {
		t.Errorf("evidence = %s", got.Evidence)
	}
	if !exists(filepath.Join(got.Evidence, "node0/log/log_1.log")) {
		t.Error("the row's evidence path does not hold the log")
	}
}

func TestArchiveOfARunThatNeverBuiltAClusterIsANoOp(t *testing.T) {
	w, err := Create(t.TempDir(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Archive(true); err != nil {
		t.Fatalf("Archive = %v", err)
	}
}
