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
		{name: "fresh_install", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64"},
		{name: "linux_amd64", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64"},
		{name: "linux_arm64", system: "Linux", machine: "aarch64", platform: "linux", arch: "arm64"},
		{name: "darwin_amd64", system: "Darwin", machine: "x86_64", platform: "darwin", arch: "amd64"},
		{name: "darwin_arm64_pinned", system: "Darwin", machine: "arm64", platform: "darwin", arch: "arm64", version: "v1.2.3"},
		{name: "unknown_previous_version", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64"},
		{name: "invalid_binary_version", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64", failure: "cannot read version from downloaded mysq"},
		{name: "no_modify_path", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64"},
		{name: "corrupt", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64", failure: "checksum mismatch"},
		{name: "missing_checksum", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64", failure: "missing or invalid checksum"},
		{name: "no_release", system: "Linux", machine: "x86_64", platform: "linux", arch: "amd64", failure: "a published release is required"},
		{name: "unsupported", system: "Linux", machine: "i686", failure: "supported architectures"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "mock-bin")
			installDir := filepath.Join(dir, "install 'quoted' \\ $cash $(printf BAD) `printf BAD`")
			home := filepath.Join(dir, "home")
			zdot := filepath.Join(home, "zsh config")
			xdg := filepath.Join(home, "xdg config")
			for _, path := range []string{bin, installDir, home} {
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
			const originalProfile = "# existing user configuration without a final newline"
			write(filepath.Join(home, ".profile"), originalProfile, 0600)
			if tc.name == "linux_amd64" {
				write(filepath.Join(home, ".bash_profile"), originalProfile, 0600)
				write(filepath.Join(home, ".bash_login"), originalProfile, 0600)
			} else if tc.name == "linux_arm64" {
				write(filepath.Join(home, ".bash_login"), originalProfile, 0600)
			}
			previous := "#!/bin/sh\nprintf 'mysq version 1.2.2\\n'\n"
			if tc.name == "unknown_previous_version" {
				previous = "#!/bin/sh\nexit 1\n"
			}
			if tc.name != "fresh_install" {
				write(filepath.Join(installDir, "mysq"), previous, 0755)
			}
			archive := fmt.Sprintf("mysq_%s_%s.tar.gz", tc.platform, tc.arch)
			binary := "#!/bin/sh\nprintf 'mysq version 1.2.3\\n'\n"
			if tc.name == "invalid_binary_version" {
				binary = "#!/bin/sh\nprintf 'unexpected output\\n'\n"
			}
			checksum := releaseFixture(t, dir, archive, binary)
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
			noModify := "0"
			if tc.name == "no_modify_path" {
				noModify = "1"
			}
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "INSTALL_DIR="+installDir,
				"VERSION="+tc.version, "TEST_SYSTEM="+tc.system, "TEST_MACHINE="+tc.machine,
				"TEST_FIXTURE="+dir, "TEST_FAILURE="+tc.name, "HOME="+home, "ZDOTDIR="+zdot,
				"XDG_CONFIG_HOME="+xdg, "SHELL=/bin/fish", "MYSQ_NO_MODIFY_PATH="+noModify)
			output, err := cmd.CombinedOutput()
			installed, readErr := os.ReadFile(filepath.Join(installDir, "mysq"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.failure != "" {
				if err == nil || !strings.Contains(string(output), tc.failure) || string(installed) != previous {
					t.Fatalf("unsafe failure: %v\n%s", err, output)
				}
				assertProfileUnchanged(t, home, originalProfile)
				return
			}
			if err != nil || string(installed) != binary {
				t.Fatalf("installation failed: %v\n%s", err, output)
			}
			wantResult := "Updated mysq v1.2.2 → v1.2.3 (" + installDir + "/mysq)"
			if tc.name == "fresh_install" {
				wantResult = "Installed mysq v1.2.3 (" + installDir + "/mysq)"
			}
			if tc.name == "unknown_previous_version" {
				wantResult = "Updated mysq unknown → v1.2.3 (" + installDir + "/mysq)"
			}
			if !strings.Contains(string(output), wantResult) {
				t.Fatalf("missing version transition: %s", output)
			}
			run, err := exec.Command(filepath.Join(installDir, "mysq")).CombinedOutput()
			if err != nil || string(run) != "mysq version 1.2.3\n" {
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
			if noModify == "1" {
				assertProfileUnchanged(t, home, originalProfile)
				if _, err := os.Stat(filepath.Join(home, ".bashrc")); !os.IsNotExist(err) {
					t.Fatal("opt-out created shell configuration")
				}
				if !strings.Contains(string(output), "For this terminal, run: fish_add_path --path") {
					t.Fatal("missing current-shell instructions")
				}
				return
			}
			configs := []string{filepath.Join(home, ".profile"), filepath.Join(home, ".bashrc"), filepath.Join(zdot, ".zshrc"), filepath.Join(xdg, "fish/conf.d/mysq.fish")}
			if tc.name == "linux_amd64" {
				configs = append(configs, filepath.Join(home, ".bash_profile"))
				untouched, _ := os.ReadFile(filepath.Join(home, ".bash_login"))
				if string(untouched) != originalProfile {
					t.Fatal("modified an inactive Bash login file")
				}
			} else if tc.name == "linux_arm64" {
				configs = append(configs, filepath.Join(home, ".bash_login"))
			} else if _, err := os.Stat(filepath.Join(home, ".bash_profile")); !os.IsNotExist(err) {
				t.Fatal("created a Bash profile that shadows .profile")
			}
			before := make(map[string]string)
			for _, path := range configs {
				data, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(data), "# mysq PATH") {
					t.Fatalf("missing shell setup in %s: %v", path, err)
				}
				before[path] = string(data)
			}
			if !strings.HasPrefix(before[filepath.Join(home, ".profile")], originalProfile+"\n") {
				t.Fatal("existing profile content was not preserved")
			}
			// Advance the mocked latest release. A pinned tag must keep its payload.
			upgraded := binary
			if tc.version == "" {
				upgraded = strings.ReplaceAll(binary, "1.2.3", "1.2.4")
				write(filepath.Join(dir, "checksums.txt"), releaseFixture(t, dir, archive, upgraded), 0600)
			}
			reinstall := exec.Command("sh", "../../install.sh")
			reinstall.Env = cmd.Env
			output, err = reinstall.CombinedOutput()
			if err != nil {
				t.Fatalf("reinstall failed: %v %s", err, output)
			}
			wantTransition := "Updated mysq v1.2.3 → v1.2.4"
			if tc.version != "" {
				wantTransition = "Updated mysq v1.2.3 → v1.2.3"
			}
			if !strings.Contains(string(output), wantTransition) {
				t.Fatalf("wrong upgrade versions: %s", output)
			}
			installed, err = os.ReadFile(filepath.Join(installDir, "mysq"))
			if err != nil || string(installed) != upgraded {
				t.Fatalf("upgrade did not replace existing binary: %v", err)
			}
			for path, want := range before {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != want {
					t.Fatalf("reinstall changed shell configuration: %s", path)
				}
			}
			verifyShellStartup(t, cmd.Env, home, installDir)
		})
	}
}

func assertProfileUnchanged(t *testing.T, home, original string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".profile"))
	if err != nil || string(data) != original {
		t.Fatal("failed or opted-out installation changed shell profile")
	}
}

func verifyShellStartup(t *testing.T, env []string, home, installDir string) {
	t.Helper()
	for _, tc := range []struct {
		shell string
		args  []string
	}{
		{"sh", []string{"-c", `. "$HOME/.profile"; . "$HOME/.profile"; command -v mysq; printf '%s\n' "$PATH"`}},
		{"bash", []string{"--noprofile", "--rcfile", filepath.Join(home, ".bashrc"), "-ic", `command -v mysq; printf '%s\n' "$PATH"`}},
		{"bash", []string{"-lc", `command -v mysq; printf '%s\n' "$PATH"`}},
		{"zsh", []string{"-ic", `command -v mysq; printf '%s\n' "$PATH"`}},
		{"fish", []string{"-c", `command -s mysq; string join : $PATH`}},
	} {
		if _, err := exec.LookPath(tc.shell); err != nil {
			t.Logf("%s unavailable; its generated configuration is still checked", tc.shell)
			continue
		}
		cmd := exec.Command(tc.shell, tc.args...)
		cmd.Env = env
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s startup failed: %v", tc.shell, err)
		}
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		if len(lines) != 2 || lines[0] != filepath.Join(installDir, "mysq") {
			t.Fatalf("%s did not discover installed binary: %s", tc.shell, output)
		}
		count := 0
		for _, part := range strings.Split(lines[1], ":") {
			if part == installDir {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s PATH contains install directory %d times", tc.shell, count)
		}
	}
}

func releaseFixture(t *testing.T, dir, archive, binary string) string {
	t.Helper()
	var payload strings.Builder
	gz := gzip.NewWriter(&payload)
	tw := tar.NewWriter(gz)
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
	if err := os.WriteFile(filepath.Join(dir, archive), []byte(payload.String()), 0600); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(payload.String())), archive)
}
