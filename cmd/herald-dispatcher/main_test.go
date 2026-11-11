package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"
)

// logRecord holds the fields of a JSON log line that the tests check.
type logRecord struct {
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	Service string `json:"service"`
}

func TestRunStopsOnCancel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var stdout bytes.Buffer
		errc := make(chan error, 1)
		go func() { errc <- run(ctx, &stdout) }()

		synctest.Wait()
		select {
		case err := <-errc:
			t.Fatalf("run returned before cancellation: %v", err)
		default:
		}

		cancel()
		if err := <-errc; err != nil {
			t.Fatalf("run: %v", err)
		}

		want := []logRecord{
			{Level: "INFO", Msg: "started", Service: "herald-dispatcher"},
			{Level: "INFO", Msg: "stopped", Service: "herald-dispatcher"},
		}
		if diff := cmp.Diff(want, decodeLog(t, &stdout)); diff != "" {
			t.Errorf("log (-want +got):\n%s", diff)
		}
	})
}

// decodeLog parses r as a stream of JSON log lines and fails the test if any
// line isn't JSON.
func decodeLog(t *testing.T, r io.Reader) []logRecord {
	t.Helper()
	var records []logRecord
	dec := json.NewDecoder(r)
	for {
		var rec logRecord
		err := dec.Decode(&rec)
		if errors.Is(err, io.EOF) {
			return records
		}
		if err != nil {
			t.Fatalf("decode log line: %v", err)
		}
		records = append(records, rec)
	}
}
