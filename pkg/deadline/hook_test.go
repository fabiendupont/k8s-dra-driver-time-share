package deadline

import (
	"strings"
	"testing"
)

func TestReadOCIState(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantPID int
		wantErr bool
	}{
		{
			name:    "valid state",
			input:   `{"ociVersion":"1.0.2","id":"abc123","status":"creating","pid":12345,"bundle":"/run/containers/abc"}`,
			wantPID: 12345,
		},
		{
			name:    "minimal state",
			input:   `{"pid":42}`,
			wantPID: 42,
		},
		{
			name:    "invalid json",
			input:   `not json`,
			wantErr: true,
		},
		{
			name:    "no pid",
			input:   `{"id":"test","status":"creating"}`,
			wantPID: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := readOCIState(strings.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if state.PID != tt.wantPID {
				t.Errorf("PID = %d, want %d", state.PID, tt.wantPID)
			}
		})
	}
}
