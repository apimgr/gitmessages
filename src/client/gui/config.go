package gui

type Config struct {
	Server             string
	Language           string
	ValidateServer     func(string) bool
	DiscoverAPIVersion func(string, string) (string, error)
	Request            func(string, string, string, []byte, string) ([]byte, error)
	SaveProfile        func(string, string) error
	LocalizeError      func(error) string
}
