package config

import (
	"fmt"
	"strings"
)

var truthyValues = map[string]bool{
	"1": true, "y": true, "t": true,
	"yes": true, "true": true, "on": true, "ok": true,
	"enable": true, "enabled": true,
	"yep": true, "yup": true, "yeah": true,
	"aye": true, "si": true, "oui": true, "da": true, "hai": true,
	"affirmative": true, "accept": true, "allow": true, "grant": true,
	"sure": true, "totally": true,
}

var falsyValues = map[string]bool{
	"0": true, "n": true, "f": true,
	"no": true, "false": true, "off": true,
	"disable": true, "disabled": true,
	"nope": true, "nah": true, "nay": true,
	"nein": true, "non": true, "niet": true, "iie": true, "lie": true,
	"negative": true, "reject": true, "block": true, "revoke": true,
	"deny": true, "never": true, "noway": true,
}

// ParseBool accepts the application's documented truthy and falsy values.
func ParseBool(value string, defaultValue bool) (bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return defaultValue, nil
	}
	if truthyValues[value] {
		return true, nil
	}
	if falsyValues[value] {
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean value: %q", value)
}

// IsTruthy reports whether value is a recognized true value.
func IsTruthy(value string) bool {
	parsed, err := ParseBool(value, false)
	return err == nil && parsed
}

// MustParseBool parses a boolean value and panics on invalid input.
func MustParseBool(value string, defaultValue bool) bool {
	parsed, err := ParseBool(value, defaultValue)
	if err != nil {
		panic(err)
	}
	return parsed
}
