package tenantapi_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// TestRESTResponses pins the REST/JSON responses, field names and formats
// included, which the protos alone don't show.
func TestRESTResponses(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var application tenantv1.CreateApplicationResponse
	setup.mustInvoke(t, a, key, createApplication("Customer A"), &application)
	applicationID := application.GetApplication().GetId()
	var ep tenantv1.CreateEndpointResponse
	setup.mustInvoke(t, a, key, createEndpoint(applicationID, "https://example.com/a", nil), &ep)
	epID := ep.GetEndpoint().GetId()

	// One normalizer for every response, so that an ID gets the same
	// placeholder in every golden file.
	n := newNormalizer()
	n.placeholder(applicationID)
	n.placeholder(epID)

	tests := []struct {
		name   string
		noAuth bool // make the call without an API key
		call   call
	}{
		{name: "create_application", call: createApplication("Customer B")},
		{name: "get_application", call: getApplication(applicationID)},
		{name: "list_applications", call: listApplications(1, "")},
		{name: "create_endpoint", call: createEndpoint(applicationID, "https://example.com/b", []string{"invoice.*", "customer.>"})},
		{name: "get_endpoint", call: getEndpoint(applicationID, epID)},
		{name: "list_endpoints", call: listEndpoints(applicationID, 0, "")},
		{name: "unauthenticated", noAuth: true, call: getApplication(applicationID)},
		{name: "not_found", call: getApplication("0193a1b2-0000-7000-8000-000000000000")},
		{name: "invalid_argument", call: createEndpoint(applicationID, "https://example.com/b", []string{"invoice..paid"})},
		{name: "create_message", call: createMessage(applicationID, "invoice.paid", payload, "order-1")},
		{name: "payload_too_large", call: createMessage(applicationID, "invoice.paid", `"`+strings.Repeat("a", 256<<10)+`"`, "")},
	}
	// Not parallel: the calls create resources, and the placeholders are
	// numbered in the order the responses are read.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := "Bearer " + key
			if tt.noAuth {
				auth = ""
			}
			_, body := doREST(t, a, auth, tt.call)
			checkGolden(t, tt.name, n.normalize(t, body))
		})
	}
}

// normalizer replaces the values in responses that differ on every run
// (IDs, timestamps, signing secrets, page tokens) with placeholders, and
// indents the JSON.
type normalizer struct {
	placeholders map[string]string
	ids          int
}

func newNormalizer() *normalizer {
	return &normalizer{placeholders: make(map[string]string)}
}

// placeholder returns the placeholder for the ID id, numbering IDs in the
// order they are first seen.
func (n *normalizer) placeholder(id string) string {
	if p, ok := n.placeholders[id]; ok {
		return p
	}
	n.ids++
	p := fmt.Sprintf("ID_%d", n.ids)
	n.placeholders[id] = p
	return p
}

func (n *normalizer) normalize(t *testing.T, body []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, body)
	}
	replacements := make(map[string]string)
	n.collect(v, replacements)
	s := string(body)
	for value, placeholder := range replacements {
		s = strings.ReplaceAll(s, jsonString(value), jsonString(placeholder))
	}
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(s), "", "  "); err != nil {
		t.Fatalf("indent response: %v", err)
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// collect finds the varying values in v and adds them to replacements.
func (n *normalizer) collect(v any, replacements map[string]string) {
	switch v := v.(type) {
	case map[string]any:
		for k, field := range v {
			s, ok := field.(string)
			switch {
			case !ok || s == "":
				n.collect(field, replacements)
			case k == "id" || k == "application_id":
				replacements[s] = n.placeholder(s)
			case k == "created_at":
				replacements[s] = "TIMESTAMP"
			case k == "signing_secret":
				replacements[s] = "SIGNING_SECRET"
			case k == "next_page_token":
				replacements[s] = "PAGE_TOKEN"
			}
		}
	case []any:
		for _, e := range v {
			n.collect(e, replacements)
		}
	}
}

// checkGolden compares got with testdata/<name>.golden, or rewrites the file
// with -update.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("%s (-want +got):\n%s", path, diff)
	}
}
