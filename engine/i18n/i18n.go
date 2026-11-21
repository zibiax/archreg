// Package i18n translates the app's text between English and Swedish.
//
// Strings are written in English in the code and looked up in a Swedish
// catalogue keyed by the English text (gettext style), so the English
// stays readable where it's used. TestCatalog checks that every string the
// code passes to T or F has a Swedish entry.
package i18n

import (
	"fmt"
	"regexp"
)

// Lang is a language code: "en" or "sv".
type Lang string

const (
	EN Lang = "en"
	SV Lang = "sv"
)

// Parse returns SV for anything Swedish ("sv", "sv-SE"), else EN.
func Parse(s string) Lang {
	if len(s) >= 2 && (s[:2] == "sv" || s[:2] == "SV") {
		return SV
	}
	return EN
}

// T translates a fixed string.
func (l Lang) T(s string) string {
	if l == SV {
		if t, ok := sv[s]; ok {
			return t
		}
	}
	return s
}

// F translates a format string, then formats it (with decimal commas in
// Swedish).
func (l Lang) F(format string, a ...any) string {
	return l.Dec(fmt.Sprintf(l.T(format), a...))
}

var decimal = regexp.MustCompile(`(\d)\.(\d)`)

// Dec writes decimals the local way: "1,5 m" in Swedish, "1.5 m" in English.
func (l Lang) Dec(s string) string {
	if l != SV {
		return s
	}
	return decimal.ReplaceAllString(s, "$1,$2")
}

// Noun returns the singular or plural of an element noun ("room", "window",
// "door", "building", "element").
func (l Lang) Noun(noun string, n int) string {
	forms, ok := nouns[l][noun]
	if !ok {
		forms = nouns[EN][noun]
	}
	if forms[0] == "" {
		return noun
	}
	if n == 1 {
		return forms[0]
	}
	return forms[1]
}

var nouns = map[Lang]map[string][2]string{
	EN: {"room": {"room", "rooms"}, "window": {"window", "windows"}, "door": {"door", "doors"}, "building": {"building", "buildings"}, "element": {"element", "elements"}},
	SV: {"room": {"rum", "rum"}, "window": {"fönster", "fönster"}, "door": {"dörr", "dörrar"}, "building": {"byggnad", "byggnader"}, "element": {"del", "delar"}},
}

// Has reports whether s has a Swedish translation (for tests).
func Has(s string) bool { _, ok := sv[s]; return ok }
