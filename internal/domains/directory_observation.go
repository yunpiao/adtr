package domains

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
)

const directoryObservationMaxBytes = 16 << 20

// Revalidate stored DTOs using the same dictionary as wire entries. Bound every
// string before copying it; canonical ordering and absent-vs-zero values survive.
func validDirectoryObject(o directoryassets.Object) bool {
	if len(o.GUID) != 36 || o.GUID[8] != '-' || o.GUID[13] != '-' || o.GUID[18] != '-' || o.GUID[23] != '-' || len(o.Classes) < 1 || len(o.Classes) > 32 || len(o.DN) > 4096 {
		return false
	}
	b, err := hex.DecodeString(strings.ReplaceAll(o.GUID, "-", ""))
	if err != nil || len(b) != 16 {
		return false
	}
	binary.LittleEndian.PutUint32(b[:4], binary.BigEndian.Uint32(b[:4]))
	binary.LittleEndian.PutUint16(b[4:6], binary.BigEndian.Uint16(b[4:6]))
	binary.LittleEndian.PutUint16(b[6:8], binary.BigEndian.Uint16(b[6:8]))
	classes := make([][]byte, 0, len(o.Classes))
	for _, c := range o.Classes {
		if len(c) > 64 {
			return false
		}
		classes = append(classes, []byte(c))
	}
	e := directoryassets.Entry{DN: o.DN, Attributes: []directoryassets.Attribute{{Name: "objectGUID", Values: [][]byte{b}}, {Name: "objectClass", Values: classes}}}
	if o.SAMAccountName != nil {
		if len(*o.SAMAccountName) > 1024 {
			return false
		}
		e.Attributes = append(e.Attributes, directoryassets.Attribute{Name: "sAMAccountName", Values: [][]byte{[]byte(*o.SAMAccountName)}})
	}
	if o.UserAccountControl != nil {
		e.Attributes = append(e.Attributes, directoryassets.Attribute{Name: "userAccountControl", Values: [][]byte{[]byte(strconv.FormatUint(uint64(*o.UserAccountControl), 10))}})
	}
	decoded, err := directoryassets.Decode(e)
	return err == nil && reflect.DeepEqual(decoded, o)
}

func encodeDirectoryObservation(o ldapconnection.DirectoryObservation) ([]byte, error) {
	bad := func() ([]byte, error) { return nil, problem(422, "invalid_directory_observation") }
	// RootDSE dnsHostName is case-insensitive and may include the DNS root dot.
	// Canonicalize at the storage boundary as well as in the real executor.
	host, hostErr := CanonicalDNS(o.Source.DCHostName)
	if hostErr != nil {
		return bad()
	}
	o.Source.DCHostName = host
	s := o.Source
	if o.Objects == nil || len(o.Objects) > 10000 || s.Pages < 1 || s.Pages > 100 || s.StartedAt.IsZero() || s.CompletedAt.Before(s.StartedAt) || s.CompletedAt.Sub(s.StartedAt) > 125*time.Second || s.ElapsedMilliseconds < 0 || s.ElapsedMilliseconds > 125000 || len(s.NamingContext) > 4096 {
		return bad()
	}
	for _, name := range []string{s.ServerName, s.DCHostName, s.Domain} {
		canonical, err := CanonicalDNS(name)
		if err != nil || canonical != name {
			return bad()
		}
	}
	if !ldapconnection.DirectoryNamingContextMatchesDomain(s.NamingContext, s.Domain) {
		return bad()
	}
	source, err := json.Marshal(s)
	if err != nil {
		return bad()
	}
	used := len(source) + 32
	seen := make(map[string]bool, len(o.Objects))
	for _, object := range o.Objects {
		if !validDirectoryObject(object) || !ldapconnection.DirectoryDNInDomain(object.DN, s.Domain) || seen[object.GUID] {
			return bad()
		}
		seen[object.GUID] = true
		raw, err := json.Marshal(object)
		if err != nil || len(raw)+1 > directoryObservationMaxBytes-used {
			return bad()
		}
		used += len(raw) + 1
	}
	raw, err := json.Marshal(o)
	if err != nil || len(raw) > directoryObservationMaxBytes {
		return bad()
	}
	return raw, nil
}

// Publication stores the complete bounded observation in the caller's fenced
// transaction. Generic task results stay empty; readers must require succeeded.
func publishDirectoryObservationTx(ctx context.Context, tx pgx.Tx, t tasks.Task, p directoryPinnedPayload, observation ldapconnection.DirectoryObservation) error {
	pins, err := directoryUseTaskPayload(t)
	if err != nil || pins != p {
		return errDirectoryUseEvidence
	}
	raw, err := encodeDirectoryObservation(observation)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	_, err = tx.Exec(ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, t.TenantID, t.DomainID, t.ID, t.ActorID, t.LeaseOwner, t.FencingToken, t.Attempt, len(observation.Objects), raw, hex.EncodeToString(digest[:]))
	if err != nil {
		return err
	}
	revision, _ := Revision(p.ConnectionRevision)
	generation, _ := Revision(p.ConnectionCredentialGeneration)
	return audit(ctx, tx, tasks.Principal{TenantID: t.TenantID, ActorID: t.ActorID}, t.DomainID, "domain_directory_result", revision, revision, generation, t.ID, "success")
}

// Persisted bytes are accepted only in the canonical bounded form emitted by
// publication. A matching digest alone is not enough to accept malformed data.
func decodeDirectoryObservation(raw []byte, digest string, count int) (ldapconnection.DirectoryObservation, error) {
	bad := func() (ldapconnection.DirectoryObservation, error) {
		return ldapconnection.DirectoryObservation{}, problem(409, "directory_observation_unavailable")
	}
	if len(raw) == 0 || len(raw) > directoryObservationMaxBytes || count < 0 || count > 10000 || len(digest) != 64 {
		return bad()
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != digest {
		return bad()
	}
	var envelope struct {
		Objects json.RawMessage `json:"objects"`
		Source  json.RawMessage `json:"source"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Source) > 8192 {
		return bad()
	}
	var observation ldapconnection.DirectoryObservation
	if json.Unmarshal(envelope.Source, &observation.Source) != nil {
		return bad()
	}
	reader := json.NewDecoder(bytes.NewReader(envelope.Objects))
	token, err := reader.Token()
	if err != nil || token != json.Delim('[') {
		return bad()
	}
	observation.Objects = make([]directoryassets.Object, 0, count)
	for reader.More() {
		if len(observation.Objects) >= count {
			return bad()
		}
		var piece json.RawMessage
		if reader.Decode(&piece) != nil || len(piece) > 32768 {
			return bad()
		}
		var object directoryassets.Object
		if json.Unmarshal(piece, &object) != nil || !validDirectoryObject(object) {
			return bad()
		}
		observation.Objects = append(observation.Objects, object)
	}
	if token, err = reader.Token(); err != nil || token != json.Delim(']') || len(observation.Objects) != count {
		return bad()
	}
	canonical, err := encodeDirectoryObservation(observation)
	if err != nil || !bytes.Equal(canonical, raw) {
		return bad()
	}
	return observation, nil
}
