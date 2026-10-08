package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestProfileAvatarLimits(t *testing.T) {
	if profileMaxAvatarBytes != 2<<20 || profileMaxAvatarDimension != 1024 || profileMaxStoredAvatarBytes != 4<<20 {
		t.Fatalf("unexpected avatar limits: input=%d dimension=%d stored=%d", profileMaxAvatarBytes, profileMaxAvatarDimension, profileMaxStoredAvatarBytes)
	}
}

func TestProfileAvatarNormalizesPixelsAndStripsMetadata(t *testing.T) {
	source := profileTestImage(7, 5)
	pngRaw := profileTestPNG(t, source)
	marker := []byte("synthetic-profile-metadata-must-not-survive")
	pngWithMetadata := profileTestPNGChunk(t, pngRaw, "tEXt", append([]byte("Comment\x00"), marker...))
	var jpegBuffer bytes.Buffer
	if err := jpeg.Encode(&jpegBuffer, source, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	jpegRaw := jpegBuffer.Bytes()
	// A JPEG comment is valid metadata but must never become stored avatar data.
	comment := []byte{0xff, 0xfe, 0, 0}
	binary.BigEndian.PutUint16(comment[2:], uint16(len(marker)+2))
	comment = append(comment, marker...)
	jpegWithMetadata := append(append(append([]byte(nil), jpegRaw[:2]...), comment...), jpegRaw[2:]...)
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"png", pngRaw},
		{"png_metadata", pngWithMetadata},
		{"png_appended_content", append(append([]byte(nil), pngRaw...), marker...)},
		{"jpeg", jpegRaw},
		{"jpeg_metadata", jpegWithMetadata},
		{"jpeg_appended_content", append(append([]byte(nil), jpegRaw...), marker...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, _, err := image.Decode(bytes.NewReader(tc.raw))
			if err != nil {
				t.Fatalf("invalid test image: %v", err)
			}
			out, err := normalizeProfileAvatar(base64.StdEncoding.EncodeToString(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if len(out) == 0 || len(out) > profileMaxStoredAvatarBytes {
				t.Fatalf("stored image length %d is outside the allowed bounds", len(out))
			}
			got, format, err := image.Decode(bytes.NewReader(out))
			if err != nil || format != "png" {
				t.Fatalf("stored image is not a decodable PNG: format=%q err=%v", format, err)
			}
			if got.Bounds() != want.Bounds() {
				t.Fatalf("bounds changed: got %v want %v", got.Bounds(), want.Bounds())
			}
			for y := want.Bounds().Min.Y; y < want.Bounds().Max.Y; y++ {
				for x := want.Bounds().Min.X; x < want.Bounds().Max.X; x++ {
					w := color.NRGBAModel.Convert(want.At(x, y)).(color.NRGBA)
					g := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)
					if g != w {
						t.Fatalf("pixel (%d,%d) changed: got %+v want %+v", x, y, g, w)
					}
				}
			}
			if bytes.Contains(out, marker) {
				t.Fatal("source metadata survived normalization")
			}
			for _, chunk := range profileTestPNGChunks(t, out) {
				if chunk != "IHDR" && chunk != "IDAT" && chunk != "IEND" {
					t.Fatalf("unexpected stored PNG metadata chunk %q", chunk)
				}
			}
			second, err := normalizeProfileAvatar(base64.StdEncoding.EncodeToString(out))
			if err != nil || !bytes.Equal(second, out) {
				t.Fatalf("normalization is not stable: %v", err)
			}
		})
	}
}

func TestProfileAvatarRejectsNoncanonicalBase64AndUnsupportedInput(t *testing.T) {
	validPNG := profileTestPNG(t, profileTestImage(2, 2))
	// Use a valid PNG whose canonical encoding requires two padding characters.
	payload := []byte("X\x00")
	for (len(validPNG)+12+len(payload))%3 != 1 {
		payload = append(payload, 'a')
	}
	validPNG = profileTestPNGChunk(t, validPNG, "tEXt", payload)
	canonical := base64.StdEncoding.EncodeToString(validPNG)
	if !strings.HasSuffix(canonical, "==") {
		t.Fatal("test fixture is missing base64 padding")
	}
	// Nonzero unused pad bits decode to identical bytes under permissive decoders.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	padIndex := len(canonical) - 3
	padValue := strings.IndexByte(alphabet, canonical[padIndex])
	nonzeroPadBits := canonical[:padIndex] + string(alphabet[padValue+1]) + canonical[padIndex+1:]
	var gifBuffer bytes.Buffer
	if err := gif.Encode(&gifBuffer, profileTestImage(2, 2), nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, file string }{
		{"empty", ""},
		{"url", "https://example.test/avatar.png"},
		{"http_url", "http://127.0.0.1/avatar.png"},
		{"data_uri", "data:image/png;base64," + canonical},
		{"absolute_path", "/tmp/avatar.png"},
		{"relative_path", "../avatar.png"},
		{"file_url", "file:///tmp/avatar.png"},
		{"gif", base64.StdEncoding.EncodeToString(gifBuffer.Bytes())},
		{"svg", base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>`))},
		{"arbitrary_bytes", base64.StdEncoding.EncodeToString([]byte("not an image"))},
		{"leading_space", " " + canonical},
		{"trailing_space", canonical + " "},
		{"embedded_space", canonical[:8] + " " + canonical[8:]},
		{"embedded_tab", canonical[:8] + "\t" + canonical[8:]},
		{"embedded_newline", canonical[:8] + "\n" + canonical[8:]},
		{"embedded_crlf", canonical[:8] + "\r\n" + canonical[8:]},
		{"trailing_newline", canonical + "\n"},
		{"unicode_whitespace", "\u00a0" + canonical},
		{"missing_padding", strings.TrimRight(canonical, "=")},
		{"one_missing_pad", canonical[:len(canonical)-1]},
		{"excess_padding", canonical + "="},
		{"interior_padding", canonical[:8] + "=" + canonical[9:]},
		{"nonzero_pad_bits", nonzeroPadBits},
		{"url_alphabet", "_" + canonical[1:]},
		{"invalid_character", "!" + canonical[1:]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeProfileAvatar(tc.file); err == nil {
				t.Fatal("invalid avatar accepted")
			}
		})
	}
	if _, err := normalizeProfileAvatar(canonical); err != nil {
		t.Fatalf("canonical padded PNG rejected: %v", err)
	}
}

func TestProfileAvatarRawSizeBoundary(t *testing.T) {
	base := profileTestPNG(t, profileTestImage(1, 1))
	for _, size := range []int{profileMaxAvatarBytes - 1, profileMaxAvatarBytes, profileMaxAvatarBytes + 1, profileMaxAvatarBytes + 3} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			payload := append([]byte("Comment\x00"), bytes.Repeat([]byte("x"), size-len(base)-12-len("Comment\x00"))...)
			raw := profileTestPNGChunk(t, base, "tEXt", payload)
			if len(raw) != size {
				t.Fatalf("wrong fixture size: %d", len(raw))
			}
			out, err := normalizeProfileAvatar(base64.StdEncoding.EncodeToString(raw))
			if size > profileMaxAvatarBytes {
				if err == nil {
					t.Fatal("decoded image above the raw-byte limit was accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("valid image at allowed size rejected: %v", err)
			}
			if len(out) > profileMaxStoredAvatarBytes || len(out) >= len(raw) {
				t.Fatalf("normalization did not remove large metadata: output=%d input=%d", len(out), len(raw))
			}
		})
	}
}

func TestProfileAvatarCanonicalPaddingLengths(t *testing.T) {
	base := profileTestPNG(t, profileTestImage(1, 1))
	for padding := 0; padding < 3; padding++ {
		t.Run(fmt.Sprint(padding), func(t *testing.T) {
			payload := []byte("Comment\x00")
			for (3-(len(base)+12+len(payload))%3)%3 != padding {
				payload = append(payload, 'a')
			}
			raw := profileTestPNGChunk(t, base, "tEXt", payload)
			canonical := base64.StdEncoding.EncodeToString(raw)
			if len(canonical)-len(strings.TrimRight(canonical, "=")) != padding {
				t.Fatal("incorrect base64 padding fixture")
			}
			if _, err := normalizeProfileAvatar(canonical); err != nil {
				t.Fatalf("canonical encoding with %d padding characters rejected: %v", padding, err)
			}
		})
	}
}

func TestProfileAvatarDimensionBoundaries(t *testing.T) {
	for _, size := range []image.Point{{1, 1}, {1024, 1}, {1, 1024}, {1024, 1024}, {1025, 1}, {1, 1025}} {
		t.Run(fmt.Sprintf("%dx%d", size.X, size.Y), func(t *testing.T) {
			// Uniform data keeps every fixture well below the raw-byte limit.
			raw := profileTestPNG(t, image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
			out, err := normalizeProfileAvatar(base64.StdEncoding.EncodeToString(raw))
			if size.X > 1024 || size.Y > 1024 {
				if err == nil {
					t.Fatal("oversized dimension accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := png.DecodeConfig(bytes.NewReader(out))
			if err != nil || cfg.Width != size.X || cfg.Height != size.Y {
				t.Fatalf("boundary image was not preserved: %+v %v", cfg, err)
			}
			if len(out) > profileMaxStoredAvatarBytes {
				t.Fatal("stored PNG exceeds output limit")
			}
		})
	}
}

func TestProfileAvatarRejectsMalformedDecodedImages(t *testing.T) {
	validPNG := profileTestPNG(t, profileTestImage(3, 2))
	var jpegBuffer bytes.Buffer
	if err := jpeg.Encode(&jpegBuffer, profileTestImage(3, 2), nil); err != nil {
		t.Fatal(err)
	}
	jpegRaw := jpegBuffer.Bytes()
	scan := bytes.Index(jpegRaw, []byte{0xff, 0xda})
	if scan < 0 {
		t.Fatal("JPEG fixture has no scan")
	}
	truncatedJPEG := jpegRaw[:scan+2+int(binary.BigEndian.Uint16(jpegRaw[scan+2:]))]
	if _, err := jpeg.DecodeConfig(bytes.NewReader(truncatedJPEG)); err != nil {
		t.Fatalf("fixture must have a valid config before pixel decoding fails: %v", err)
	}
	if _, err := png.DecodeConfig(bytes.NewReader(validPNG[:33])); err != nil {
		t.Fatalf("PNG fixture must have a valid config before pixel decoding fails: %v", err)
	}
	corruptCRC := append([]byte(nil), validPNG...)
	corruptCRC[len(corruptCRC)-1] ^= 0xff
	cases := []struct {
		name string
		raw  []byte
	}{
		{"png_signature_only", validPNG[:8]},
		{"png_config_without_pixels", validPNG[:33]},
		{"png_missing_end", validPNG[:len(validPNG)-12]},
		{"png_corrupt_crc", corruptCRC},
		{"jpeg_missing_pixels", truncatedJPEG},
		{"jpeg_truncated", jpegRaw[:len(jpegRaw)-2]},
		{"png_zero_width", profileTestPNGDimensions(t, validPNG, 0, 2)},
		{"png_zero_height", profileTestPNGDimensions(t, validPNG, 3, 0)},
		{"png_huge_width", profileTestPNGDimensions(t, validPNG, 1<<30, 2)},
		{"png_huge_height", profileTestPNGDimensions(t, validPNG, 3, 1<<30)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeProfileAvatar(base64.StdEncoding.EncodeToString(tc.raw)); err == nil {
				t.Fatal("malformed decoded image accepted")
			}
		})
	}
}

func TestProfileBodyRequiresExactJSONContract(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		id        int64
		file      string
	}{
		{"minimal", `{"userId":1,"file":"x"}`, 1, "x"},
		{"order_and_whitespace", " \n { \"file\": \"a/b+c==\", \"userId\": 42 } \t\n", 42, "a/b+c=="},
		{"exact_above_float_precision", `{"userId":9007199254740993,"file":"x"}`, 9007199254740993, "x"},
		{"max_int64", `{"userId":9223372036854775807,"file":"x"}`, 9223372036854775807, "x"},
		{"escaped_exact_keys", `{"\u0075ser\u0049d":3,"\u0066ile":"a\/b\u002bc=="}`, 3, "a/b+c=="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateProfileBody([]byte(tc.raw))
			if err != nil || got.UserID != tc.id || got.File != tc.file {
				t.Fatalf("valid request changed or rejected: got=%+v err=%v", got, err)
			}
		})
	}
	invalid := []struct{ name, raw string }{
		{"empty", ""},
		{"whitespace", " \t\n"},
		{"null", "null"},
		{"array", `[{"userId":1,"file":"x"}]`},
		{"string", `"request"`},
		{"empty_object", `{}`},
		{"missing_id", `{"file":"x"}`},
		{"missing_file", `{"userId":1}`},
		{"empty_file", `{"userId":1,"file":""}`},
		{"null_id", `{"userId":null,"file":"x"}`},
		{"null_file", `{"userId":1,"file":null}`},
		{"number_file", `{"userId":1,"file":123}`},
		{"bool_file", `{"userId":1,"file":true}`},
		{"object_file", `{"userId":1,"file":{}}`},
		{"array_file", `{"userId":1,"file":["x"]}`},
		{"string_id", `{"userId":"1","file":"x"}`},
		{"bool_id", `{"userId":true,"file":"x"}`},
		{"object_id", `{"userId":{},"file":"x"}`},
		{"array_id", `{"userId":[1],"file":"x"}`},
		{"zero", `{"userId":0,"file":"x"}`},
		{"negative_zero", `{"userId":-0,"file":"x"}`},
		{"negative", `{"userId":-1,"file":"x"}`},
		{"decimal", `{"userId":1.0,"file":"x"}`},
		{"fraction", `{"userId":0.1,"file":"x"}`},
		{"exponent", `{"userId":1e0,"file":"x"}`},
		{"exponent_integer", `{"userId":1E3,"file":"x"}`},
		{"leading_zero", `{"userId":01,"file":"x"}`},
		{"leading_plus", `{"userId":+1,"file":"x"}`},
		{"overflow", `{"userId":9223372036854775808,"file":"x"}`},
		{"underflow", `{"userId":-9223372036854775809,"file":"x"}`},
		{"huge_number", `{"userId":999999999999999999999999999999999999999,"file":"x"}`},
		{"wrong_id_case", `{"UserId":1,"file":"x"}`},
		{"wrong_id_acronym", `{"userID":1,"file":"x"}`},
		{"wrong_file_case", `{"userId":1,"File":"x"}`},
		{"escaped_wrong_case", `{"\u0055serId":1,"file":"x"}`},
		{"duplicate_id", `{"userId":1,"userId":2,"file":"x"}`},
		{"duplicate_file", `{"userId":1,"file":"x","file":"y"}`},
		{"duplicate_escaped_id", `{"userId":1,"user\u0049d":1,"file":"x"}`},
		{"duplicate_escaped_file", `{"userId":1,"file":"x","\u0066ile":"x"}`},
		{"id_case_alias", `{"userId":1,"UserID":2,"file":"x"}`},
		{"unknown", `{"userId":1,"file":"x","extra":null}`},
		{"tenant_injection", `{"userId":1,"file":"x","tenantId":"other"}`},
		{"actor_injection", `{"userId":1,"file":"x","actorId":2}`},
		{"trailing_object", `{"userId":1,"file":"x"}{}`},
		{"trailing_null", `{"userId":1,"file":"x"} null`},
		{"trailing_junk", `{"userId":1,"file":"x"}x`},
		{"trailing_comma", `{"userId":1,"file":"x",}`},
		{"unterminated", `{"userId":1,"file":"x"`},
		{"invalid_utf8", "{\"userId\":1,\"file\":\"" + string([]byte{0xff}) + "\"}"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateProfileBody([]byte(tc.raw)); err == nil {
				t.Fatalf("invalid request accepted: %q", tc.raw)
			}
		})
	}
}

func profileTestImage(width, height int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			m.SetNRGBA(x, y, color.NRGBA{R: uint8(31*x + 7*y), G: uint8(19*x + 53*y), B: uint8(71*x + 11*y), A: uint8(80 + (x+y)%4*50)})
		}
	}
	return m
}

func profileTestPNG(t *testing.T, m image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func profileTestPNGChunk(t *testing.T, raw []byte, kind string, payload []byte) []byte {
	t.Helper()
	if len(raw) < 33 || string(raw[12:16]) != "IHDR" || len(kind) != 4 {
		t.Fatal("invalid PNG chunk fixture")
	}
	chunk := make([]byte, len(payload)+12)
	binary.BigEndian.PutUint32(chunk, uint32(len(payload)))
	copy(chunk[4:8], kind)
	copy(chunk[8:], payload)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	return append(append(append([]byte(nil), raw[:33]...), chunk...), raw[33:]...)
}

func profileTestPNGDimensions(t *testing.T, raw []byte, width, height uint32) []byte {
	t.Helper()
	if len(raw) < 33 || string(raw[12:16]) != "IHDR" {
		t.Fatal("invalid PNG dimension fixture")
	}
	out := append([]byte(nil), raw...)
	binary.BigEndian.PutUint32(out[16:20], width)
	binary.BigEndian.PutUint32(out[20:24], height)
	binary.BigEndian.PutUint32(out[29:33], crc32.ChecksumIEEE(out[12:29]))
	return out
}

func profileTestPNGChunks(t *testing.T, raw []byte) []string {
	t.Helper()
	if len(raw) < 8 || string(raw[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatal("missing stored PNG signature")
	}
	var chunks []string
	for rest := raw[8:]; len(rest) > 0; {
		if len(rest) < 12 {
			t.Fatal("truncated stored PNG chunk")
		}
		length := int(binary.BigEndian.Uint32(rest[:4]))
		if length > len(rest)-12 {
			t.Fatal("invalid stored PNG chunk length")
		}
		chunks = append(chunks, string(rest[4:8]))
		rest = rest[length+12:]
	}
	return chunks
}
