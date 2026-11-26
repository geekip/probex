package server

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

func decodeText(data []byte, encoding string) (text, selected string, bom bool, err error) {
	if encoding != "auto" && encoding != "utf-8" && encoding != "gbk" {
		return "", "", false, errors.New("encoding must be auto, utf-8 or gbk")
	}
	bom = bytes.HasPrefix(data, utf8BOM)
	if bom {
		if encoding == "gbk" {
			return "", "", false, errors.New("UTF-8 BOM conflicts with GBK; select UTF-8")
		}
		data = data[len(utf8BOM):]
		encoding = "utf-8"
	}
	if encoding == "auto" {
		encoding = "utf-8"
		if !utf8.Valid(data) {
			encoding = "gbk"
		}
	}
	if encoding == "utf-8" {
		if !utf8.Valid(data) {
			return "", "", false, errors.New("invalid UTF-8 text")
		}
		text = string(data)
	} else {
		decoded, failure := simplifiedchinese.GBK.NewDecoder().Bytes(data)
		if failure != nil {
			return "", "", false, failure
		}
		if bytes.ContainsRune(decoded, utf8.RuneError) {
			return "", "", false, errors.New("invalid GBK text")
		}
		text = string(decoded)
	}
	if !textCharacters(text) {
		return "", "", false, errors.New("binary content cannot be opened as text")
	}
	return text, encoding, bom, nil
}

func encodeText(text, encoding string, bom bool) ([]byte, error) {
	if !utf8.ValidString(text) {
		return nil, errors.New("invalid UTF-8 text")
	}
	if !textCharacters(text) {
		return nil, errors.New("binary content cannot be saved as text")
	}
	switch encoding {
	case "utf-8":
		data := []byte(text)
		if bom {
			data = append(append([]byte(nil), utf8BOM...), data...)
		}
		return data, nil
	case "gbk":
		if bom {
			return nil, errors.New("GBK does not support a UTF-8 BOM")
		}
		data, err := simplifiedchinese.GBK.NewEncoder().String(text)
		if err != nil {
			return nil, fmt.Errorf("text contains a character that GBK cannot encode: %w", err)
		}
		return []byte(data), nil
	default:
		return nil, errors.New("encoding must be utf-8 or gbk")
	}
}

func textCharacters(text string) bool {
	return !strings.ContainsFunc(text, func(r rune) bool {
		return r < 0x20 && r != '\t' && r != '\r' && r != '\n' && r != '\f' || r == 0x7f
	})
}
