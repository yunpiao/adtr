// adtr-simulate evaluates explicit synthetic inputs offline. It never connects
// to AD, a database, or the production API and is not an authorization gateway.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yunpiao/adtr/internal/assetassessment"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/dnsipassets"
	"github.com/yunpiao/adtr/internal/dnsipxlsx"
	"github.com/yunpiao/adtr/internal/groupgraph"
	"github.com/yunpiao/adtr/internal/riskscore"
	"github.com/yunpiao/adtr/internal/securityevents"
)

const maxInput = 4 << 20

var errInput = errors.New("invalid simulated input")

func decode(data []byte, dst any) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errInput
	}
	v := json.NewDecoder(bytes.NewReader(data))
	v.UseNumber()
	if err := uniqueJSON(v, 0); err != nil {
		return errInput
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return errInput
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errInput
	}
	return nil
}

// Reject ambiguous duplicate keys before decoding typed structs. Case folds
// match encoding/json's case-insensitive field lookup; depth is independently bounded.
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errInput
	}
	token, err := d.Token()
	if err != nil {
		return errInput
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return errInput
			}
			k, ok := key.(string)
			if !ok {
				return errInput
			}
			for _, c := range k {
				if c > 127 {
					return errInput
				}
			}
			k = strings.ToLower(k)
			if seen[k] {
				return errInput
			}
			seen[k] = true
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return errInput
		}
	case '[':
		for d.More() {
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return errInput
		}
	default:
		return errInput
	}
	return nil
}

type graphRequest struct {
	Snapshot groupgraph.Snapshot `json:"snapshot"`
	Query    groupgraph.Query    `json:"query"`
}
type rulesRequest struct {
	Scope  detectionrules.Scope   `json:"scope"`
	Rules  []detectionrules.Rule  `json:"rules"`
	Events []detectionrules.Event `json:"events"`
}
type dnsRequest struct {
	Records []dnsipassets.Record `json:"records"`
	Filter  dnsipassets.Filter   `json:"filter"`
}
type eventsRequest struct {
	Scope         securityevents.Scope      `json:"scope"`
	Provenance    securityevents.Provenance `json:"provenance"`
	Events        []securityevents.RawEvent `json:"events"`
	Filter        securityevents.Filter     `json:"filter"`
	WindowSeconds int                       `json:"windowSeconds"`
	Threshold     int                       `json:"threshold"`
}

func evaluate(ctx context.Context, command string, data []byte) (any, error) {
	switch command {
	case "assets":
		var in assetassessment.Input
		if err := decode(data, &in); err != nil {
			return nil, err
		}
		return assetassessment.Evaluate(in)
	case "groups":
		var in graphRequest
		if err := decode(data, &in); err != nil {
			return nil, err
		}
		g, err := groupgraph.Build(in.Snapshot)
		if err != nil {
			return nil, err
		}
		result, err := g.Query(in.Query)
		if err != nil {
			return nil, err
		}
		return struct {
			Result      groupgraph.Result      `json:"result"`
			Diagnostics groupgraph.Diagnostics `json:"diagnostics"`
		}{result, g.Diagnostics()}, nil
	case "risk":
		var in riskscore.Request
		if err := decode(data, &in); err != nil {
			return nil, err
		}
		return riskscore.Evaluate(in)
	case "rules":
		var in rulesRequest
		if err := decode(data, &in); err != nil {
			return nil, err
		}
		p, err := detectionrules.Compile(in.Scope, in.Rules)
		if err != nil {
			return nil, err
		}
		return p.Evaluate(ctx, in.Events)
	case "dns", "dns-xlsx":
		var in dnsRequest
		if err := decode(data, &in); err != nil {
			return nil, err
		}
		if command == "dns" {
			return dnsipassets.Select(in.Records, in.Filter)
		}
		var b bytes.Buffer
		if err := dnsipxlsx.WriteRecords(ctx, &b, in.Records, in.Filter); err != nil {
			return nil, err
		}
		return struct {
			ContentType string `json:"contentType"`
			Workbook    []byte `json:"workbookBase64"`
		}{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", b.Bytes()}, nil
	case "events":
		var in eventsRequest
		if err := decode(data, &in); err != nil {
			return nil, err
		}
		if in.WindowSeconds < 1 || in.WindowSeconds > 86400 {
			return nil, errInput
		}
		d, err := securityevents.Normalize(in.Scope, in.Provenance, in.Events)
		if err != nil {
			return nil, err
		}
		page, err := d.Query(in.Scope, in.Filter)
		if err != nil {
			return nil, err
		}
		matches, err := d.CorrelateFailures(in.Scope, time.Duration(in.WindowSeconds)*time.Second, in.Threshold)
		if err != nil {
			return nil, err
		}
		return struct {
			Page       securityevents.Page        `json:"page"`
			Detections []securityevents.Detection `json:"detections"`
		}{page, matches}, nil
	default:
		return nil, errInput
	}
}
func run(args []string, input io.Reader, output, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: adtr-simulate assets|groups|dns|dns-xlsx|risk|rules|events < synthetic.json")
		return 2
	}
	data, err := io.ReadAll(io.LimitReader(input, maxInput+1))
	if err != nil || len(data) > maxInput {
		fmt.Fprintln(stderr, "invalid simulated input")
		return 2
	}
	result, err := evaluate(context.Background(), args[0], data)
	if err != nil {
		fmt.Fprintln(stderr, "simulated evaluation rejected")
		return 2
	}
	envelope := struct {
		Simulation bool   `json:"simulation"`
		Contract   string `json:"contract"`
		Command    string `json:"command"`
		Result     any    `json:"result"`
	}{true, "adtr.offline-simulation.v1", args[0], result}
	// Marshal before writing so validation failures cannot emit partial results.
	encoded, err := json.Marshal(envelope)
	if err != nil {
		fmt.Fprintln(stderr, "simulation encoding failed")
		return 1
	}
	encoded = append(encoded, '\n')
	n, err := output.Write(encoded)
	if err != nil || n != len(encoded) {
		fmt.Fprintln(stderr, "simulation output failed")
		return 1
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
