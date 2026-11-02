package dnsipxlsx

import (
	"context"
	"github.com/yunpiao/adtr/internal/dnsipassets"
	"io"
)

// WriteRecords validates and selects all matching simulated rows before writing.
// Every data row and the sheet title explicitly identify simulation provenance.
// On any write error discard the destination; this function does not publish it.
func WriteRecords(ctx context.Context, w io.Writer, records []dnsipassets.Record, f dnsipassets.Filter) error {
	if ctx == nil || w == nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rows, err := dnsipassets.Select(records, f)
	if err != nil {
		return err
	}
	i := 0
	return Write(ctx, w, []string{"ID", "Domain", "DNS name", "IP", "Observation", "Source", "Contract version", "Simulation"}, func() ([]string, error) {
		if i == len(rows) {
			return nil, io.EOF
		}
		r := rows[i]
		i++
		return []string{r.ID, r.Domain, r.DNSName, r.IP, r.Observation, r.Source, r.Version, "true"}, nil
	})
}
