package tenantapi_test

import (
	"bytes"
	"encoding/base64"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
	"github.com/madskrogh/herald/internal/ingest/ingestdb"
)

// The one test that reads a table directly: how the signing secret is stored
// is the property under test, and the API deliberately can't show it.
func TestSigningSecretIsStoredOnlyEncrypted(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var app tenantv1.CreateApplicationResponse
	transports[0].mustInvoke(t, a, key, createApplication("Customer A"), &app)
	var created tenantv1.CreateEndpointResponse
	transports[0].mustInvoke(t, a, key, createEndpoint(app.GetApplication().GetId(), "https://example.com/webhooks", nil), &created)
	secret := created.GetSigningSecret()
	endpointID := uuid.MustParse(created.GetEndpoint().GetId())

	t.Run("not in plain", func(t *testing.T) {
		t.Parallel()

		// Positive control: an absence check also passes when it can't see
		// the secret, so first make sure it fires on rows that hold one in
		// plain, in each form it could be stored in.
		const planted = "whsec_UExBTlRFRFBMQU5URURQTEFOVEVEUExB" // base64 of "PLANTEDPLANTEDPLANTEDPLA"
		for _, stored := range [][]byte{[]byte(planted), []byte("UExBTlRFRFBMQU5URURQTEFOVEVEUExB"), []byte("PLANTEDPLANTEDPLANTEDPLA")} {
			tx, err := a.pool.Begin(t.Context())
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			err = ingestdb.New(tx).CreateSigningSecret(t.Context(), ingestdb.CreateSigningSecretParams{
				EndpointID: endpointID,
				Ciphertext: stored,
			})
			if err != nil {
				t.Fatalf("plant a signing secret: %v", err)
			}
			found := slices.ContainsFunc(readSigningSecretRows(t, tx), func(row signingSecretRow) bool { return row.holdsSecret(planted) })
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatalf("roll back the planted signing secret: %v", err)
			}
			if !found {
				t.Fatalf("the check misses a signing secret stored in plain as %q", stored)
			}
		}
		// No signing_secrets column is text, so this row can't come from
		// Postgres.
		if !(signingSecretRow{Text: "(UExBTlRFRFBMQU5URURQTEFOVEVEUExB)"}).holdsSecret(planted) {
			t.Fatal("the check misses a signing secret stored in plain as text")
		}

		rows := readSigningSecretRows(t, a.pool)
		if len(rows) != 1 {
			t.Fatalf("got %d signing_secrets rows, want 1", len(rows))
		}
		if rows[0].holdsSecret(secret) {
			t.Errorf("signing_secrets row %s contains the signing secret", rows[0].Text)
		}
	})

	t.Run("decrypts to the secret returned", func(t *testing.T) {
		t.Parallel()
		rows := readSigningSecretRows(t, a.pool)
		if len(rows) != 1 {
			t.Fatalf("got %d signing_secrets rows, want 1", len(rows))
		}
		if rows[0].EndpointID != endpointID {
			t.Errorf("signing secret belongs to Endpoint %s, want %s", rows[0].EndpointID, endpointID)
		}
		plaintext, err := a.keyring.Decrypt(rows[0].Ciphertext, []byte(endpointID.String()))
		if err != nil {
			t.Fatalf("decrypt with the Endpoint ID as associated data: %v", err)
		}
		if string(plaintext) != secret {
			t.Errorf("stored signing secret decrypts to %q, want the one returned, %q", plaintext, secret)
		}
	})
}

// signingSecretRow is a signing_secrets row as the tests read it: the whole
// row in its text form, which shows bytea as hex, and the columns the tests
// check.
type signingSecretRow struct {
	Text       string
	EndpointID uuid.UUID
	Ciphertext []byte
}

// holdsSecret reports whether row contains secret in plain: as the whole
// secret, its base64 part alone, or the key bytes that part encodes.
func (row signingSecretRow) holdsSecret(secret string) bool {
	encoded := strings.TrimPrefix(secret, "whsec_")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		panic(err) // Signing secrets are always base64 after the prefix.
	}
	return strings.Contains(row.Text, encoded) ||
		bytes.Contains(row.Ciphertext, []byte(encoded)) ||
		bytes.Contains(row.Ciphertext, raw)
}

// readSigningSecretRows returns the signing_secrets rows that db can see. The
// query lives here, not in sqlc: it reads what herald-api never needs to.
func readSigningSecretRows(t *testing.T, db ingestdb.DBTX) []signingSecretRow {
	t.Helper()
	rows, err := db.Query(t.Context(), "SELECT s::text, s.endpoint_id, s.ciphertext FROM ingest.signing_secrets s")
	if err != nil {
		t.Fatalf("query signing_secrets: %v", err)
	}
	stored, err := pgx.CollectRows(rows, pgx.RowToStructByPos[signingSecretRow])
	if err != nil {
		t.Fatalf("read signing_secrets: %v", err)
	}
	return stored
}
