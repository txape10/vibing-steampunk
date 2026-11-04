package temse

import (
	"encoding/binary"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

// list_unicode.hex is the TemSe object of a one-page list spool written by a
// background job on a 7.58 system: a header line with two colour commands,
// a rule of box-drawing characters, one line of output, and the page end.
func TestDecodeList(t *testing.T) {
	raw, err := os.ReadFile("testdata/list_unicode.hex")
	if err != nil {
		t.Fatal(err)
	}
	data, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	l, err := DecodeList(data, "4103")
	if err != nil {
		t.Fatal(err)
	}
	if l.Records != 4 || l.Pages != 1 || len(l.Lines) != 4 {
		t.Fatalf("records %d pages %d lines %d", l.Records, l.Pages, len(l.Lines))
	}
	head := l.Lines[0]
	if !strings.HasPrefix(head.Text, "04.09.2026") || !strings.Contains(head.Text, "vsp: deep objects to INDX for cluster parser probes") || !strings.HasSuffix(head.Text, "1") {
		t.Errorf("header line: %q", head.Text)
	}
	if strings.Join(head.Formats, ",") != "COL0N,COL0H" {
		t.Errorf("formats: %v", head.Formats)
	}
	if rule := l.Lines[1].Text; !strings.HasPrefix(rule, "───") || strings.ContainsRune(rule, escLineArt) || len([]rune(rule)) != 255 {
		t.Errorf("rule: %q (%d runes)", rule[:12], len([]rune(rule)))
	}
	if l.Lines[2].Text != "exported VSPDEEP to INDX(ZE)" {
		t.Errorf("body: %q", l.Lines[2].Text)
	}
	if l.Lines[3].Control != "P" || l.Lines[3].Text != "" {
		t.Errorf("page end: %+v", l.Lines[3])
	}
	text := l.Text()
	if !strings.HasSuffix(text, "exported VSPDEEP to INDX(ZE)\n") || strings.Count(text, "\n") != 3 {
		t.Errorf("text: %q", text)
	}
	if _, err := DecodeList(data, "1100"); err == nil {
		t.Error("single-byte code page accepted")
	}
	if _, err := DecodeList(data[:len(data)-10], "4103"); err == nil {
		t.Error("truncated data accepted")
	}
}

// record builds one TemSe record: two-byte big-endian length (header + body),
// two flag bytes, and the text as UTF-16.
func record(text string, bigEndian bool) []byte {
	units := utf16.Encode([]rune(text))
	body := make([]byte, 2*len(units))
	for i, u := range units {
		if bigEndian {
			binary.BigEndian.PutUint16(body[2*i:], u)
		} else {
			binary.LittleEndian.PutUint16(body[2*i:], u)
		}
	}
	out := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint16(out, uint16(4+len(body)))
	return append(out, body...)
}

func TestDecodeList_BigEndian4102(t *testing.T) {
	data := append(record(" hola", true), record("P", true)...)
	l, err := DecodeList(data, "4102")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Lines) != 2 || l.Lines[0].Text != "hola" || l.Pages != 1 {
		t.Errorf("got %+v", l)
	}
}

func TestDecodeList_RecordShorterThanHeader(t *testing.T) {
	if _, err := DecodeList([]byte{0, 3, 0, 0}, "4103"); err == nil {
		t.Error("length < 4 accepted")
	}
}

func TestDecodeList_OddBodyBytes(t *testing.T) {
	// length 7 = 4 header + 3 body bytes: not whole UTF-16 units
	if _, err := DecodeList([]byte{0, 7, 0, 0, 0x20, 0, 0x41}, "4103"); err == nil {
		t.Error("odd body accepted")
	}
}

func TestDecodeList_TruncatedFormatEscape(t *testing.T) {
	// a format escape with fewer than five characters after it must not panic
	data := record(" ab\uF8FFCO", false)
	l, err := DecodeList(data, "4103")
	if err != nil {
		t.Fatal(err)
	}
	if l.Lines[0].Text != "ab" || len(l.Lines[0].Formats) != 1 || l.Lines[0].Formats[0] != "CO" {
		t.Errorf("got %+v", l.Lines[0])
	}
}

func TestDecodeList_NonASCIIControlKept(t *testing.T) {
	data := record("é resto", false)
	l, err := DecodeList(data, "4103")
	if err != nil {
		t.Fatal(err)
	}
	if l.Lines[0].Control != "é" || l.Lines[0].Text != " resto" {
		t.Errorf("got %+v", l.Lines[0])
	}
}

func TestDecodeList_TwoPages(t *testing.T) {
	data := append(append(record(" a", false), record("P", false)...), record(" b", false)...)
	l, err := DecodeList(data, "4103")
	if err != nil {
		t.Fatal(err)
	}
	if l.Pages != 2 || l.Lines[2].Page != 2 {
		t.Errorf("pages %d, last line page %d", l.Pages, l.Lines[2].Page)
	}
}

func TestDecodeList_EmptyInput(t *testing.T) {
	for _, in := range [][]byte{nil, {}} {
		l, err := DecodeList(in, "4103")
		if err != nil || len(l.Lines) != 0 || l.Pages != 1 || l.Records != 0 {
			t.Errorf("got %+v, %v", l, err)
		}
	}
}

func TestDecodeList_ShortTrailingHeader(t *testing.T) {
	for n := 1; n <= 3; n++ {
		if _, err := DecodeList(make([]byte, n), "4103"); err == nil {
			t.Errorf("%d stray bytes accepted", n)
		}
	}
}

func TestDecodeList_PageEndKeepsItsText(t *testing.T) {
	l, err := DecodeList(record("Pextra", false), "4103")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Lines) != 1 || l.Lines[0].Control != "P" || l.Lines[0].Text != "extra" {
		t.Errorf("got %+v", l.Lines)
	}
}

func TestDecodeList_EmptyBodyRecordCounted(t *testing.T) {
	l, err := DecodeList(record("", false), "4103")
	if err != nil {
		t.Fatal(err)
	}
	if l.Records != 1 || len(l.Lines) != 0 {
		t.Errorf("got %+v", l)
	}
}

func TestDecodeList_SurrogatePair(t *testing.T) {
	l, err := DecodeList(record(" a😀b", false), "4103")
	if err != nil {
		t.Fatal(err)
	}
	if l.Lines[0].Text != "a😀b" {
		t.Errorf("got %q", l.Lines[0].Text)
	}
}

func TestDecodeList_CompleteFormatAndLineArt(t *testing.T) {
	l, err := DecodeList(record(" \uF8FFCOL0Nx\uF8FC─y", false), "4103")
	if err != nil {
		t.Fatal(err)
	}
	ln := l.Lines[0]
	if ln.Text != "x─y" || len(ln.Formats) != 1 || ln.Formats[0] != "COL0N" {
		t.Errorf("got %+v", ln)
	}
}

func TestList_TextMultiPage(t *testing.T) {
	data := append(append(record(" a", false), record("P", false)...), record(" b", false)...)
	l, _ := DecodeList(data, "4103")
	if got := l.Text(); got != "a\n\fb\n" {
		t.Errorf("got %q", got)
	}
}

func FuzzDecodeList(f *testing.F) {
	f.Add(record(" a\uF8FFCO", false), "4103")
	f.Add([]byte{0, 4, 0, 0}, "4102")
	f.Fuzz(func(t *testing.T, data []byte, charcod string) {
		_, _ = DecodeList(data, charcod)
	})
}
