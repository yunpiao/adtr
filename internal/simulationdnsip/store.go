package simulationdnsip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dnsipassets"
	"io"
	"regexp"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func digest(v any) string { b, _ := json.Marshal(v); return sum(b) }
func authorize(tx pgx.Tx, s Scope, write bool) error {
	if tx == nil || !identifier.MatchString(s.Tenant) || !identifier.MatchString(s.Domain) || s.ActorID < 1 || !s.Read || (write && !s.Manage) {
		return ErrDenied
	}
	return nil
}
func lock(ctx context.Context, tx pgx.Tx, s Scope) error {
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "simulation-dns-ip:"+digest([]string{s.Tenant, s.Domain}))
	return e
}
func strict(b []byte, limit int, v any) error {
	if len(b) > limit {
		return ErrLimit
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(&struct{}{}) != io.EOF {
		return ErrInvalid
	}
	return nil
}

// Include conservative JSONB separator spacing before any write; a readable row must fit too.
func bounded(v any, limit int) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, ErrInvalid
	}
	extra := bytes.Count(b, []byte(":")) + bytes.Count(b, []byte(","))
	if len(b)+extra > limit {
		return nil, ErrLimit
	}
	return b, nil
}

type storedObservation struct {
	Observation Observation          `json:"observation"`
	Records     []dnsipassets.Record `json:"records"`
}

func normalizeFixture(f Fixture) (storedObservation, error) {
	var out storedObservation
	if !identifier.MatchString(f.ID) || !identifier.MatchString(f.Source) || len(f.Records) > dnsipassets.MaxRecords {
		return out, ErrInvalid
	}
	// Validate/canonicalize DNS domain even for empty observations through the existing core.
	probe, e := dnsipassets.Normalize(dnsipassets.Record{ID: "probe", Domain: f.DNSDomain, DNSName: f.DNSDomain, IP: "192.0.2.1", Observation: f.ID, Source: f.Source, Version: dnsipassets.Version, Simulation: true})
	if e != nil {
		return out, ErrInvalid
	}
	rows, e := dnsipassets.Select(f.Records, dnsipassets.Filter{Domain: probe.Domain, Observation: f.ID})
	if e != nil || len(rows) != len(f.Records) {
		return out, ErrInvalid
	}
	for _, r := range rows {
		if r.Source != f.Source {
			return out, ErrInvalid
		}
	}
	out = storedObservation{Observation: Observation{ID: f.ID, DNSDomain: probe.Domain, Source: f.Source, RecordCount: len(rows), Complete: true, Contract: Contract, Simulation: true}, Records: rows}
	out.Observation.Hash = digest(out)
	if _, e = bounded(out, MaxObservationBytes); e != nil {
		return storedObservation{}, e
	}
	return out, nil
}

// SeedFixtureTx is for trusted isolated fixture producers, never a public HTTP ingestion endpoint.
func SeedFixtureTx(ctx context.Context, tx pgx.Tx, s Scope, f Fixture) (Observation, error) {
	if e := authorize(tx, s, true); e != nil {
		return Observation{}, e
	}
	v, e := normalizeFixture(f)
	if e != nil {
		return Observation{}, e
	}
	if e = lock(ctx, tx, s); e != nil {
		return Observation{}, e
	}
	old, e := observation(ctx, tx, s, f.ID)
	if e == nil {
		if old.Observation.Hash != v.Observation.Hash {
			return Observation{}, ErrConflict
		}
		return old.Observation, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return Observation{}, e
	}
	var n int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_dns_ip_observations WHERE tenant_id=$1 AND domain_id=$2`, s.Tenant, s.Domain).Scan(&n); e != nil {
		return Observation{}, e
	}
	if n >= MaxObservations {
		return Observation{}, ErrLimit
	}
	b, e := bounded(v, MaxObservationBytes)
	if e != nil {
		return Observation{}, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_dns_ip_observations VALUES($1,$2,$3,$4,$5)`, s.Tenant, s.Domain, f.ID, v.Observation.Hash, b)
	return v.Observation, e
}
func observation(ctx context.Context, tx pgx.Tx, s Scope, id string) (storedObservation, error) {
	var v storedObservation
	var b []byte
	var h string
	if !identifier.MatchString(id) {
		return v, ErrInvalid
	}
	e := tx.QueryRow(ctx, `SELECT hash,body FROM adtr.simulation_dns_ip_observations WHERE tenant_id=$1 AND domain_id=$2 AND observation_id=$3`, s.Tenant, s.Domain, id).Scan(&h, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if e = strict(b, MaxObservationBytes, &v); e != nil {
		return v, e
	}
	expected, e := normalizeFixture(Fixture{ID: v.Observation.ID, DNSDomain: v.Observation.DNSDomain, Source: v.Observation.Source, Records: v.Records})
	if e != nil || id != v.Observation.ID || h != v.Observation.Hash || digest(expected) != digest(v) {
		return storedObservation{}, ErrInvalid
	}
	return v, nil
}
func ObservationsTx(ctx context.Context, tx pgx.Tx, s Scope) ([]Observation, error) {
	out := []Observation{}
	if e := authorize(tx, s, false); e != nil {
		return out, e
	}
	rows, e := tx.Query(ctx, `SELECT observation_id FROM adtr.simulation_dns_ip_observations WHERE tenant_id=$1 AND domain_id=$2 ORDER BY observation_id LIMIT 9`, s.Tenant, s.Domain)
	if e != nil {
		return out, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(ids) > MaxObservations {
		return out, ErrLimit
	}
	for _, id := range ids {
		v, e := observation(ctx, tx, s, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v.Observation)
	}
	return out, nil
}
func ListTx(ctx context.Context, tx pgx.Tx, s Scope, q Query) (Page, error) {
	var out Page
	if e := authorize(tx, s, false); e != nil {
		return out, e
	}
	v, e := observation(ctx, tx, s, q.ObservationID)
	if e != nil {
		return out, e
	}
	rows, total, e := dnsipassets.List(v.Records, dnsipassets.Filter{Domain: v.Observation.DNSDomain, Observation: q.ObservationID, Keyword: q.Keyword, Family: q.Family}, q.Offset, q.Limit)
	if e != nil {
		return out, ErrInvalid
	}
	out = Page{Observation: v.Observation, Query: q, Rows: rows, Total: total, Exhausted: q.Offset >= total || len(rows) >= total-q.Offset}
	if _, e = bounded(out, MaxResponseBytes); e != nil {
		return Page{}, e
	}
	return out, nil
}
