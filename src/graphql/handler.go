package graphqlapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/apimgr/gitmessages/src/common/i18n"
	gql "github.com/graphql-go/graphql"
)

// Handler executes validated GraphQL operations against the commit catalog.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
			OperationName string         `json:"operationName"`
		}
		switch r.Method {
		case http.MethodGet:
			request.Query = r.URL.Query().Get("query")
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
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
		default:
			w.Header().Set("Allow", "GET, POST")
			writeTransportError(w, r, http.StatusMethodNotAllowed, i18n.T(language(r), "graphql.error.method"))
			return
		}
		if request.Query == "" {
			writeTransportError(w, r, http.StatusBadRequest, i18n.T(language(r), "graphql.error.required"))
			return
		}
		result := gql.Do(gql.Params{Schema: schema, RequestString: request.Query, VariableValues: request.Variables, OperationName: request.OperationName, Context: WithLanguage(r.Context(), language(r))})
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
