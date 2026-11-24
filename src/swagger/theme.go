package swagger

// ThemeCSS returns local Swagger UI overrides that use the shared site theme.
func ThemeCSS() ([]byte, error) {
	return Asset("theme.css")
}
