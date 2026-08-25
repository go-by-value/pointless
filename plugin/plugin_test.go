package plugin_test

import (
	"flag"
	"strconv"
	"testing"

	"github.com/golangci/plugin-module-register/register"

	"github.com/go-by-value/pointless"
	_ "github.com/go-by-value/pointless/plugin"
)

func TestRegistered(t *testing.T) {
	t.Parallel()

	newPlugin, err := register.GetPlugin("pointless")
	if err != nil {
		t.Fatalf("GetPlugin: %v", err)
	}

	p, err := newPlugin(nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}

	analyzers, err := p.BuildAnalyzers()
	if err != nil {
		t.Fatalf("BuildAnalyzers: %v", err)
	}
	if len(analyzers) != 1 || analyzers[0].Name != "pointless" {
		t.Fatalf("BuildAnalyzers: got %v, want one analyzer named pointless", analyzers)
	}

	if got := p.GetLoadMode(); got != register.LoadModeTypesInfo {
		t.Fatalf("GetLoadMode: got %q, want %q", got, register.LoadModeTypesInfo)
	}
}

func TestSettings(t *testing.T) {
	t.Parallel()

	newPlugin, err := register.GetPlugin("pointless")
	if err != nil {
		t.Fatalf("GetPlugin: %v", err)
	}

	tests := map[string]struct {
		conf any
		want pointless.Settings
	}{
		"nil keeps defaults": {
			conf: nil,
			want: pointless.DefaultSettings(),
		},
		"omitted keys keep defaults": {
			conf: map[string]any{"threshold": 128},
			want: pointless.Settings{Threshold: 128, Receivers: pointless.ReceiversType, Returns: true, Slices: false},
		},
		"all keys": {
			conf: map[string]any{"threshold": 64, "receivers": "method", "returns": false, "slices": true},
			want: pointless.Settings{Threshold: 64, Receivers: pointless.ReceiversMethod, Returns: false, Slices: true},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := newPlugin(tt.conf)
			if err != nil {
				t.Fatalf("newPlugin: %v", err)
			}
			analyzers, err := p.BuildAnalyzers()
			if err != nil {
				t.Fatalf("BuildAnalyzers: %v", err)
			}

			got := flagSettings(t, &analyzers[0].Flags)
			if got != tt.want {
				t.Fatalf("settings: got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// flagSettings reads the analyzer settings back through its flags, which
// NewAnalyzer binds to the settings it was given.
func flagSettings(t *testing.T, fs *flag.FlagSet) pointless.Settings {
	t.Helper()

	return pointless.Settings{
		Threshold: atoi(t, fs.Lookup("threshold").Value.String()),
		Receivers: fs.Lookup("receivers").Value.String(),
		Returns:   fs.Lookup("returns").Value.String() == "true",
		Slices:    fs.Lookup("slices").Value.String() == "true",
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()

	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("Atoi(%q): %v", s, err)
	}

	return n
}
