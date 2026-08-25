package pointless_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/go-by-value/pointless"
)

func TestDefaults(t *testing.T) {
	t.Parallel()

	analysistest.Run(t, analysistest.TestData(), pointless.NewAnalyzer(pointless.DefaultSettings()), "a", "b")
}

func TestReceiversPerMethod(t *testing.T) {
	t.Parallel()

	s := pointless.DefaultSettings()
	s.Receivers = pointless.ReceiversMethod
	s.Returns = false
	analysistest.Run(t, analysistest.TestData(), pointless.NewAnalyzer(s), "method")
}

func TestSlices(t *testing.T) {
	t.Parallel()

	s := pointless.DefaultSettings()
	s.Slices = true
	s.Returns = false
	analysistest.Run(t, analysistest.TestData(), pointless.NewAnalyzer(s), "ptrslices")
}

func TestThreshold(t *testing.T) {
	t.Parallel()

	s := pointless.DefaultSettings()
	s.Threshold = 16
	analysistest.Run(t, analysistest.TestData(), pointless.NewAnalyzer(s), "threshold")
}

func TestInvalidReceivers(t *testing.T) {
	t.Parallel()

	s := pointless.DefaultSettings()
	s.Receivers = "bogus"
	a := pointless.NewAnalyzer(s)

	results := analysistest.Run(&recordingT{}, analysistest.TestData(), a, "a")
	if len(results) == 0 || results[0].Err == nil {
		t.Fatal("expected an error for an invalid receivers setting")
	}
}

// recordingT swallows failures so that a test can assert on results.
type recordingT struct{}

func (recordingT) Errorf(string, ...any) {}
