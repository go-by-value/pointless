// Package plugin registers pointless as a golangci-lint module plugin.
//
// Reference it from .custom-gcl.yml:
//
//	plugins:
//	  - module: github.com/go-by-value/pointless
//	    import: github.com/go-by-value/pointless/plugin
//	    version: v0.1.0
//
// Settings are optional; omitted keys keep their defaults:
//
//	linters:
//	  settings:
//	    custom:
//	      pointless:
//	        type: module
//	        settings:
//	          threshold: 128
//	          receivers: method
//	          returns: true
//	          slices: false
package plugin

import (
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"

	"github.com/go-by-value/pointless"
)

func init() {
	register.Plugin("pointless", New)
}

// settings mirrors pointless.Settings with optional fields, so that keys
// absent from the configuration keep their defaults.
type settings struct {
	Threshold *int    `json:"threshold"`
	Receivers *string `json:"receivers"`
	Returns   *bool   `json:"returns"`
	Slices    *bool   `json:"slices"`
}

type plugin struct {
	settings pointless.Settings
}

var _ register.LinterPlugin = (*plugin)(nil)

// New returns the pointless plugin configured with conf, a map decoded from
// the golangci-lint configuration.
func New(conf any) (register.LinterPlugin, error) {
	s, err := register.DecodeSettings[settings](conf)
	if err != nil {
		return nil, err
	}

	cfg := pointless.DefaultSettings()
	if s.Threshold != nil {
		cfg.Threshold = *s.Threshold
	}
	if s.Receivers != nil {
		cfg.Receivers = *s.Receivers
	}
	if s.Returns != nil {
		cfg.Returns = *s.Returns
	}
	if s.Slices != nil {
		cfg.Slices = *s.Slices
	}

	return plugin{settings: cfg}, nil
}

func (p plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{pointless.NewAnalyzer(p.settings)}, nil
}

func (plugin) GetLoadMode() string {
	return register.LoadModeTypesInfo
}
