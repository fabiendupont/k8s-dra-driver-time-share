package deadline

import (
	"strings"
	"testing"
)

func TestRunCDIHookArgValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing runtime-ns and period-ns",
			args:    []string{"--core=0"},
			wantErr: "--runtime-ns and --period-ns are required",
		},
		{
			name:    "missing period-ns",
			args:    []string{"--core=0", "--runtime-ns=1000000"},
			wantErr: "--runtime-ns and --period-ns are required",
		},
		{
			name:    "invalid core value",
			args:    []string{"--core=notanumber", "--runtime-ns=1000000", "--period-ns=4000000"},
			wantErr: "invalid argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunCDIHook(tt.args)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

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
