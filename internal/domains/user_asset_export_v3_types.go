package domains

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

const UserAssetExportV3KindName = "user_assets.v3.export"
const UserAssetExportMaxBytes = 32 << 20
const UserAssetExportMaxRows = 10000

// ExportPayload contains no credentials, actor or mutable latest-source selector.
type UserAssetExportV3Payload struct {
	DomainID                   string   `json:"domainId"`
	ExpectedRevision           string   `json:"expectedRevision"`
	ExpectedCredentialRevision string   `json:"expectedCredentialRevision"`
	ObservationID              string   `json:"observationId"`
	Search                     string   `json:"search"`
	SAMAccountName             string   `json:"sAMAccountName"`
	StartTm                    string   `json:"startTm"`
	EndTm                      string   `json:"endTm"`
	CreatTm                    string   `json:"creatTm"`
	ScreenColumns              []string `json:"screenColumns"`
}

func (p UserAssetExportV3Payload) Filter() UserAssetsV2Filter {
	return UserAssetsV2Filter{DomainID: p.DomainID, ExpectedRevision: p.ExpectedRevision, ExpectedCredentialRevision: p.ExpectedCredentialRevision, ObservationID: p.ObservationID, Search: p.Search, SAMAccountName: p.SAMAccountName, StartTm: p.StartTm, EndTm: p.EndTm, CreatTm: p.CreatTm, PageIdx: 1, PageSize: 10}
}

var userAssetExportColumns = []string{"guid", "sAMAccountName", "sid", "mail", "dn", "whenCreated"}

// ValidateExportV3JSON rejects duplicate keys and lone surrogate escapes before
// encoding/json can replace them. It also enforces a closed, shallow object.
func ValidateUserAssetExportV3JSON(raw []byte) error {
	if len(raw) == 0 || len(raw) > 32768 || !utf8.Valid(raw) {
		return problem(400, "invalid_input")
	}
	// Unescaped backslashes introduce JSON escapes. Paired surrogate escapes are
	// allowed; escaped literal backslashes are skipped without interpreting them.
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return problem(400, "invalid_input")
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return problem(400, "invalid_input")
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return problem(400, "invalid_input")
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return problem(400, "invalid_input")
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return problem(400, "invalid_input")
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return problem(400, "invalid_input")
			}
			i += 6
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return problem(400, "invalid_input")
	}
	seen := map[string]bool{}
	for d.More() {
		token, e = d.Token()
		key, ok := token.(string)
		if e != nil || !ok || seen[key] {
			return problem(400, "invalid_input")
		}
		seen[key] = true
		var v json.RawMessage
		if d.Decode(&v) != nil || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return problem(400, "invalid_input")
		}
	}
	if token, e = d.Token(); e != nil || token != json.Delim('}') {
		return problem(400, "invalid_input")
	}
	if _, e = d.Token(); e != io.EOF {
		return problem(400, "invalid_input")
	}
	return nil
}

func ValidateUserAssetExportV3Payload(raw json.RawMessage) (json.RawMessage, error) {
	if err := ValidateUserAssetExportV3JSON(raw); err != nil {
		return nil, err
	}
	var p UserAssetExportV3Payload
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return nil, problem(400, "invalid_input")
	}
	if p.ObservationID == "" || p.ScreenColumns == nil {
		return nil, problem(400, "invalid_input")
	}
	if err := ValidateUserAssetsV2Filter(p.Filter()); err != nil {
		return nil, err
	}
	if len(p.ScreenColumns) == 0 {
		p.ScreenColumns = append([]string(nil), userAssetExportColumns...)
	}
	if len(p.ScreenColumns) > 6 {
		return nil, problem(400, "invalid_input")
	}
	seen := map[string]bool{}
	for _, c := range p.ScreenColumns {
		found := false
		for _, known := range userAssetExportColumns {
			if known == c {
				found = true
			}
		}
		if !found || seen[c] {
			return nil, problem(400, "invalid_input")
		}
		seen[c] = true
	}
	return json.Marshal(p)
}

func userAssetExportHeaders(columns []string) []string {
	names := map[string]string{"guid": "GUID", "sAMAccountName": "SAM", "sid": "SID", "mail": "mail", "dn": "DN", "whenCreated": "whenCreated"}
	out := make([]string, len(columns))
	for i, c := range columns {
		out[i] = names[c]
	}
	return out
}

func userAssetExportCells(row directoryassets.PublicObjectV3, columns []string) []string {
	value := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	out := make([]string, len(columns))
	for i, c := range columns {
		switch c {
		case "guid":
			out[i] = row.GUID
		case "sAMAccountName":
			out[i] = value(row.SAMAccountName)
		case "sid":
			out[i] = value(row.ObjectSID)
		case "mail":
			out[i] = value(row.Mail)
		case "dn":
			out[i] = row.DN
		case "whenCreated":
			out[i] = value(row.WhenCreated)
		}
	}
	return out
}
