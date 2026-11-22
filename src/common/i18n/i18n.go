package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFiles embed.FS

type Locale struct {
	Meta struct {
		Language         string   `json:"language"`
		Name             string   `json:"name"`
		NativeName       string   `json:"native_name"`
		Direction        string   `json:"direction"`
		Version          string   `json:"version"`
		PluralCategories []string `json:"plural_categories"`
	} `json:"meta"`
	Strings map[string]string            `json:"strings"`
	Plurals map[string]map[string]string `json:"plurals"`
}

var (
	localeCache  sync.Map
	registryOnce sync.Once
	registry     []string
)

// Supported returns the language codes whose complete locale files are embedded.
func Supported() []string {
	registryOnce.Do(loadRegistry)
	return append([]string(nil), registry...)
}

// Languages returns locale metadata in stable language-code order.
func Languages() []Locale {
	codes := Supported()
	languages := make([]Locale, 0, len(codes))
	for _, code := range codes {
		languages = append(languages, Get(code))
	}
	return languages
}

func loadRegistry() {
	files, err := fs.Glob(localeFiles, "locales/*.json")
	if err != nil {
		return
	}
	for _, file := range files {
		data, err := fs.ReadFile(localeFiles, file)
		if err != nil {
			continue
		}
		var locale Locale
		if json.Unmarshal(data, &locale) != nil || locale.Meta.Language == "" || locale.Meta.Language != strings.TrimSuffix(strings.TrimPrefix(file, "locales/"), ".json") {
			continue
		}
		if locale.Meta.Direction != "ltr" && locale.Meta.Direction != "rtl" {
			continue
		}
		registry = append(registry, locale.Meta.Language)
	}
	sort.Strings(registry)
}

// IsSupported reports whether code has a valid embedded locale file.
func IsSupported(code string) bool {
	for _, supportedCode := range Supported() {
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
		return Locale{Strings: map[string]string{}, Plurals: map[string]map[string]string{}}
	}
	localeCache.Store(code, locale)
	return locale
}

// JSON returns the original embedded locale document for browser translation loading.
func JSON(code string) ([]byte, bool) {
	if !IsSupported(code) {
		return nil, false
	}
	data, err := fs.ReadFile(localeFiles, "locales/"+code+".json")
	return data, err == nil
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

// Format translates key and substitutes named {placeholder} values.
func Format(code, key string, values map[string]string) string {
	message := T(code, key)
	for name, value := range values {
		message = strings.ReplaceAll(message, "{"+name+"}", value)
	}
	return message
}

// Plural selects a CLDR category and formats the corresponding plural message.
func Plural(code, key string, count int64, values map[string]string) string {
	locale := Get(code)
	forms, ok := locale.Plurals[key]
	if !ok {
		forms, ok = Get("en").Plurals[key]
	}
	if !ok {
		return Format(code, key, values)
	}
	category := pluralCategory(locale.Meta.Language, count)
	message, ok := forms[category]
	if !ok {
		message = forms["other"]
	}
	interpolation := make(map[string]string, len(values)+1)
	for name, value := range values {
		interpolation[name] = value
	}
	interpolation["count"] = fmt.Sprint(count)
	for name, value := range interpolation {
		message = strings.ReplaceAll(message, "{"+name+"}", value)
	}
	return message
}

func pluralCategory(language string, count int64) string {
	switch language {
	case "ar":
		switch {
		case count == 0:
			return "zero"
		case count == 1:
			return "one"
		case count == 2:
			return "two"
		case count%100 >= 3 && count%100 <= 10:
			return "few"
		case count%100 >= 11 && count%100 <= 99:
			return "many"
		default:
			return "other"
		}
	case "fr":
		if count == 0 || count == 1 {
			return "one"
		}
	case "it":
		if count == 1 {
			return "one"
		}
		if count != 0 && count%1_000_000 == 0 {
			return "many"
		}
	default:
		if count == 1 {
			return "one"
		}
	}
	return "other"
}

// ValidateParity reports locale keys, placeholders, and plural categories that differ from English.
func ValidateParity() map[string][]string {
	english := Get("en")
	problems := make(map[string][]string)
	for _, code := range Supported() {
		locale := Get(code)
		for _, category := range locale.Meta.PluralCategories {
			if category == "" {
				problems[code] = append(problems[code], "empty_plural_category")
			}
		}
		for key := range english.Strings {
			value, ok := locale.Strings[key]
			if !ok {
				problems[code] = append(problems[code], "missing:"+key)
				continue
			}
			if !samePlaceholders(english.Strings[key], value) {
				problems[code] = append(problems[code], "placeholders:"+key)
			}
			if strings.TrimSpace(value) == "" {
				problems[code] = append(problems[code], "empty:"+key)
			}
		}
		for key := range locale.Strings {
			if _, ok := english.Strings[key]; !ok {
				problems[code] = append(problems[code], "extra:"+key)
			}
		}
		for key, englishForms := range english.Plurals {
			forms, ok := locale.Plurals[key]
			if !ok {
				problems[code] = append(problems[code], "missing_plural:"+key)
				continue
			}
			for _, category := range locale.Meta.PluralCategories {
				value, exists := forms[category]
				if !exists || strings.TrimSpace(value) == "" {
					problems[code] = append(problems[code], "plural_category:"+key+":"+category)
					continue
				}
				if !sameNonCountPlaceholders(englishForms["other"], value) {
					problems[code] = append(problems[code], "plural_placeholders:"+key+":"+category)
				}
			}
			for category := range forms {
				if !contains(locale.Meta.PluralCategories, category) && category != "zero" {
					problems[code] = append(problems[code], "orphan_plural_category:"+key+":"+category)
				}
			}
		}
		for key := range locale.Plurals {
			if _, ok := english.Plurals[key]; !ok {
				problems[code] = append(problems[code], "extra_plural:"+key)
			}
		}
	}
	return problems
}

func samePlaceholders(first, second string) bool {
	return strings.Join(placeholders(first), "\x00") == strings.Join(placeholders(second), "\x00")
}

func sameNonCountPlaceholders(first, second string) bool {
	filtered := func(value string) []string {
		all := placeholders(value)
		result := all[:0]
		for _, name := range all {
			if name != "count" {
				result = append(result, name)
			}
		}
		return result
	}
	return strings.Join(filtered(first), "\x00") == strings.Join(filtered(second), "\x00")
}

func placeholders(value string) []string {
	var names []string
	for value != "" {
		start := strings.IndexByte(value, '{')
		if start < 0 {
			break
		}
		end := strings.IndexByte(value[start+1:], '}')
		if end < 0 {
			break
		}
		names = append(names, value[start+1:start+1+end])
		value = value[start+2+end:]
	}
	sort.Strings(names)
	return names
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
