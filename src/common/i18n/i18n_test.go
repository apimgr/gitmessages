package i18n

import (
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestLocaleParity(t *testing.T) {
	if problems := ValidateParity(); len(problems) != 0 {
		t.Fatalf("translation key parity errors: %#v", problems)
	}
}

func TestForbiddenFormatTokensAreRejected(t *testing.T) {
	for _, value := range []string{"value %s", "count %d", "enabled %t", "item {0}"} {
		if !hasForbiddenFormatToken(value) {
			t.Errorf("hasForbiddenFormatToken(%q) = false", value)
		}
	}
	for _, value := range []string{"100% complete", "Hello, {name}"} {
		if hasForbiddenFormatToken(value) {
			t.Errorf("hasForbiddenFormatToken(%q) = true", value)
		}
	}
}

func TestFallbackAndDirection(t *testing.T) {
	if got := T("unsupported", "nav.home"); got != "Home" {
		t.Fatalf("unsupported locale fallback = %q", got)
	}
	if got := Get("ar").Meta.Direction; got != "rtl" {
		t.Fatalf("Arabic direction = %q", got)
	}
	if got := T("fr", "missing.translation.key"); got != "missing.translation.key" {
		t.Fatalf("unknown key fallback = %q", got)
	}
}

func TestRequiredLanguagesAndMetadata(t *testing.T) {
	wantCategories := map[string][]string{
		"en": {"one", "other"}, "es": {"one", "other"}, "fr": {"one", "other"}, "de": {"one", "other"},
		"it": {"one", "many", "other"}, "ar": {"zero", "one", "two", "few", "many", "other"},
		"zh": {"other"}, "ja": {"other"},
	}
	for _, code := range []string{"en", "es", "fr", "de", "it", "ar", "zh", "ja"} {
		if !IsSupported(code) {
			t.Errorf("required language %q is not registered", code)
			continue
		}
		locale := Get(code)
		if locale.Meta.Language != code || locale.Meta.NativeName == "" || locale.Meta.Direction == "" {
			t.Errorf("incomplete metadata for %q: %#v", code, locale.Meta)
		}
		if !slices.Contains(locale.Meta.PluralCategories, "other") {
			t.Errorf("locale %q does not declare the required other plural category", code)
		}
		if !reflect.DeepEqual(locale.Meta.PluralCategories, wantCategories[code]) {
			t.Errorf("locale %q plural categories = %v, want %v", code, locale.Meta.PluralCategories, wantCategories[code])
		}
	}
}

func TestLocaleRegistryMatchesEmbeddedFiles(t *testing.T) {
	files, err := fs.Glob(localeFiles, "locales/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(Supported()) {
		t.Fatalf("registered %d locales for %d embedded files", len(Supported()), len(files))
	}
	for _, file := range files {
		code := strings.TrimSuffix(strings.TrimPrefix(file, "locales/"), ".json")
		if !IsSupported(code) {
			t.Errorf("embedded locale %q is not registered", code)
		}
	}
}

func TestFormatAndPlural(t *testing.T) {
	if got := Format("en", "common.count", map[string]string{"count": "3"}); got != "Count: 3" {
		t.Fatalf("interpolated message = %q", got)
	}
	if got := Plural("en", "items", 2, nil); got != "2 items" {
		t.Fatalf("English plural = %q", got)
	}
	if got := Plural("fr", "items", 0, nil); got != "0 élément" {
		t.Fatalf("French zero plural = %q", got)
	}
	if got := Plural("ar", "items", 2, nil); got != "عنصران" {
		t.Fatalf("Arabic dual plural = %q", got)
	}
}

func TestLanguagesJSONAndPluralCategories(t *testing.T) {
	if languages := Languages(); len(languages) != len(Supported()) || len(languages) == 0 {
		t.Fatalf("Languages() returned %d entries for %d supported locales", len(languages), len(Supported()))
	}
	if data, ok := JSON("it"); !ok || len(data) == 0 {
		t.Fatal("JSON() did not return the embedded Italian locale")
	}
	if _, ok := JSON("unsupported"); ok {
		t.Fatal("JSON() accepted an unsupported locale")
	}
	for _, test := range []struct {
		language string
		count    int64
	}{
		{"ar", 0},
		{"ar", 1},
		{"ar", 2},
		{"ar", 5},
		{"ar", 15},
		{"ar", 101},
		{"fr", 0},
		{"fr", 1},
		{"it", 1},
		{"it", 1_000_000},
		{"it", 2},
		{"zh", 2},
		{"ja", 3},
		{"en", 2},
	} {
		if got := pluralCategory(test.language, test.count); got == "" {
			t.Errorf("pluralCategory(%q, %d) is empty", test.language, test.count)
		}
	}
}
