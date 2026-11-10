package i18n

import (
	"embed"
	"encoding/json"
	"io/fs"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFiles embed.FS

type Locale struct {
	Meta struct {
		Language   string `json:"language"`
		Name       string `json:"name"`
		NativeName string `json:"native_name"`
		Direction  string `json:"direction"`
		Version    string `json:"version"`
	} `json:"meta"`
	Strings map[string]string `json:"strings"`
}

var supported = []string{"en", "es", "zh", "fr", "ar", "de", "ja"}
var localeCache sync.Map

// Supported returns the locale identifiers in stable display order.
func Supported() []string { return append([]string(nil), supported...) }

// IsSupported reports whether code is one of the embedded locales.
func IsSupported(code string) bool {
	for _, supportedCode := range supported {
		if code == supportedCode {
			return true
		}
	}
	return false
}

// Get loads an embedded locale, falling back to English for invalid codes.
func Get(code string) Locale {
	if !IsSupported(code) {
		code = "en"
	}
	if cached, ok := localeCache.Load(code); ok {
		return cached.(Locale)
	}
	data, err := fs.ReadFile(localeFiles, "locales/"+code+".json")
	if err != nil {
		data, _ = fs.ReadFile(localeFiles, "locales/en.json")
	}
	var locale Locale
	if json.Unmarshal(data, &locale) != nil {
		return Locale{Strings: map[string]string{}}
	}
	localeCache.Store(code, locale)
	return locale
}

// T returns a localized string, falling back to English and then the key.
func T(code, key string) string {
	if value, ok := Get(code).Strings[key]; ok {
		return value
	}
	if value, ok := Get("en").Strings[key]; ok {
		return value
	}
	return strings.TrimSpace(key)
}

// ValidateParity reports locale keys that differ from English.
func ValidateParity() map[string][]string {
	english := Get("en").Strings
	problems := make(map[string][]string)
	for _, code := range supported[1:] {
		locale := Get(code).Strings
		for key := range english {
			if _, ok := locale[key]; !ok {
				problems[code] = append(problems[code], "missing:"+key)
			}
		}
		for key := range locale {
			if _, ok := english[key]; !ok {
				problems[code] = append(problems[code], "extra:"+key)
			}
		}
	}
	return problems
}
