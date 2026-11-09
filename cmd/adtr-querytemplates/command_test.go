package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/querytemplates"
)

const emptyScenario = `{"operations":[{"op":"list"}]}`
const emptyScenarioResult = "{\"simulation\":true,\"lists\":[{\"defaultTplList\":[],\"customizeTplList\":[]}]}\n"
const commandUsage = "usage: adtr-querytemplates < synthetic-scenario.json\n"

type privateWriter struct{ short bool }

func (w privateWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, errors.New("PRIVATE_WRITER_DETAILS")
}

func TestCommandWriteErrorIsFixed(t *testing.T) {
	for _, out := range []io.Writer{privateWriter{}, privateWriter{short: true}} {
		err := run(strings.NewReader(emptyScenario), out)
		if err == nil || err.Error() != "cannot write scenario result" {
			t.Fatalf("write error must be fixed, got %v", err)
		}
	}
}

// This helper executes the real main entrypoint in a separate process. The
// test-only switch is not part of the command's production environment surface.
func TestQueryTemplatesCommandHelper(t *testing.T) {
	if os.Getenv("ADTR_QUERYTEMPLATES_TEST_HELPER") != "1" {
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

func TestCommandProcessExitAndSeparatePipes(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, input, stdout, stderr string
		args                        []string
		code                        int
	}{
		{"valid", emptyScenario, emptyScenarioResult, "", nil, 0},
		{"unknown flag", emptyScenario, "", commandUsage, []string{"--PRIVATE_FLAG"}, 2},
		{"file argument", emptyScenario, "", commandUsage, []string{"PRIVATE_FILE_PATH"}, 2},
		{"help is not a supported mode", emptyScenario, "", commandUsage, []string{"--help"}, 2},
		{"empty argument", emptyScenario, "", commandUsage, []string{""}, 2},
		{"multiple arguments", "PRIVATE_STDIN", "", commandUsage, []string{"--input", "PRIVATE_FILE_PATH"}, 2},
		{"invalid JSON", `{"PRIVATE_INPUT_SENTINEL":true}`, "", "synthetic query-template scenario failed: invalid synthetic template: JSON structure\n", nil, 1},
		{"late operation failure", `{"operations":[{"op":"list"},{"op":"delete","id":"PRIVATE_MISSING_ID"}]}`, "", "synthetic query-template scenario failed: synthetic template not found\n", nil, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			args := append([]string{"-test.run=^TestQueryTemplatesCommandHelper$", "--"}, tt.args...)
			cmd := exec.CommandContext(ctx, executable, args...)
			cmd.Env = append(os.Environ(), "ADTR_QUERYTEMPLATES_TEST_HELPER=1")
			cmd.Stdin = strings.NewReader(tt.input)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatal("command did not terminate within test deadline")
			}
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != tt.code || stdout.String() != tt.stdout || stderr.String() != tt.stderr {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

type commandCountingReader struct{ bytes int }

func (r *commandCountingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	r.bytes += len(p)
	return len(p), nil
}

func TestCommandReadBudget(t *testing.T) {
	in := &commandCountingReader{}
	var out bytes.Buffer
	if err := run(in, &out); !errors.Is(err, querytemplates.ErrInvalid) || in.bytes != querytemplates.MaxJSONBytes+1 || out.Len() != 0 {
		t.Fatalf("err=%v read=%d output=%d", err, in.bytes, out.Len())
	}
}

type commandPanicReader struct{}

func (commandPanicReader) Read([]byte) (int, error) { panic("unexpected stdin read") }

type commandPanicWriter struct{}

func (commandPanicWriter) Write([]byte) (int, error) { panic("unexpected stdout write") }

func TestCommandArgumentsFailBeforeIO(t *testing.T) {
	for _, args := range [][]string{{"--PRIVATE_FLAG"}, {"PRIVATE_PATH"}, {""}, {"--"}, {"one", "two"}} {
		var diagnostic bytes.Buffer
		if code := execute(args, commandPanicReader{}, commandPanicWriter{}, &diagnostic); code != 2 || diagnostic.String() != commandUsage {
			t.Fatalf("code=%d diagnostic=%q", code, diagnostic.String())
		}
	}
}

type commandPrivateReader struct{}

func (commandPrivateReader) Read(p []byte) (int, error) {
	return copy(p, emptyScenario), errors.New("PRIVATE_READER_DETAILS")
}

func TestCommandIOErrorsAreSanitized(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if code := execute(nil, commandPrivateReader{}, &out, &diagnostic); code != 1 || out.Len() != 0 || diagnostic.String() != "synthetic query-template scenario failed: cannot read scenario\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diagnostic.String())
	}
	for _, writer := range []io.Writer{privateWriter{}, privateWriter{short: true}} {
		diagnostic.Reset()
		if code := execute(nil, strings.NewReader(emptyScenario), writer, &diagnostic); code != 1 || diagnostic.String() != "synthetic query-template scenario failed: cannot write scenario result\n" {
			t.Fatalf("code=%d stderr=%q", code, diagnostic.String())
		}
	}
}

type commandPartialWriter struct {
	bytes.Buffer
	calls int
}

func (w *commandPartialWriter) Write(p []byte) (int, error) {
	w.calls++
	n, _ := w.Buffer.Write(p[:len(p)/2])
	return n, errors.New("PRIVATE_PARTIAL_WRITE_DETAILS")
}

func TestCommandPartialWriteIsNotRetried(t *testing.T) {
	var out commandPartialWriter
	var diagnostic bytes.Buffer
	if code := execute(nil, strings.NewReader(emptyScenario), &out, &diagnostic); code != 1 || out.calls != 1 || out.String() != emptyScenarioResult[:len(emptyScenarioResult)/2] || diagnostic.String() != "synthetic query-template scenario failed: cannot write scenario result\n" {
		t.Fatalf("code=%d calls=%d stdout=%q stderr=%q", code, out.calls, out.String(), diagnostic.String())
	}
}

func TestCommandArgumentDoesNotWaitForStdinEOF(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestQueryTemplatesCommandHelper$", "--", "PRIVATE_PATH")
	cmd.Env = append(os.Environ(), "ADTR_QUERYTEMPLATES_TEST_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close() // Keep the input open until the child has terminated.
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatal("unsupported argument waited for stdin instead of rejecting it")
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || stdout.Len() != 0 || stderr.String() != commandUsage {
		t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}
