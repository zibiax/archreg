package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestCatalog finds every string the code translates and checks that the
// Swedish catalogue has it, with the same format verbs in the same order.
func TestCatalog(t *testing.T) {
	goLit := `("(?:[^"\\]|\\.)*")`
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\b(?:tr|trf|T|F)\(\s*` + goLit),                             // Go code
		regexp.MustCompile(`\{\{\s*tf?\s+` + goLit),                                     // templates
		regexp.MustCompile("regexp\\.MustCompile\\(`[^`]*`\\), " + goLit),               // chat glossary
		regexp.MustCompile(`b\.Warnings = append\(b\.Warnings, ` + goLit),               // import notes
		regexp.MustCompile(`kindRidgeHeight: ` + goLit + `, kindTotalHeight: ` + goLit), // envelope names
	}
	var missing []string
	seen := map[string]bool{}
	err := filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "i18n") {
			return err
		}
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".tmpl") {
			return nil
		}
		b, _ := os.ReadFile(path)
		for _, re := range patterns {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				for _, lit := range m[1:] {
					s, err := strconv.Unquote(lit)
					if err != nil || seen[s] {
						continue
					}
					seen[s] = true
					tr, ok := sv[s]
					switch {
					case !ok:
						missing = append(missing, path+": "+s)
					case verbs(tr) != verbs(s):
						t.Errorf("%s: format verbs differ:\n  en %q\n  sv %q", path, s, tr)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) < 150 {
		t.Fatalf("found only %d strings; the patterns are probably wrong", len(seen))
	}
	for _, m := range missing {
		t.Errorf("no Swedish for %s", m)
	}
}

var verbRe = regexp.MustCompile(`%[-+#0]*[0-9.]*[a-zA-Z]`)

func verbs(s string) string { return strings.Join(verbRe.FindAllString(s, -1), " ") }

func TestT(t *testing.T) {
	if SV.T("Must fix") != "Måste åtgärdas" || EN.T("Must fix") != "Must fix" || SV.T("not in catalogue") != "not in catalogue" {
		t.Fatal("T")
	}
	if SV.Noun("door", 2) != "dörrar" || EN.Noun("window", 1) != "window" || Parse("sv-SE") != SV || Parse("en-GB") != EN {
		t.Fatal("Noun/Parse")
	}
}
