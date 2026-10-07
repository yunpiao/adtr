package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"unicode/utf8"
)

const (
	profileMaxAvatarBytes       = 2 << 20
	profileMaxAvatarDimension   = 1024
	profileMaxStoredAvatarBytes = 4 << 20
	profileMaxBodyBytes         = 3 << 20
)

type profileUpload struct {
	UserID int64
	File   string
}

// Decode field tokens rather than a map: duplicate/case-folded keys must not
// silently change a security-relevant identifier. No implicit defaults or nulls.
func validateProfileBody(raw []byte) (profileUpload, error) {
	var out profileUpload
	invalid := fail(400, "invalid_input")
	if !utf8.Valid(raw) || len(raw) > profileMaxBodyBytes {
		return out, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return out, invalid
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return out, invalid
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return out, invalid
		}
		switch key {
		case "userId":
			if json.Unmarshal(value, &out.UserID) != nil || out.UserID <= 0 {
				return out, invalid
			}
		case "file":
			if json.Unmarshal(value, &out.File) != nil || out.File == "" {
				return out, invalid
			}
		default:
			return out, invalid
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return out, invalid
	}
	if !seen["userId"] || !seen["file"] {
		return out, invalid
	}
	if d.Decode(new(any)) != io.EOF {
		return out, invalid
	}
	return out, nil
}

// Only canonical padded base64 is accepted. DecodeConfig bounds pixel allocation
// before decoding; re-encoding drops metadata and any appended active content.
func normalizeProfileAvatar(file string) ([]byte, error) {
	invalid := fail(400, "invalid_avatar")
	if len(file) == 0 || len(file) > base64.StdEncoding.EncodedLen(profileMaxAvatarBytes) {
		return nil, invalid
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(file)
	if err != nil || len(raw) == 0 || len(raw) > profileMaxAvatarBytes || base64.StdEncoding.EncodeToString(raw) != file {
		return nil, invalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > profileMaxAvatarDimension || config.Height > profileMaxAvatarDimension {
		return nil, invalid
	}
	img, decodedFormat, err := image.Decode(bytes.NewReader(raw))
	if err != nil || decodedFormat != format || img.Bounds().Dx() != config.Width || img.Bounds().Dy() != config.Height {
		return nil, invalid
	}
	var out bytes.Buffer
	if err = png.Encode(&out, img); err != nil || out.Len() > profileMaxStoredAvatarBytes {
		return nil, invalid
	}
	return out.Bytes(), nil
}
