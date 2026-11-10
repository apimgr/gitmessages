package i18n

import "testing"

func TestLocaleParity(t *testing.T) {
	if problems := ValidateParity(); len(problems) != 0 {
		t.Fatalf("translation key parity errors: %#v", problems)
	}
}

func TestFallbackAndDirection(t *testing.T) {
	if got := T("unsupported", "nav.home"); got != "Home" {
		t.Fatalf("unsupported locale fallback = %q", got)
	}
	if got := Get("ar").Meta.Direction; got != "rtl" {
		t.Fatalf("Arabic direction = %q", got)
	}
}
