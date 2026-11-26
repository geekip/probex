package server

import (
	"bytes"
	"strings"
	"testing"
)

func TestTextEncodingRoundTripPreservesBytes(t *testing.T) {
	cases := []struct {
		name     string
		data     []byte
		encoding string
		text     string
		bom      bool
	}{
		{"empty", nil, "utf-8", "", false},
		{"UTF8 mixed newlines", []byte("中文\r\nline\nlast\rend\t\f"), "utf-8", "中文\r\nline\nlast\rend\t\f", false},
		{"UTF8 BOM", append([]byte{0xef, 0xbb, 0xbf}, []byte("中文\r\n")...), "utf-8", "中文\r\n", true},
		{"UTF8 replacement character is valid", []byte("replacement �"), "utf-8", "replacement �", false},
		{"GBK Chinese and euro", []byte{0xd6, 0xd0, 0xce, 0xc4, 0x80, 0x0d, 0x0a}, "gbk", "中文€\r\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, requested := range []string{"auto", c.encoding} {
				text, encoding, bom, err := decodeText(c.data, requested)
				if err != nil || text != c.text || encoding != c.encoding || bom != c.bom {
					t.Fatalf("decode %q: text=%q encoding=%q bom=%v err=%v", requested, text, encoding, bom, err)
				}
				encoded, err := encodeText(text, encoding, bom)
				if err != nil || !bytes.Equal(encoded, c.data) {
					t.Fatalf("changed original bytes: got=%x want=%x err=%v", encoded, c.data, err)
				}
			}
		})
	}
}

func TestTextEncodingRejectsInvalidAndUnencodableContent(t *testing.T) {
	for _, c := range []struct {
		name     string
		data     []byte
		encoding string
	}{
		{"bad UTF8", []byte{0xff}, "utf-8"},
		{"unfinished GBK", []byte{0x81}, "gbk"},
		{"bad GBK trail", []byte{0x81, 0x20}, "gbk"},
		{"unassigned GBK", []byte{0xfe, 0xfe}, "gbk"},
		{"NUL binary", []byte("abc\x00def"), "auto"},
		{"control binary", []byte("abc\x01def"), "auto"},
		{"UTF16", []byte{0xff, 0xfe, 0x61, 0}, "auto"},
		{"unknown encoding", []byte("text"), "latin1"},
		{"BOM conflicts with GBK", []byte{0xef, 0xbb, 0xbf}, "gbk"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, _, _, err := decodeText(c.data, c.encoding); err == nil {
				t.Fatal("invalid content accepted")
			}
		})
	}
	for _, c := range []struct {
		name, text, encoding string
		bom                  bool
	}{
		{"emoji outside GBK", "中文😀", "gbk", false},
		{"GBK BOM", "中文", "gbk", true},
		{"invalid UTF8", string([]byte{0xff}), "utf-8", false},
		{"binary text", "abc\x00def", "utf-8", false},
		{"unknown encoding", "text", "auto", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := encodeText(c.text, c.encoding, c.bom); err == nil {
				t.Fatal("invalid content encoded")
			}
		})
	}
	// Explicit encoding selection changes interpretation rather than rewriting bytes.
	data := []byte{0xc2, 0xa1}
	auto, _, _, err := decodeText(data, "auto")
	if err != nil {
		t.Fatal(err)
	}
	gbk, encoding, bom, err := decodeText(data, "gbk")
	if err != nil || auto == gbk {
		t.Fatal(auto, gbk, err)
	}
	encoded, err := encodeText(gbk, encoding, bom)
	if err != nil || !bytes.Equal(encoded, data) {
		t.Fatal(encoded, err)
	}
	_, err = encodeText("😀", "gbk", false)
	if err == nil || !strings.Contains(err.Error(), "GBK cannot encode") {
		t.Fatal(err)
	}
}
