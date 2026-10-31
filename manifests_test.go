package plugins

import (
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/Doldas/floweysales/pluginsdk"
)

var official = []string{"yahoo", "binance", "finnhub", "avanza"}

func TestManifestsMatchDirectoriesAndPolicy(t *testing.T) {
	providers := map[string]string{}
	for _, dir := range official {
		raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
		if err != nil {
			t.Fatal(err)
		}
		m, err := sdk.ParseManifest(raw)
		if err != nil {
			t.Fatalf("%s/plugin.json: %v", dir, err)
		}
		if m.ID != dir {
			t.Errorf("%s/plugin.json declares id %q", dir, m.ID)
		}
		if other, dup := providers[m.Provider.ID]; dup {
			t.Errorf("%s and %s both provide %s", dir, other, m.Provider.ID)
		}
		providers[m.Provider.ID] = dir
		if m.Provider.MinPollSeconds < 2 {
			t.Errorf("%s polls faster than 2s", dir)
		}
		if !m.Provider.OfficialAPI && m.Terms == "" {
			t.Errorf("%s uses unofficial endpoints but has no install terms", dir)
		}
	}
}
