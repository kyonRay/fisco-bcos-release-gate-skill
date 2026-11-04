package workspace

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/fbterr"
)

// DefectsPath is the file holding this run's defect rows: the archived record once the run has
// been cleaned up, the engine's live file while the cluster directory still exists.
func (w Workspace) DefectsPath() string {
	if _, err := os.Stat(w.Failures); err == nil {
		return w.Failures
	}
	return w.ClusterFailures
}

// Archive removes the cluster directory and keeps the run's record. A round leaves about 1.5GB
// there (an 878MB binary copy and four RocksDB directories), and a 3.18.0 node refuses to start
// with under 1GB free, so a few leftover rounds stop the next one from running.
//
// Defect rows always survive. Node logs and other small files survive when keepLogs is set or
// when the run recorded a defect, because a defect row names them as its evidence. Binaries, chain
// data and key material never do. The caller must have confirmed that no node is still running.
func (w Workspace) Archive(keepLogs bool) error {
	defects, err := w.moveDefects()
	if err != nil {
		return err
	}
	if keepLogs || defects {
		if err := w.harvest(); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(w.Cluster); err != nil {
		return fbterr.Infraf("cannot remove %s: %v", w.Cluster, err)
	}
	return nil
}

// moveDefects appends the engine's rows to the record beside events.jsonl and repoints their
// evidence paths at where the harvest puts those files. It reports whether there were any rows.
func (w Workspace) moveDefects() (bool, error) {
	rows, err := os.ReadFile(w.ClusterFailures)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fbterr.Infraf("cannot read %s: %v", w.ClusterFailures, err)
	}
	if len(bytes.TrimSpace(rows)) == 0 {
		return false, nil
	}
	rows = bytes.ReplaceAll(rows, []byte(w.Cluster), []byte(w.Evidence))
	f, err := os.OpenFile(w.Failures, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, fbterr.Infraf("cannot write %s: %v", w.Failures, err)
	}
	defer f.Close()
	if _, err := f.Write(rows); err != nil {
		return false, fbterr.Infraf("cannot write %s: %v", w.Failures, err)
	}
	return true, nil
}

// harvest moves everything a person reads after a failure into Evidence, at the same relative
// path. Directories holding chain data or keys are skipped whole. Executables are skipped because
// that is what the node binary copies are, whatever an upgrade timeline named them.
func (w Workspace) harvest() error {
	err := filepath.WalkDir(w.Cluster, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) { // a run that never built a cluster
				return nil
			}
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "data", "conf", "ca", "sdk":
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || path == w.ClusterFailures {
			return nil
		}
		switch filepath.Ext(path) {
		case ".pem", ".key", ".crt", ".srl":
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&0o111 != 0 {
			return nil
		}
		rel, err := filepath.Rel(w.Cluster, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(w.Evidence, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Rename(path, dst)
	})
	if err != nil {
		return fbterr.Infraf("cannot keep the logs of %s: %v", w.Cluster, err)
	}
	return nil
}
