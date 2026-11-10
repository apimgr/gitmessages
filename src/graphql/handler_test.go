package graphqlapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerQueryAndMutation(t *testing.T) {
	handler := Handler()
	query := `{"query":"query { types { type example } conventions { name } }"}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewBufferString(query)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("query status = %d", recorder.Code)
	}
	var result struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []any                      `json:"errors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 || len(result.Data["types"]) == 0 || len(result.Data["conventions"]) == 0 {
		t.Fatalf("query result incomplete: %s", recorder.Body.String())
	}

	mutation := `{"query":"mutation Build($input: BuildMessageInput!) { buildMessage(input: $input) { message } }","variables":{"input":{"type":"feat","scope":"api","description":"add search"}}}`
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewBufferString(mutation)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("mutation status = %d", recorder.Code)
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("feat(api): add search")) {
		t.Fatalf("mutation response = %s", recorder.Body.String())
	}
}

func TestHandlerRejectsMalformedRequests(t *testing.T) {
	handler := Handler()
	for _, request := range []struct{ method, body string }{
		{http.MethodPost, "{"},
		{http.MethodPut, ""},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(request.method, "/graphql", bytes.NewBufferString(request.body)))
		if recorder.Code < 400 {
			t.Errorf("%s returned %d", request.method, recorder.Code)
		}
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewBufferString(`{"query":"{ types { type } }"} {}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("trailing JSON returned %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewBufferString(`{"query":"query { notAField }"}`)))
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("errors")) {
		t.Fatalf("GraphQL validation result: status %d, body %s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerLocalizesCatalog(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewBufferString(`{"query":"{ types { type description } }"}`))
	request.Header.Set("X-GitMessages-Language", "es")
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("Una nueva funcionalidad")) {
		t.Fatalf("GraphQL catalog was not localized: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
