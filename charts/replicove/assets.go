// Package operatorchart embeds the installable chart in the Replicove CLI.
package operatorchart

import "embed"

//go:embed Chart.yaml values.yaml templates/*.yaml crds/*.yaml files/snapshot-crds/*.yaml
var Files embed.FS
