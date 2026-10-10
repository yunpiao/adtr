package domains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

func validUserAssetsV2Filter() UserAssetsV2Filter {
	return UserAssetsV2Filter{DomainID: "domain", ExpectedRevision: "1", ExpectedCredentialRevision: "2", PageIdx: 1, PageSize: 10}
}

func requireUserAssetsV2Error(t *testing.T, err error, status int, code string) {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) || failure.Status != status || failure.Code != code {
		t.Fatalf("expected bounded %d %s error, got %v", status, code, err)
	}
}

func TestUserAssetsV2FilterStrictSourcePageAndUTF16Bounds(t *testing.T) {
	base := validUserAssetsV2Filter()
	for name, change := range map[string]func(*UserAssetsV2Filter){
		"missing domain":               func(f *UserAssetsV2Filter) { f.DomainID = "" },
		"reserved domain":              func(f *UserAssetsV2Filter) { f.DomainID = "platform" },
		"invalid observation":          func(f *UserAssetsV2Filter) { f.ObservationID = "bad/id" },
		"missing revision":             func(f *UserAssetsV2Filter) { f.ExpectedRevision = "" },
		"zero revision":                func(f *UserAssetsV2Filter) { f.ExpectedRevision = "0" },
		"negative revision":            func(f *UserAssetsV2Filter) { f.ExpectedRevision = "-1" },
		"noncanonical revision":        func(f *UserAssetsV2Filter) { f.ExpectedRevision = "01" },
		"signed revision":              func(f *UserAssetsV2Filter) { f.ExpectedRevision = "+1" },
		"overflow revision":            func(f *UserAssetsV2Filter) { f.ExpectedRevision = "9223372036854775808" },
		"missing credential revision":  func(f *UserAssetsV2Filter) { f.ExpectedCredentialRevision = "" },
		"overflow credential revision": func(f *UserAssetsV2Filter) { f.ExpectedCredentialRevision = "9223372036854775808" },
		"page zero":                    func(f *UserAssetsV2Filter) { f.PageIdx = 0 },
		"page large":                   func(f *UserAssetsV2Filter) { f.PageIdx = 10001 },
		"unpinned continuation":        func(f *UserAssetsV2Filter) { f.PageIdx = 2 },
		"size unlimited":               func(f *UserAssetsV2Filter) { f.PageSize = -1 },
		"size zero":                    func(f *UserAssetsV2Filter) { f.PageSize = 0 },
		"legacy size":                  func(f *UserAssetsV2Filter) { f.PageSize = 25 },
		"large size":                   func(f *UserAssetsV2Filter) { f.PageSize = 100 },
		"utf8":                         func(f *UserAssetsV2Filter) { f.Search = string([]byte{0xff}) },
		"surrogate":                    func(f *UserAssetsV2Filter) { f.Search = string([]byte{0xed, 0xa0, 0x80}) },
		"ascii 51":                     func(f *UserAssetsV2Filter) { f.Search = strings.Repeat("x", 51) },
		"nonbmp 26":                    func(f *UserAssetsV2Filter) { f.Search = strings.Repeat("😀", 26) },
		"byte bound":                   func(f *UserAssetsV2Filter) { f.Search = strings.Repeat("界", 67) },
	} {
		t.Run(name, func(t *testing.T) {
			f := base
			change(&f)
			requireUserAssetsV2Error(t, ValidateUserAssetsV2Filter(f), 400, "invalid_input")
			// Validation must finish without touching SQL, even with no store.
			_, err := New(nil).UserAssetsV2ListTx(context.Background(), nil, "tenant", []string{"domain"}, f)
			requireUserAssetsV2Error(t, err, 400, "invalid_input")
		})
	}
	for _, search := range []string{"", " ", "\x00\r\n\u200b", strings.Repeat("x", 50), strings.Repeat("界", 50), strings.Repeat("😀", 25), strings.Repeat("e\u0301", 25)} {
		for _, size := range []int{10, 20, 30, 40, 50} {
			f := base
			f.Search, f.PageSize = search, size
			f.ExpectedRevision, f.ExpectedCredentialRevision = "9223372036854775807", "9223372036854775807"
			f.PageIdx, f.ObservationID = 10000, "snapshot"
			if err := ValidateUserAssetsV2Filter(f); err != nil {
				t.Fatal("valid exact boundary rejected", err)
			}
		}
	}
}

func TestUserAssetV2InputCanonicalGUIDAndRequiredPin(t *testing.T) {
	base := UserAssetV2Input{DomainID: "domain", ExpectedRevision: "1", ExpectedCredentialRevision: "2", ObservationID: "snapshot", ObjectGUID: "00000000-0000-0000-0000-000000000000"}
	for _, guid := range []string{base.ObjectGUID, "ffffffff-ffff-ffff-ffff-ffffffffffff", "00112233-4455-6677-8899-aabbccddeeff"} {
		in := base
		in.ObjectGUID = guid
		if err := ValidateUserAssetV2Input(in); err != nil {
			t.Fatal("codec-valid GUID rejected", err)
		}
	}
	for _, guid := range []string{"", "00112233-4455-6677-8899-AABBCCDDEEFF", "00112233445566778899aabbccddeeff", "00112233-4455-6677-8899-aabbccddeefg", "00112233_4455-6677-8899-aabbccddeeff"} {
		in := base
		in.ObjectGUID = guid
		requireUserAssetsV2Error(t, ValidateUserAssetV2Input(in), 400, "invalid_input")
	}
	base.ObservationID = ""
	_, err := New(nil).UserAssetV2DetailTx(context.Background(), nil, "tenant", []string{"domain"}, base)
	requireUserAssetsV2Error(t, err, 400, "invalid_input")
}

func TestUserAssetsV2LiteralRawIndependentFieldsAndCaseMapping(t *testing.T) {
	sam, sid, mail := "Alpha", "S-1-5-21", " Beta\x00😀 <script>%_*?'\"\\()[]+& .*$ (|(x=y)) SS é e\u0301 \u200b"
	o := directoryassets.PublicObjectV2{Object: directoryassets.Object{GUID: "00112233-4455-6677-8899-aabbccddeeff", DN: "CN=Person,OU=Dept,DC=example,DC=test", SAMAccountName: &sam}, ObjectSID: &sid, Mail: &mail, Description: []string{"description-only"}}
	for _, q := range []string{"", "ALPHA", "s-1-5-21", " beta", "\x00", "😀", "<SCRIPT>", "%", "_", "*", "?", "'", `"`, `\`, "(", ")", "[", "]", "+", "&", ".*$", "(|(x=y))", "é", "e\u0301", "\u200b", "ou=DEPT"} {
		if !userAssetsV2Match(o, strings.ToLower(q)) {
			t.Fatalf("literal raw search failed for %q", q)
		}
	}
	for _, q := range []string{o.GUID, "description-only", "AlphaS-1-5", `\u0000`, "a.*b", "BETA ", "ß", "person,ou=dept,dc=example,dc=testalpha"} {
		if userAssetsV2Match(o, strings.ToLower(q)) {
			t.Fatalf("search added wildcard, display, normalization or cross-field semantics for %q", q)
		}
	}
	for _, tc := range []struct {
		value, query string
		match        bool
	}{{"É", "é", true}, {"É", "e\u0301", false}, {"e\u0301", "é", false}, {"Σ", "σ", true}, {"Σ", "ς", false}, {"Straße", "STRASSE", false}} {
		o.Mail, o.SAMAccountName, o.ObjectSID, o.DN = &tc.value, nil, nil, ""
		if userAssetsV2Match(o, strings.ToLower(tc.query)) != tc.match {
			t.Fatal("case mapping differs from Go ToLower plus Contains")
		}
	}
}

func TestUserAssetsV2FilterBeforePaginationStableGUIDAndExactProjection(t *testing.T) {
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	base := o.Objects[0]
	o.Objects = nil
	for i := 61; i >= 1; i-- {
		stored := base
		stored.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
		name := fmt.Sprintf("User %02d", i)
		stored.Base.SAMAccountName = &name
		o.Objects = append(o.Objects, stored)
	}
	for i, kind := range []directoryassets.Kind{directoryassets.Group, directoryassets.Computer} {
		stored := base
		stored.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", 100+i)
		stored.Base.Kind = kind
		if kind == directoryassets.Group {
			stored.Base.Classes = []string{"group", "top"}
		} else {
			stored.Base.Classes = []string{"computer", "top", "user"}
		}
		o.Objects = append(o.Objects, stored)
	}
	selection := UserAssetsV2Selection{DomainID: "domain", Revision: "1", CredentialRevision: "2"}
	for _, size := range []int{10, 20, 30, 40, 50} {
		for _, index := range []int{1, 2, 7, 10000} {
			f := validUserAssetsV2Filter()
			f.PageSize, f.PageIdx = size, index
			out, err := userAssetsV2Page(context.Background(), selection, "snapshot", o, f)
			start := (index - 1) * size
			want := min(size, max(0, 61-start))
			if err != nil || out.DictionaryVersion != 2 || !out.Available || out.Selection != selection || out.ObservationID != "snapshot" || out.Page.Total != 61 || out.Page.Pages != (61+size-1)/size || len(out.List) != want || out.List == nil {
				t.Fatal("pagination/count/provenance changed", out.Page, err)
			}
			for i, row := range out.List {
				if row.GUID != fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", start+i+1) || row.Kind != directoryassets.User || *row.Mail != " Case\x00😀@example.test " || *row.WhenCreated != "0001-01-01T00:00:00Z" {
					t.Fatal("page changed ordered observed values")
				}
			}
		}
	}
	f := validUserAssetsV2Filter()
	f.Search = "uSeR 61"
	out, err := userAssetsV2Page(context.Background(), selection, "snapshot", o, f)
	if err != nil || out.Page.Total != 1 || out.Page.Pages != 1 || len(out.List) != 1 || *out.List[0].SAMAccountName != "User 61" {
		t.Fatal("filter ran after unfiltered first page", err)
	}
	f.Search = "absent"
	empty, err := userAssetsV2Page(context.Background(), selection, "snapshot", o, f)
	if err != nil || !empty.Available || empty.Page.Total != 0 || empty.Page.Pages != 0 || empty.List == nil || len(empty.List) != 0 {
		t.Fatal("empty match became unavailable", err)
	}
	o.Discard()
	if *out.List[0].Mail != " Case\x00😀@example.test " || out.Source.Domain != "example.test" {
		t.Fatal("public projection aliases owned observation")
	}
}

func TestUserAssetsV2NullZeroAndExactResponseKeys(t *testing.T) {
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	directoryassets.ClearStoredObjectsV2(o.Objects)
	zero := uint32(0)
	o.Objects = []directoryassets.StoredObjectV2{{Base: directoryassets.Object{GUID: "00000000-0000-0000-0000-000000000000", DN: "CN=Null,DC=example,DC=test", Kind: directoryassets.User, Classes: []string{"top", "user"}, UserAccountControl: &zero}}}
	out, err := userAssetsV2Page(context.Background(), UserAssetsV2Selection{"domain", "1", "2"}, "snapshot", o, validUserAssetsV2Filter())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || len(envelope) != 7 {
		t.Fatal("list response keys changed")
	}
	var list []map[string]json.RawMessage
	if json.Unmarshal(envelope["list"], &list) != nil || len(list) != 1 || len(list[0]) != 10 {
		t.Fatal("public keys changed")
	}
	for _, key := range []string{"samAccountName", "objectSid", "mail", "description", "whenCreated"} {
		if string(list[0][key]) != "null" {
			t.Fatal("missing became empty", key)
		}
	}
	if string(list[0]["userAccountControl"]) != "0" {
		t.Fatal("observed zero disappeared")
	}
	unavailable := UserAssetsV2List{DictionaryVersion: 2, Selection: out.Selection, List: []directoryassets.PublicObjectV2{}, Page: Page{Index: 1, Size: 10}}
	raw, _ = json.Marshal(unavailable)
	if bytes.Contains(raw, []byte("observationId")) || bytes.Contains(raw, []byte("source")) || !bytes.Contains(raw, []byte(`"list":[]`)) {
		t.Fatal("unavailable invented evidence")
	}
	detail := UserAssetV2Detail{DictionaryVersion: 2, Selection: out.Selection, ObservationID: "snapshot", Source: *out.Source, Object: out.List[0]}
	raw, _ = json.Marshal(detail)
	envelope = nil
	if json.Unmarshal(raw, &envelope) != nil || len(envelope) != 5 || envelope["object"] == nil || envelope["available"] != nil {
		t.Fatal("detail response keys changed")
	}
}

type userAssetsV2CancelContext struct {
	context.Context
	calls int
}

func (c *userAssetsV2CancelContext) Err() error {
	c.calls++
	if c.calls >= 3 {
		return context.Canceled
	}
	return nil
}

func TestUserAssetsV2CancellationScopeAndCompleteResponseCap(t *testing.T) {
	f := validUserAssetsV2Filter()
	f.ExpectedRevision = "9"
	_, err := New(nil).UserAssetsV2ListTx(context.Background(), nil, "tenant", nil, f)
	requireUserAssetsV2Error(t, err, 404, "not_found")
	_, err = New(nil).UserAssetV2DetailTx(context.Background(), nil, "tenant", nil, UserAssetV2Input{DomainID: f.DomainID, ExpectedRevision: "9", ExpectedCredentialRevision: "9", ObservationID: "missing", ObjectGUID: "00000000-0000-0000-0000-000000000000"})
	requireUserAssetsV2Error(t, err, 404, "not_found")
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	o.Objects = append(o.Objects, o.Objects[0], o.Objects[0])
	ctx := &userAssetsV2CancelContext{Context: context.Background()}
	out, err := userAssetsV2Page(ctx, UserAssetsV2Selection{}, "snapshot", o, validUserAssetsV2Filter())
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, UserAssetsV2List{}) {
		t.Fatal("cancelled scan returned partial page", err)
	}
	large := strings.Repeat("\x00", UserAssetsV2MaxBytes/6)
	for _, response := range []any{UserAssetsV2List{List: []directoryassets.PublicObjectV2{{Mail: &large}}}, UserAssetV2Detail{Object: directoryassets.PublicObjectV2{Mail: &large}}} {
		requireUserAssetsV2Error(t, validateUserAssetsV2Response(context.Background(), response), 422, "directory_limit_exceeded")
	}
	// Exactly account for the encoder newline: the enclosing JSON string costs
	// two bytes, so this is the largest allowed scalar envelope for this helper.
	if err := validateUserAssetsV2Response(context.Background(), strings.Repeat("a", UserAssetsV2MaxBytes-3)); err != nil {
		t.Fatal(err)
	}
	requireUserAssetsV2Error(t, validateUserAssetsV2Response(context.Background(), strings.Repeat("a", UserAssetsV2MaxBytes-2)), 422, "directory_limit_exceeded")
}

type userAssetsV2LoaderTx struct {
	pgx.Tx
	row  pgx.Row
	sql  string
	args []any
}

func (t *userAssetsV2LoaderTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	t.sql, t.args = sql, args
	return t.row
}

type userAssetsV2LoaderRow struct {
	body                   []byte
	domain, server, digest string
	count                  int
	err                    error
}

func (r userAssetsV2LoaderRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string), *dest[1].(*[]byte), *dest[2].(*string), *dest[3].(*int), *dest[4].(*string), *dest[5].(*string) = "snapshot", r.body, r.digest, r.count, r.domain, r.server
	return nil
}

func TestUserAssetsV2SharedLoaderPinsBodyOwnershipAndCompleteValidation(t *testing.T) {
	for _, mode := range []string{"ok", "source", "digest", "corrupt-unselected-user", "duplicate-guid", "no-row", "pinned-no-row", "dependency"} {
		t.Run(mode, func(t *testing.T) {
			o := sampleDirectoryV2Observation()
			defer o.Discard()
			raw, err := encodeDirectoryObservationV2(o)
			if err != nil {
				t.Fatal(err)
			}
			row := userAssetsV2LoaderRow{body: raw, domain: o.Source.Domain, server: o.Source.ServerName, count: len(o.Objects)}
			pin := "snapshot"
			switch mode {
			case "source":
				row.server = "other.example.test"
			case "corrupt-unselected-user":
				row.body = bytes.Replace(raw, []byte(`],"source":`), []byte(`,{}],"source":`), 1)
				row.count++
			case "duplicate-guid":
				piece, err := directoryassets.EncodeStoredObjectV2(o.Objects[0])
				if err != nil {
					t.Fatal(err)
				}
				defer clear(piece)
				row.body = bytes.Replace(raw, []byte(`],"source":`), append(append([]byte(","), piece...), []byte(`],"source":`)...), 1)
				row.count++
			case "no-row":
				row.err = pgx.ErrNoRows
				pin = ""
			case "pinned-no-row":
				row.err = pgx.ErrNoRows
			case "dependency":
				row.err = errors.New("synthetic dependency")
			}
			row.digest = directoryDigest(row.body)
			if mode == "digest" {
				row.digest = strings.Repeat("0", 64)
			}
			tx := &userAssetsV2LoaderTx{row: row}
			id, got, err := New(nil).loadDirectoryV2ObservationTx(context.Background(), tx, "tenant", "domain", pin)
			defer got.Discard()
			if mode == "ok" {
				if err != nil || id != "snapshot" || !reflect.DeepEqual(got, o) {
					t.Fatal("loader lost exact observation", err)
				}
			} else if mode == "no-row" {
				if err != nil || id != "" || !reflect.DeepEqual(got, ldapconnection.DirectoryV2Observation{}) {
					t.Fatal("no row became evidence", err)
				}
			} else if mode == "dependency" {
				if !errors.Is(err, row.err) || id != "" {
					t.Fatal("dependency error became empty success", err)
				}
			} else {
				requireUserAssetsV2Error(t, err, 409, "directory_observation_unavailable")
				if id != "" || !reflect.DeepEqual(got, ldapconnection.DirectoryV2Observation{}) {
					t.Fatal("failed loader retained partial observation")
				}
			}
			if row.err == nil && !bytes.Equal(row.body, make([]byte, len(row.body))) {
				t.Fatal("raw database body not cleared")
			}
			clear(raw)
			clear(row.body)
			for _, predicate := range []string{"t.tenant_id=o.tenant_id", "t.domain_id=o.domain_id", "t.actor_id=o.actor_id", "u.tenant_id=o.tenant_id", "u.domain_id=o.domain_id", "c.tenant_id=o.tenant_id", "c.domain_id=o.domain_id", "o.dictionary_version=2", "u.dictionary_version=2", "u.purpose='domain.directory_read.v2'", "t.kind='domain.directory_read.v2'", "t.payload_version=1", "t.state='succeeded'", "u.actor_id=o.actor_id", "o.opener_owner=u.opener_owner", "o.opener_fencing_token=u.opener_fencing_token", "o.opener_attempt=u.opener_attempt", "adtr.domain_directory_use_pins(u)", "c.deleted_at IS NULL", "c.credential_mode='operation_account'", "u.connection_revision=c.connection_revision", "u.connection_credential_generation=c.credential_revision", "u.account_id=c.operation_account_id", "u.account_credential_revision=c.operation_account_credential_revision", "u.policy_revision=$4", "ORDER BY o.created_at DESC,o.task_id DESC LIMIT 1"} {
				if !strings.Contains(tx.sql, predicate) {
					t.Fatal("shared loader lost predicate", predicate)
				}
			}
			if len(tx.args) != 4 || tx.args[0] != "tenant" || tx.args[1] != "domain" || tx.args[2] != pin {
				t.Fatal("loader lost parameterized identity")
			}
		})
	}
}
