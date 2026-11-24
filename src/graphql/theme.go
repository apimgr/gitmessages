package graphqlapi

// ThemeCSS returns local GraphiQL overrides that use the shared site theme.
func ThemeCSS() ([]byte, error) {
	return Asset("theme.css")
}
