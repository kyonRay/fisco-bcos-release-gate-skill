package clusters

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// nodeName pulls node0 / node12 out of a fisco-bcos command line. build_chain lays a cluster out as
// <workspace>/127.0.0.1/node<i>/, and the node runs with that directory in its argv.
var nodeName = regexp.MustCompile(`/(node\d+)/`)

// DiscoverNodes finds the node processes belonging to one cluster workspace.
//
// Without this every persistent entry registers zero nodes, and statusOf reports Unknown forever:
// the cluster can never be seen as active, never be found stale, and never be reclaimed after a
// crash. The whole liveness and PID-reuse apparatus sits unused behind an empty slice. It went
// unnoticed while `gate run` was the only thing that built clusters, because it released its
// reservation on the way out regardless.
//
// Matching is by workspace path, which is what keeps one cluster's discovery from picking up
// another's -- two chains on one machine differ in their node directories, not in their binary.
//
// The process table is read with `ps -axww -o pid=,args=` and filtered here, not with
// `pgrep -af`: BSD pgrep's -a means "include ancestors" and prints bare PIDs, so on macOS the old
// form found nothing, ever, and every test of the liveness path passed vacuously there. -ax (not
// -e: on macOS -e prints the environment) selects every process; -ww stops the argv from being
// cut at 80 columns, which would hide the workspace path this filter matches on.
func DiscoverNodes(workspace string, p Prober) []Node {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,args=").Output()
	if err != nil {
		return nil
	}
	return parsePgrep(string(out), workspace+string(os.PathSeparator), os.Getpid(), p)
}

// LooksLikeCluster reports whether workspace holds the layout build_chain writes: the stop script
// cluster_down.sh will run. It is what lets `cluster down --workspace` act on a directory nobody
// registered, without turning an arbitrary path into something to run scripts under.
func LooksLikeCluster(workspace string) bool {
	_, err := os.Stat(workspace + string(os.PathSeparator) + "127.0.0.1" + string(os.PathSeparator) + "stop_all.sh")
	return err == nil
}

// parsePgrep is the pure half, so the parsing is testable without spawning a chain. needle is the
// workspace prefix a node's argv must contain; empty matches every line (the pre-filtered pgrep
// output the tests were written against).
func parsePgrep(out string, needle string, self int, p Prober) []Node {
	var nodes []Node
	seen := map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pidStr, cmd, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		cmd = strings.TrimSpace(cmd)
		if needle != "" && !strings.Contains(cmd, needle) {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		// Skip our own process: fbt's argv can name the workspace it just built, and registering
		// the host as one of the chain's nodes would make the entry look alive for as long as fbt
		// runs and stale the instant it exits.
		if err != nil || pid <= 0 || pid == self || seen[pid] {
			continue
		}
		// Only the node binary. A shell that happens to mention the workspace is not a node, and
		// counting one would make a fully dead cluster read as degraded and never be reclaimed.
		if !strings.Contains(cmd, "fisco-bcos") {
			continue
		}
		seen[pid] = true
		name := ""
		if m := nodeName.FindStringSubmatch(cmd); m != nil {
			name = m[1]
		}
		nodes = append(nodes, Node{Name: name, PID: pid, Start: p.Fingerprint(pid)})
	}
	return nodes
}
