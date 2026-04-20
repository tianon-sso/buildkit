//go:build !windows

package workers

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/pkg/errors"
)

const buildkitdNetworkProtocol = "unix"

func applyBuildkitdPlatformFlags(args []string) []string {
	return append(args, "--oci-worker=false")
}

func requireRoot() error {
	if os.Getuid() != 0 {
		return errors.Wrap(integration.ErrRequirements, "requires root")
	}
	return nil
}

func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true, // stretch sudo needs this for sigterm
	}
}

func getBuildkitdAddr(tmpdir string) string {
	return "unix://" + filepath.Join(tmpdir, "buildkitd.sock")
}

func getBuildkitdDebugAddr(tmpdir string) string {
	return "unix://" + filepath.Join(tmpdir, "buildkitd-debug.sock")
}

func getTraceSocketPath(tmpdir string) string {
	return filepath.Join(tmpdir, "otel-grpc.sock")
}

func getContainerdSock(tmpdir string) string {
	return filepath.Join(tmpdir, "containerd.sock")
}

func getContainerdDebugSock(tmpdir string) string {
	return filepath.Join(tmpdir, "debug.sock")
}

func mountInfo(tmpdir string) error {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return errors.Wrap(err, "failed to open mountinfo")
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if strings.Contains(s.Text(), tmpdir) {
			return errors.Errorf("leaked mountpoint for %s", tmpdir)
		}
	}
	return s.Err()
}

// moved here since os.Chown is not supported on Windows.
// see no-op counterpart in util_windows.go
func chown(name string, uid, gid int) error {
	return os.Chown(name, uid, gid)
}

func normalizeAddress(address string) string {
	// for parity with windows, no effect for unix
	return address
}

func applyDockerdPlatformFlags(flags []string, _ string) []string {
	// NOTE: upstream passes "--userland-proxy=false" here, but that triggers a
	// write to /proc/sys/net/ipv4/conf/docker0/route_localnet (for hairpin NAT)
	// which fails in restricted containers where /proc/sys is read-only.
	// Omitting it means Docker uses the userland proxy instead; docker-proxy is
	// never actually spawned since the tests don't publish container ports.
	//
	// We also pass --iptables=false so that parallel dockerd instances don't
	// conflict on shared iptables chain names (they share a network namespace).
	// This is safe: the tests don't publish container ports and don't require
	// outbound NAT. The bridge itself is given a unique name in Moby.New so
	// that each instance creates its own interface without conflicting.
	flags = append(flags, "--iptables=false")
	return flags
}

func getBuildkitdNetworkAddr(tmpdir string) string {
	return tmpdir
}
