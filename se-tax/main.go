// Command se-tax is the FloweySales plugin for Swedish tax on sales of
// securities: it turns broker transaction exports into the K4 attachment,
// with cost basis, tax estimates and the SRU files Skatteverket imports.
package main

import (
	_ "embed"
	"time"

	sdk "github.com/Doldas/floweysales/pluginsdk"
)

//go:embed plugin.json
var manifestJSON []byte

func main() {
	sdk.Main(newPlugin(&taxPlugin{now: time.Now}))
}

func newPlugin(t *taxPlugin) sdk.Plugin {
	manifest, err := sdk.ParseManifest(manifestJSON)
	if err != nil {
		panic(err)
	}
	return sdk.Plugin{Manifest: manifest, Tool: t.tool}
}
