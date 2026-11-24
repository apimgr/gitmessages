package graphqlapi

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"

	"github.com/apimgr/gitmessages/src/common/i18n"
	gql "github.com/graphql-go/graphql"
)

//go:embed assets/*
var uiAssets embed.FS

// Asset returns a bundled GraphiQL browser asset by its local file name.
func Asset(name string) ([]byte, error) {
	if path.Base(name) != name || name == "." || name == ".." {
		return nil, fs.ErrNotExist
	}
	return uiAssets.ReadFile("assets/" + name)
}

// Handler executes validated GraphQL operations against the commit catalog.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
			OperationName string         `json:"operationName"`
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeTransportError(w, r, http.StatusMethodNotAllowed, i18n.T(language(r), "graphql.error.method"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		mediaType, _, parseErr := mime.ParseMediaType(contentType)
		if parseErr != nil {
			writeTransportError(w, r, http.StatusUnsupportedMediaType, i18n.T(language(r), "graphql.error.invalid_body"))
			return
		}
		switch mediaType {
		case "application/json":
			decoder := json.NewDecoder(r.Body)
			if err := decoder.Decode(&request); err != nil {
				writeTransportError(w, r, http.StatusBadRequest, i18n.T(language(r), "graphql.error.invalid_body"))
				return
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				writeTransportError(w, r, http.StatusBadRequest, i18n.T(language(r), "graphql.error.multiple"))
				return
			}
		case "application/x-www-form-urlencoded":
			if err := r.ParseForm(); err != nil {
				writeTransportError(w, r, http.StatusBadRequest, i18n.T(language(r), "graphql.error.invalid_body"))
				return
			}
			request.Query = r.PostForm.Get("query")
		default:
			writeTransportError(w, r, http.StatusUnsupportedMediaType, i18n.T(language(r), "graphql.error.invalid_body"))
			return
		}
		if request.Query == "" {
			writeTransportError(w, r, http.StatusBadRequest, i18n.T(language(r), "graphql.error.required"))
			return
		}
		lang := language(r)
		result := gql.Do(gql.Params{Schema: Schema(lang), RequestString: request.Query, VariableValues: request.Variables, OperationName: request.OperationName, Context: WithLanguage(r.Context(), lang)})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(result)
	})
}

func language(r *http.Request) string {
	return r.Header.Get("X-GitMessages-Language")
}

func writeTransportError(w http.ResponseWriter, r *http.Request, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": http.StatusText(status), "message": message, "details": map[string]any{}})
}
