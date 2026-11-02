package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	sdk "github.com/Doldas/floweysales/pluginsdk"
	"github.com/Doldas/floweysales/pluginsdk/plugintest"
)

// UPDATE_MANIFEST=1 go test ./se-tax regenerates plugin.json.
func TestPluginJSONMatchesGoManifest(t *testing.T) {
	want := buildManifest()
	if os.Getenv("UPDATE_MANIFEST") == "1" {
		var doc map[string]any
		full, _ := json.Marshal(want)
		_ = json.Unmarshal(full, &doc)
		delete(doc, "provider") // a tools-only plugin has no provider
		raw, _ := json.MarshalIndent(doc, "", "  ")
		if err := os.WriteFile("plugin.json", append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := sdk.ParseManifest(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	var ga, gb any
	_ = json.Unmarshal(a, &ga)
	_ = json.Unmarshal(b, &gb)
	if !reflect.DeepEqual(ga, gb) {
		t.Fatal("plugin.json is out of date; run UPDATE_MANIFEST=1 go test ./se-tax")
	}
}

func TestConformance(t *testing.T) {
	plugintest.Conformance(t, newPlugin(&taxPlugin{now: fixedNow}))
}
