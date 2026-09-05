package install

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer; Windows uses release ZIPs")
	}
	for _, tc := range []struct {
		name, system, machine, platform, arch, version, failure string
	}{
		{name: "linux_amd64", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64"},
		{name: "linux_arm64", system: "Linux", machine: "aarch64", platform: "linux", arch: "arm64"},
		{name: "darwin_amd64", system: "Darwin", machine: "x86_64", platform: "darwin", arch: "amd64"},
		{name: "darwin_arm64_pinned", system: "Darwin", machine: "arm64", platform: "darwin", arch: "arm64", version: "v1.2.3"},
		{name: "corrupt", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64", failure: "checksum mismatch"},
		{name: "missing_checksum", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64", failure: "missing or invalid checksum"},
		{name: "no_release", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64", failure: "a published release is required"},
		{name: "unsupported", system: "Linux", machine: "i686", failure: "supported architectures"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "mock-bin")
			installDir := filepath.Join(dir, "install with spaces")
			for _, path := range []string{bin, installDir} {
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, content string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), mode); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(installDir, "mysq"), "previous installation", 0755)
			archive := fmt.Sprintf("mysq_%s_%s.tar.gz", tc.platform, tc.arch)
			var payload strings.Builder
			gz := gzip.NewWriter(&payload)
			tw := tar.NewWriter(gz)
			const binary = "#!/bin/sh\nprintf 'fixture mysq\\n'\n"
			if err := tw.WriteHeader(&tar.Header{Name: "mysq", Mode: 0755, Size: int64(len(binary))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte(binary)); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(dir, archive), payload.String(), 0600)
			checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(payload.String())), archive)
			if tc.name == "corrupt" {
				checksum = strings.Repeat("0", 64) + "  " + archive + "\n"
			} else if tc.name == "missing_checksum" {
				checksum = ""
			}
			write(filepath.Join(dir, "checksums.txt"), checksum, 0600)
			write(filepath.Join(bin, "uname"), "#!/bin/sh\ncase \"$1\" in -s) echo \"$TEST_SYSTEM\";; -m) echo \"$TEST_MACHINE\";; esac\n", 0755)
			write(filepath.Join(bin, "curl"), `#!/bin/sh
set -eu
[ "$TEST_FAILURE" != no_release ] || exit 22
url=
output=
while [ "$#" -gt 0 ]; do
    case "$1" in
        https://*) url=$1 ;;
        -o) shift; output=$1 ;;
    esac
    shift
done
printf '%s\n' "$url" >> "$TEST_FIXTURE/requests"
cp "$TEST_FIXTURE/${url##*/}" "$output"
`, 0755)
			cmd := exec.Command("sh", "../../install.sh")
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "INSTALL_DIR="+installDir,
				"VERSION="+tc.version, "TEST_SYSTEM="+tc.system, "TEST_MACHINE="+tc.machine,
				"TEST_FIXTURE="+dir, "TEST_FAILURE="+tc.name)
			output, err := cmd.CombinedOutput()
			installed, readErr := os.ReadFile(filepath.Join(installDir, "mysq"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.failure != "" {
				if err == nil || !strings.Contains(string(output), tc.failure) || string(installed) != "previous installation" {
					t.Fatalf("unsafe failure: %v\n%s", err, output)
				}
				return
			}
			if err != nil || string(installed) != binary {
				t.Fatalf("installation failed: %v\n%s", err, output)
			}
			run, err := exec.Command(filepath.Join(installDir, "mysq")).CombinedOutput()
			if err != nil || string(run) != "fixture mysq\n" {
				t.Fatalf("installed binary is not executable: %v %s", err, run)
			}
			requests, err := os.ReadFile(filepath.Join(dir, "requests"))
			if err != nil {
				t.Fatal(err)
			}
			base := "https://github.com/maheshrijal/mysq/releases/latest/download/"
			if tc.version != "" {
				base = "https://github.com/maheshrijal/mysq/releases/download/" + tc.version + "/"
			}
			if string(requests) != base+"checksums.txt\n"+base+archive+"\n" {
				t.Fatalf("wrong release artifacts: %s", requests)
			}
		})
	}
}
