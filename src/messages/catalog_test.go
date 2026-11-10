package messages

import "testing"

func TestCatalogs(t *testing.T) {
	if got := len(Types()); got != 11 {
		t.Fatalf("Types() count = %d, want 11", got)
	}
	if got := len(Conventions()); got != 2 {
		t.Fatalf("Conventions() count = %d, want 2", got)
	}
	if got := len(Templates()); got != 5 {
		t.Fatalf("Templates() count = %d, want 5", got)
	}
	if item, ok := Type("feat"); !ok || item.Emoji == "" || item.Example == "" {
		t.Fatal("feat catalog entry is incomplete")
	}
	if _, ok := TemplateByName("hotfix"); !ok {
		t.Fatal("hotfix template not found")
	}
}

func TestLocalizedCatalogs(t *testing.T) {
	if got := TypesForLocale("es")[0].Description; got != "Una nueva funcionalidad" {
		t.Fatalf("Spanish feature description = %q", got)
	}
	if got := ConventionsForLocale("fr")[0].Description; got != "Messages structurés avec type, portée facultative et description concise." {
		t.Fatalf("French convention description = %q", got)
	}
	if got := TemplatesForLocale("ja")[0].Name; got != "機能" {
		t.Fatalf("Japanese template name = %q", got)
	}
	if got := TypesForLocale("unsupported")[0].Description; got != Types()[0].Description {
		t.Fatalf("unsupported locale fallback = %q", got)
	}
}

func TestBuild(t *testing.T) {
	got, err := Build(BuildInput{Type: " feat ", Scope: " api ", Description: " add search ", Body: "supports filters", Convention: "conventional-commits"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "feat(api): add search\n\nsupports filters"; got != want {
		t.Fatalf("Build() = %q, want %q", got, want)
	}

	got, err = Build(BuildInput{Type: "fix", Description: "handle nil", Breaking: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := "fix!: handle nil\n\nBREAKING CHANGE: handle nil"; got != want {
		t.Fatalf("Build() = %q, want %q", got, want)
	}
}

func TestBuildRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		input BuildInput
	}{
		{"unknown type", BuildInput{Type: "unknown", Description: "valid"}},
		{"missing description", BuildInput{Type: "feat"}},
		{"multiline description", BuildInput{Type: "feat", Description: "first\nsecond"}},
		{"long description", BuildInput{Type: "feat", Description: string(make([]byte, 101))}},
		{"invalid scope", BuildInput{Type: "feat", Scope: "../escape", Description: "valid"}},
		{"long body", BuildInput{Type: "feat", Description: "valid", Body: string(make([]byte, 5001))}},
		{"unknown convention", BuildInput{Type: "feat", Description: "valid", Convention: "unknown"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Build(tc.input); err == nil {
				t.Fatal("Build() error = nil")
			}
		})
	}
}
