package dbadmissioncheck

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// The certificate is a separately reviewed, complete selected source set. The
// smaller wiring manifest remains a tripwire, not a substitute for this check.
func sourceCertificate(data []byte) (map[string]string, error) {
	out := map[string]string{}
	for number, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid certificate line %d", number+1)
		}
		hash, name := fields[0], fields[1]
		bytes, err := hex.DecodeString(hash)
		if err != nil || len(bytes) != sha256.Size || strings.ToLower(hash) != hash {
			return nil, fmt.Errorf("invalid certificate hash at line %d", number+1)
		}
		if path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil, fmt.Errorf("invalid production source path at line %d", number+1)
		}
		if _, duplicate := out[name]; duplicate {
			return nil, fmt.Errorf("duplicate production source %s", name)
		}
		out[name] = hash
	}
	return out, nil
}
func verifySourceCertificate(expected, actual map[string]string) error {
	for name, hash := range actual {
		if expected[name] != hash {
			return fmt.Errorf("UNPROVEN added or changed compiled source %s", name)
		}
	}
	for name := range expected {
		if _, ok := actual[name]; !ok {
			return fmt.Errorf("UNPROVEN removed or unselected compiled source %s", name)
		}
	}
	return nil
}
func checkSourceCertificate(t *testing.T, tags string, actual map[string]string) {
	t.Helper()
	name := "production_default_linux.sha256"
	if tags == "adtr_ci_dbadmission" {
		name = "production_ci_linux.sha256"
	} else if tags != "" {
		t.Fatal("unreviewed source certificate build tags")
	}
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := sourceCertificate(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySourceCertificate(expected, actual); err != nil {
		t.Fatal(err)
	}
	t.Logf("reviewed complete selected repository source set: %d files", len(actual))
}
func checkSourceEnvironment(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("go", "env", "-json", "GOOS", "GOARCH", "GOVERSION", "GOFLAGS", "GOEXPERIMENT", "GOWORK", "CGO_ENABLED")
	cmd.Dir = root
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("source certificate environment: %v", err)
	}
	var env map[string]string
	if err = json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if !reviewedSourceEnvironment(env) {
		t.Fatal("UNPROVEN source certificate platform, toolchain, flags, experiment or workspace")
	}
	t.Logf("source audit environment linux/amd64 go1.27.1; CGO_ENABLED=%s (repository/external foreign code separately rejected)", env["CGO_ENABLED"])
}
func TestSourceCertificateRejectsIncompleteOrChangedSets(t *testing.T) {
	h := strings.Repeat("a", 64)
	valid := h + "  internal/example/example.go\n"
	for _, data := range []string{valid + valid, strings.ReplaceAll(valid, h, "bad"), h + " ../escape.go", h + " internal/example/example_test.go", h + " /absolute.go", h + " internal/../example.go", ""} {
		if _, err := sourceCertificate([]byte(data)); err == nil {
			t.Fatal("invalid certificate accepted")
		}
	}
	expected, err := sourceCertificate([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	for _, actual := range []map[string]string{{}, {"internal/example/example.go": strings.Repeat("b", 64)}, {"internal/example/example.go": h, "internal/example/added.go": h}} {
		if verifySourceCertificate(expected, actual) == nil {
			t.Fatal("changed selected source set accepted")
		}
	}
	if err := verifySourceCertificate(expected, map[string]string{"internal/example/example.go": h}); err != nil {
		t.Fatal(err)
	}
}

func reviewedSourceEnvironment(env map[string]string) bool {
	return !(env["GOOS"] != "linux" || env["GOARCH"] != "amd64" || env["GOVERSION"] != "go1.27.1" || env["GOFLAGS"] != "" || env["GOEXPERIMENT"] != "" || (env["GOWORK"] != "" && env["GOWORK"] != "off") || (env["CGO_ENABLED"] != "0" && env["CGO_ENABLED"] != "1"))
}

func TestSourceCertificateEnvironmentIsExact(t *testing.T) {
	base := map[string]string{"GOOS": "linux", "GOARCH": "amd64", "GOVERSION": "go1.27.1", "CGO_ENABLED": "1"}
	if !reviewedSourceEnvironment(base) {
		t.Fatal("declared audit environment rejected")
	}
	for key, value := range map[string]string{"GOOS": "darwin", "GOARCH": "arm64", "GOVERSION": "go1.27.2", "GOFLAGS": "-overlay=other.json", "GOEXPERIMENT": "experiment", "GOWORK": "/other/go.work", "CGO_ENABLED": "unknown"} {
		changed := map[string]string{}
		for k, v := range base {
			changed[k] = v
		}
		changed[key] = value
		if reviewedSourceEnvironment(changed) {
			t.Fatalf("unreviewed environment %s accepted", key)
		}
	}
}
