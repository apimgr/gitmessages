package main

import "github.com/apimgr/gitmessages/src/common/i18n"

func localizeClientError(language string, err error) string {
	if err == nil {
		return ""
	}
	for _, candidate := range []struct {
		text string
		key  string
	}{
		{"unknown commit type", "builder.error.unknown_type"},
		{"description must be a non-empty single line", "builder.error.description_required"},
		{"description must be 100 characters or fewer", "builder.error.description_too_long"},
		{"scope must be 1–50 letters, numbers, dots, slashes, underscores, or hyphens", "builder.error.scope_invalid"},
		{"body must be 5000 characters or fewer", "builder.error.body_too_long"},
		{"unknown commit convention", "builder.error.unknown_convention"},
	} {
		if err.Error() == candidate.text {
			return i18n.T(language, candidate.key)
		}
	}
	return i18n.Format(language, "gui.response_error", map[string]string{"error": err.Error()})
}
