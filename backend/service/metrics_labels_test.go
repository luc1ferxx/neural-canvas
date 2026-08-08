package service

import (
	"testing"

	"github.com/luc1ferxx/neural-canvas/backend/config"
	"github.com/luc1ferxx/neural-canvas/backend/metrics"
)

// TestProviderLabelsMatchTheConfigValues pins two constants in different packages
// to the same string.
//
// metrics cannot import config -- config is loaded before anything else and must
// not pull in the rest of the program -- so the provider names are declared twice.
// That is a drift hazard with no compiler protection: if someone renamed
// config.ProviderStub to "local", the call sites here would start emitting
// provider="local" while metrics.Init kept pre-creating provider="stub". The
// result would be a series permanently at zero next to a series no dashboard was
// built against, and nothing anywhere would fail. This test is the only thing
// standing between that rename and a silently wrong metric.
func TestProviderLabelsMatchTheConfigValues(t *testing.T) {
	for _, c := range []struct {
		name   string
		config string
		label  string
	}{
		{"openai", config.ProviderOpenAI, metrics.ProviderLabelOpenAI},
		{"stub", config.ProviderStub, metrics.ProviderLabelStub},
	} {
		if c.config != c.label {
			t.Errorf("%s: config value %q but metrics label %q; metrics.Init pre-creates "+
				"a series the call sites will never write to",
				c.name, c.config, c.label)
		}
	}
}
