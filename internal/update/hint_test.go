package update

import "testing"

func TestUpgradeHint(t *testing.T) {
	has := func(paths ...string) func(string) bool {
		return func(p string) bool {
			for _, q := range paths {
				if p == q {
					return true
				}
			}
			return false
		}
	}
	none := has()
	const home = "/home/ana"
	installer := "curl -fsSL https://packages.chrom.ar/collector/install.sh | sh"
	tests := []struct {
		name, exe, goos, gopath string
		has                     func(string) bool
		want                    string
	}{
		{"homebrew cask", "/opt/homebrew/Caskroom/collector/0.1.1/collector", "darwin", "", none, "brew upgrade collector"},
		{"homebrew on intel", "/usr/local/Caskroom/collector/0.1.1/collector", "darwin", "", none, "brew upgrade collector"},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/Caskroom/collector/0.1.1/collector", "linux", "", none, "brew upgrade collector"},
		{"apt", "/usr/bin/collector", "linux", "", has("/usr/bin/apt-get"), "sudo apt update && sudo apt upgrade collector"},
		{"dnf", "/usr/bin/collector", "linux", "", has("/usr/bin/dnf"), "sudo dnf upgrade collector"},
		{"zypper", "/usr/bin/collector", "linux", "", has("/usr/bin/zypper"), "sudo zypper update collector"},
		{"apk or unknown", "/usr/bin/collector", "linux", "", none, "download it from https://github.com/chrom-ar/collector-cli/releases"},
		{"go install default", home + "/go/bin/collector", "linux", "", none, "go install github.com/chrom-ar/collector-cli/cmd/collector@latest"},
		{"go install GOPATH", "/opt/go/bin/collector", "linux", "/opt/go", none, "go install github.com/chrom-ar/collector-cli/cmd/collector@latest"},
		{"installer, system", "/usr/local/bin/collector", "darwin", "", none, installer},
		{"installer, user", home + "/.local/bin/collector", "linux", "", none, installer},
		{"installer, elsewhere", "/opt/tools/collector", "linux", "", none, "curl -fsSL https://packages.chrom.ar/collector/install.sh | COLLECTOR_INSTALL_DIR='/opt/tools' sh"},
		{"windows", `C:\tools\collector.exe`, "windows", "", none, "download it from https://github.com/chrom-ar/collector-cli/releases"},
		{"unknown path", "", "linux", "", none, "download it from https://github.com/chrom-ar/collector-cli/releases"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UpgradeHint(Install{Exe: tt.exe, GOOS: tt.goos, Home: home, GOPATH: tt.gopath, Has: tt.has})
			if got != tt.want {
				t.Errorf("UpgradeHint(%q) = %q, want %q", tt.exe, got, tt.want)
			}
		})
	}
}
