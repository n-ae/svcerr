package zerologadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	svcerrors "github.com/n-ae/svcerr"
	"github.com/rs/zerolog"
)

func TestLog(t *testing.T) {
	tests := []struct {
		name      string
		level     svcerrors.Level
		wantLevel string
	}{
		{"info", svcerrors.LevelInfo, "info"},
		{"warn", svcerrors.LevelWarn, "warn"},
		{"error", svcerrors.LevelError, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			adapter := New(zerolog.New(&buf))

			adapter.Log(tt.level, errors.New("boom"), map[string]interface{}{"resource_id": "123"}, "something failed")

			var got map[string]interface{}
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("output is not valid JSON: %v (output: %s)", err, buf.String())
			}

			if got["level"] != tt.wantLevel {
				t.Errorf("level = %v, want %v", got["level"], tt.wantLevel)
			}
			if got["error"] != "boom" {
				t.Errorf("error = %v, want boom", got["error"])
			}
			if got["resource_id"] != "123" {
				t.Errorf("resource_id = %v, want 123", got["resource_id"])
			}
			if got["message"] != "something failed" {
				t.Errorf("message = %v, want %q", got["message"], "something failed")
			}
		})
	}
}

func TestLogNilError(t *testing.T) {
	var buf bytes.Buffer
	adapter := New(zerolog.New(&buf))

	adapter.Log(svcerrors.LevelInfo, nil, nil, "no error here")

	var got map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (output: %s)", err, buf.String())
	}

	if _, ok := got["error"]; ok {
		t.Errorf("expected no error field when err is nil, got %v", got["error"])
	}
	if got["message"] != "no error here" {
		t.Errorf("message = %v, want %q", got["message"], "no error here")
	}
}

func TestLogUnknownLevelDefaultsToInfo(t *testing.T) {
	var buf bytes.Buffer
	adapter := New(zerolog.New(&buf))

	adapter.Log(svcerrors.Level(99), nil, nil, "msg")

	var got map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (output: %s)", err, buf.String())
	}
	if got["level"] != "info" {
		t.Errorf("level = %v, want info (unrecognized level should default to Info)", got["level"])
	}
}
