package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// dumpsPy marshals v the way Python's json.dumps(v) would: default
// separators (", " / ": "), ensure_ascii=True, no HTML escaping, and (since
// v is expected to be a struct, not a map) keys in field-declaration order.
//
// This leans on encoding/json for everything it already does correctly --
// struct field ordering, and all the fiddly string escaping (quotes,
// backslashes, control characters) -- and only patches the handful of
// surface differences from Python's encoder on top: wider separators and
// \uXXXX-escaping non-ASCII runes. Floats are handled by the pyFloat type's
// MarshalJSON, since Python's float formatting (always keep a decimal
// point, sign-and-zero-padded exponents) has no stdlib equivalent.
func dumpsPy(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	// Encoder.Encode always appends a trailing newline; json.dumps doesn't.
	compact := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	return pythonizeJSON(compact), nil
}

// pythonizeJSON rewrites Go's compact JSON encoding into Python's default
// json.dumps surface form: a space after every structural "," and ":", and
// ensure_ascii-style \uXXXX escaping for any rune outside printable ASCII
// (Go's encoder leaves valid non-ASCII UTF-8 in string literals untouched,
// e.g. the real "Mar 2024: FID→INP" chart annotation, which Python instead
// renders as "Mar 2024: FID→INP" -- this is what makes that so here).
// Quote/backslash/control-char escaping is already done by encoding/json;
// this only ever copies or widens what's already valid JSON text.
func pythonizeJSON(data []byte) string {
	var b strings.Builder
	b.Grow(len(data) + len(data)/4)

	inString := false
	for i := 0; i < len(data); {
		c := data[i]

		if !inString {
			switch c {
			case '"':
				inString = true
				b.WriteByte(c)
			case ',':
				b.WriteString(", ")
			case ':':
				b.WriteString(": ")
			default:
				b.WriteByte(c)
			}
			i++
			continue
		}

		switch {
		case c == '"':
			inString = false
			b.WriteByte(c)
			i++
		case c == '\\':
			// Copy the escape sequence verbatim -- encoding/json already
			// chose the correct form (\n, \", , ...).
			b.WriteByte(c)
			i++
			if i < len(data) {
				b.WriteByte(data[i])
				if data[i] == 'u' {
					end := i + 5
					if end > len(data) {
						end = len(data)
					}
					b.WriteString(string(data[i+1 : end]))
					i = end
				} else {
					i++
				}
			}
		case c < 0x80:
			b.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRune(data[i:])
			writeUnicodeEscape(&b, r)
			i += size
		}
	}

	return b.String()
}

func writeUnicodeEscape(b *strings.Builder, r rune) {
	if r <= 0xffff {
		fmt.Fprintf(b, `\u%04x`, r)
		return
	}
	r1, r2 := utf16.EncodeRune(r)
	fmt.Fprintf(b, `\u%04x\u%04x`, r1, r2)
}

// pyFloat marshals like Python's repr(float): the shortest round-tripping
// decimal (Go's own strconv.FormatFloat with -1 precision computes the same
// digit sequence CPython's repr would), but reformatted with Python's
// surface conventions -- a trailing ".0" kept on whole numbers, and a
// sign-and-zero-padded (>=2 digit) exponent in scientific notation. Go's
// default float encoding drops the trailing ".0" and doesn't zero-pad
// exponents, which would otherwise diverge from Python's output.
type pyFloat float64

func (f pyFloat) MarshalJSON() ([]byte, error) {
	return []byte(formatPyFloat(float64(f))), nil
}

func pyFloats(vals []float64) []pyFloat {
	out := make([]pyFloat, len(vals))
	for i, v := range vals {
		out[i] = pyFloat(v)
	}
	return out
}

func formatPyFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}

	neg := math.Signbit(f)
	digits, exp := shortestDigits(math.Abs(f))
	decpt := exp + 1

	var s string
	if decpt < -3 || decpt > 16 {
		s = formatPySci(digits, exp)
	} else {
		s = formatPyFixed(digits, decpt)
	}
	if neg {
		s = "-" + s
	}
	return s
}

// shortestDigits returns af's shortest round-tripping decimal digit string
// (no sign, no decimal point) and its base-10 exponent, i.e. af equals
// 0.<digits> * 10^(exp+1), by parsing Go's own shortest round-trip
// scientific-notation formatting.
func shortestDigits(af float64) (digits string, exp int) {
	s := strconv.FormatFloat(af, 'e', -1, 64) // e.g. "6.341219649117935e-01"
	i := strings.IndexByte(s, 'e')
	exp, _ = strconv.Atoi(s[i+1:])
	return strings.Replace(s[:i], ".", "", 1), exp
}

func formatPyFixed(digits string, decpt int) string {
	switch {
	case decpt <= 0:
		return "0." + strings.Repeat("0", -decpt) + digits
	case decpt >= len(digits):
		return digits + strings.Repeat("0", decpt-len(digits)) + ".0"
	default:
		return digits[:decpt] + "." + digits[decpt:]
	}
}

func formatPySci(digits string, exp int) string {
	mantissa := digits[:1]
	if len(digits) > 1 {
		mantissa += "." + digits[1:]
	}
	return fmt.Sprintf("%se%+03d", mantissa, exp)
}
