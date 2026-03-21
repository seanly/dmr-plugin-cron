// dmr-plugin-cron is an external DMR plugin that runs scheduled jobs via HostService.RunAgent.
package main

import (
	goplugin "github.com/hashicorp/go-plugin"
	"github.com/seanly/dmr/pkg/plugin/proto"
)

func main() {
	impl := NewCronPlugin()
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: proto.Handshake,
		Plugins: map[string]goplugin.Plugin{
			"dmr-plugin": &proto.DMRPlugin{Impl: impl},
		},
	})
}
