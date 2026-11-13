package config

import "testing"

func TestProductNameKeepsCustomTitlesAndNormalizesLegacyBrand(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"", "xFacade"},
		{"Facade", "xFacade"},
		{"facade", "xFacade"},
		{"xFacade", "xFacade"},
		{"My Workspace", "My Workspace"},
	} {
		config := &Config{App: AppConfig{Name: test.input}}
		normalizeConfig(config)
		if config.App.Name != test.want {
			t.Errorf("name %q: got %q, want %q", test.input, config.App.Name, test.want)
		}
	}
}
