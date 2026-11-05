package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/learningconfig"
)

const inputSnapshot = `{"format":"synthetic-learning-config-v1","synthetic":true,"ruleLabel":"rule-a","dataSourceLabel":"source-a","domainLabel":"domain-a","windowSeconds":3600,"allowedFields":["user.name","event.id"],"selectedFields":["user.name"]}`
const expectedSnapshot = `{"format":"synthetic-learning-config-v1","synthetic":true,"ruleLabel":"rule-a","dataSourceLabel":"source-a","domainLabel":"domain-a","windowSeconds":3600,"allowedFields":["event.id","user.name"],"selectedFields":["user.name"]}`

func expectedDigest() string {
	sum := sha256.Sum256([]byte(expectedSnapshot))
	return hex.EncodeToString(sum[:])
}

func digestJSON() string {
	return `{"format":"synthetic-learning-config-digest-v1","synthetic":true,"sha256":"` + expectedDigest() + `"}` + "\n"
}

func TestRunModesAndRoundTrip(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"canonicalize"}, expectedSnapshot + "\n"},
		{[]string{"digest"}, digestJSON()},
		{[]string{"verify", expectedDigest()}, digestJSON()},
	} {
		var out bytes.Buffer
		if err := run(test.args, strings.NewReader(inputSnapshot), &out); err != nil {
			t.Fatal(err)
		}
		if out.String() != test.want {
			t.Fatalf("output = %s, want %s", out.String(), test.want)
		}
	}
	var canonical, again bytes.Buffer
	if err := run([]string{"canonicalize"}, strings.NewReader(inputSnapshot), &canonical); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"canonicalize"}, &canonical, &again); err != nil {
		t.Fatal(err)
	}
	if again.String() != expectedSnapshot+"\n" {
		t.Fatal("command round trip changed value")
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("invalid arguments must not read stdin") }

func TestRunRejectsArgumentsBeforeRead(t *testing.T) {
	for _, args := range [][]string{
		nil, {}, {"unknown"}, {"canonicalize", "extra"}, {"digest", "extra"},
		{"verify"}, {"verify", expectedDigest(), "extra"}, {"verify", ""},
		{"verify", strings.ToUpper(expectedDigest())}, {"verify", strings.Repeat("a", 63)},
		{"verify", strings.Repeat("a", 65)}, {"verify", strings.Repeat("z", 64)},
		{"verify", " " + expectedDigest()}, {"verify", expectedDigest() + "\n"},
	} {
		var out bytes.Buffer
		if err := run(args, panicReader{}, &out); err != errUsage || out.Len() != 0 {
			t.Fatalf("args %q: error %v, output length %d", args, err, out.Len())
		}
	}
}

func TestRunInvalidInputAndDigestNeverWrite(t *testing.T) {
	for _, data := range []string{
		"", "null", inputSnapshot + "{}", strings.Replace(inputSnapshot, "true", "false", 1),
		strings.Replace(inputSnapshot, "3600", "1.0", 1),
		strings.Replace(inputSnapshot, "rule-a", "rule-\xff", 1),
		strings.Replace(inputSnapshot, "rule-a", `rule-\ud800`, 1),
		strings.TrimSuffix(inputSnapshot, "}") + `,"ruleLabel":"other"}`,
		inputSnapshot + strings.Repeat(" ", learningconfig.MaxJSONBytes),
	} {
		for _, args := range [][]string{{"canonicalize"}, {"digest"}, {"verify", expectedDigest()}} {
			var out bytes.Buffer
			if err := run(args, strings.NewReader(data), &out); !errors.Is(err, learningconfig.ErrInvalid) || out.Len() != 0 {
				t.Fatalf("invalid input: error %v, output length %d", err, out.Len())
			}
		}
	}
	for _, data := range []string{
		inputSnapshot, strings.Replace(inputSnapshot, "rule-a", "other-rule", 1),
		strings.Replace(inputSnapshot, "event.id", "other.field", 1),
	} {
		var out bytes.Buffer
		if err := run([]string{"verify", strings.Repeat("0", 64)}, strings.NewReader(data), &out); err != errDigest || out.Len() != 0 {
			t.Fatalf("digest mismatch: error %v, output length %d", err, out.Len())
		}
	}
}

type countingReader struct{ read int }

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	r.read += len(p)
	return len(p), nil
}

type readFailure struct{}

func (readFailure) Read(p []byte) (int, error) {
	n := copy(p, inputSnapshot)
	return n, errors.New("PRIVATE_READ_SENTINEL")
}

type writeFailure struct{ short bool }

func (w writeFailure) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, errors.New("PRIVATE_WRITE_SENTINEL")
}

func TestRunBoundedAndSanitizedIO(t *testing.T) {
	reader := &countingReader{}
	var out bytes.Buffer
	if err := run([]string{"canonicalize"}, reader, &out); !errors.Is(err, learningconfig.ErrInvalid) {
		t.Fatal(err)
	}
	if reader.read != learningconfig.MaxJSONBytes+1 || out.Len() != 0 {
		t.Fatalf("read = %d, output length = %d", reader.read, out.Len())
	}
	if err := run([]string{"canonicalize"}, readFailure{}, &out); err != errRead || out.Len() != 0 {
		t.Fatalf("read failure = %v, output length = %d", err, out.Len())
	}
	for _, writer := range []io.Writer{writeFailure{}, writeFailure{short: true}} {
		if err := run([]string{"canonicalize"}, strings.NewReader(inputSnapshot), writer); err != errWrite {
			t.Fatalf("write failure = %v", err)
		}
	}
	exact := inputSnapshot + strings.Repeat(" ", learningconfig.MaxJSONBytes-len(inputSnapshot))
	if err := run([]string{"canonicalize"}, strings.NewReader(exact), &out); err != nil || out.String() != expectedSnapshot+"\n" {
		t.Fatalf("exact bound: %v, %q", err, out.String())
	}
}

// The child calls the actual main with isolated pipes and exits before the test
// harness can print PASS. No network, database, credentials or app runtime runs.
func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("ADTR_LEARNINGCONFIG_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(99)
}

func TestCLIProcessExitAndPipes(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, input, stdout, stderr string
		args                        []string
		exit                        int
	}{
		{"canonicalize", inputSnapshot, expectedSnapshot + "\n", "", []string{"canonicalize"}, 0},
		{"digest", inputSnapshot, digestJSON(), "", []string{"digest"}, 0},
		{"verify", inputSnapshot, digestJSON(), "", []string{"verify", expectedDigest()}, 0},
		{"mismatch", inputSnapshot, "", errDigest.Error() + "\n", []string{"verify", strings.Repeat("0", 64)}, 1},
		{"invalid", `{"PRIVATE_INPUT_SENTINEL":true}`, "", "invalid synthetic learning configuration: JSON snapshot\n", []string{"canonicalize"}, 1},
		{"usage", inputSnapshot, "", errUsage.Error() + "\n", []string{"PRIVATE_ARG_SENTINEL"}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"-test.run=^TestCLIHelperProcess$", "--"}, test.args...)
			command := exec.Command(executable, args...)
			command.Env = append(os.Environ(), "ADTR_LEARNINGCONFIG_TEST_HELPER=1")
			command.Stdin = strings.NewReader(test.input)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			if test.exit == 0 && err != nil {
				t.Fatal(err)
			}
			if test.exit != 0 {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != test.exit {
					t.Fatalf("exit = %v", err)
				}
			}
			if stdout.String() != test.stdout || stderr.String() != test.stderr {
				t.Fatalf("stdout %q, stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}
