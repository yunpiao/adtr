// adtr-querytemplates executes a bounded, offline synthetic scenario from stdin.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/yunpiao/adtr/internal/querytemplates"
)

type operation struct {
	Op      string                 `json:"op"`
	ID      *string                `json:"id"`
	TplName *string                `json:"tplName"`
	Content *querytemplates.Params `json:"content"`
	Search  *string                `json:"search"`
}
type scenario struct {
	Defaults   []defaultTemplate `json:"defaults"`
	Operations []operation       `json:"operations"`
}

// Defaults are explicit synthetic scenario inputs, never installed product
// templates. Like create.content, params must be supplied as an object.
type defaultTemplate struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name"`
	Params *querytemplates.Params `json:"params"`
}
type result struct {
	Simulation bool                   `json:"simulation"`
	Lists      []querytemplates.Lists `json:"lists"`
}

func run(in io.Reader, out io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(in, querytemplates.MaxJSONBytes+1))
	if err != nil {
		return fmt.Errorf("cannot read scenario")
	}
	var input scenario
	if err = querytemplates.Decode(data, &input); err != nil {
		return err
	}
	if len(input.Operations) == 0 || len(input.Operations) > 100 {
		return querytemplates.ErrInvalid
	}
	defaults := make([]querytemplates.Template, 0, len(input.Defaults))
	for _, template := range input.Defaults {
		if template.Params == nil {
			return querytemplates.ErrInvalid
		}
		defaults = append(defaults, querytemplates.Template{
			ID: template.ID, Name: template.Name, Params: *template.Params,
		})
	}
	store, err := querytemplates.New(defaults)
	if err != nil {
		return err
	}
	output := result{Simulation: true, Lists: []querytemplates.Lists{}}
	for _, op := range input.Operations {
		switch op.Op {
		case "create":
			if op.ID == nil || op.TplName == nil || op.Content == nil || op.Search != nil {
				return querytemplates.ErrInvalid
			}
			err = store.Create(querytemplates.Template{ID: *op.ID, Name: *op.TplName, Params: *op.Content})
		case "delete", "restore":
			if op.ID == nil || op.TplName != nil || op.Content != nil || op.Search != nil {
				return querytemplates.ErrInvalid
			}
			if op.Op == "delete" {
				err = store.Delete(*op.ID)
			} else {
				err = store.Restore(*op.ID)
			}
		case "list":
			if op.ID != nil || op.TplName != nil || op.Content != nil {
				return querytemplates.ErrInvalid
			}
			search := ""
			if op.Search != nil {
				search = *op.Search
			}
			var list querytemplates.Lists
			list, err = store.List(search)
			if err == nil {
				output.Lists = append(output.Lists, list)
			}
		default:
			return querytemplates.ErrInvalid
		}
		if err != nil {
			return err
		}
	}
	// No scenario results are emitted before every operation succeeds.
	var buffer bytes.Buffer
	if err = json.NewEncoder(&buffer).Encode(output); err != nil {
		return err
	}
	n, err := out.Write(buffer.Bytes())
	if err == nil && n != buffer.Len() {
		return io.ErrShortWrite
	}
	return err
}
func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "synthetic query-template scenario failed:", err)
		os.Exit(1)
	}
}
