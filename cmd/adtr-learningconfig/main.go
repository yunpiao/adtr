// adtr-learningconfig validates offline synthetic snapshots from standard input.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/yunpiao/adtr/internal/learningconfig"
)

var (
	errUsage      = errors.New("usage: adtr-learningconfig canonicalize | digest | verify LOWERCASE_SHA256")
	errRead       = errors.New("cannot read synthetic configuration")
	errDigest     = errors.New("synthetic configuration digest mismatch")
	errWrite      = errors.New("cannot write synthetic configuration result")
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type digestResult struct {
	Format    string `json:"format"`
	Synthetic bool   `json:"synthetic"`
	SHA256    string `json:"sha256"`
}

func run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "canonicalize", "digest":
		if len(args) != 1 {
			return errUsage
		}
	case "verify":
		if len(args) != 2 || !digestPattern.MatchString(args[1]) {
			return errUsage
		}
	default:
		return errUsage
	}
	data, err := io.ReadAll(io.LimitReader(in, learningconfig.MaxJSONBytes+1))
	if err != nil {
		return errRead
	}
	config, err := learningconfig.Parse(data)
	if err != nil {
		return err
	}
	digest, err := config.Digest()
	if err != nil {
		return err
	}
	if args[0] == "verify" && digest != args[1] {
		return errDigest
	}
	var output []byte
	if args[0] == "canonicalize" {
		output, err = config.CanonicalJSON()
	} else {
		output, err = json.Marshal(digestResult{"synthetic-learning-config-digest-v1", true, digest})
	}
	if err != nil {
		return err
	}
	output = append(output, '\n')
	// No output is attempted before complete validation and any digest match.
	// A failing destination can still accept a prefix; this is not an atomic file writer.
	n, err := out.Write(output)
	if err != nil || n != len(output) {
		return errWrite
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
