// adtr-attackpaths queries one bounded synthetic graph from stdin, without I/O
// other than stdin, stdout and stderr. It does not load application configuration.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/yunpiao/adtr/internal/attackpaths"
)

var (
	errRead  = errors.New("cannot read request")
	errWrite = errors.New("cannot write result")
)

func run(in io.Reader, out io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(in, attackpaths.MaxJSONBytes+1))
	if err != nil {
		return errRead
	}
	result, err := attackpaths.EvaluateJSON(data)
	if err != nil {
		return err
	}
	// Finish all validation and encoding before writing any result bytes.
	data, err = json.Marshal(result)
	if err != nil {
		return errWrite
	}
	data = append(data, '\n')
	if n, err := out.Write(data); err != nil || n != len(data) {
		return errWrite
	}
	return nil
}

func execute(args []string, in io.Reader, out, diagnostic io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(diagnostic, "usage: adtr-attackpaths < synthetic-request.json")
		return 2
	}
	if err := run(in, out); err != nil {
		fmt.Fprintln(diagnostic, "synthetic relationship query failed:", err)
		return 1
	}
	return 0
}

func main() { os.Exit(execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
