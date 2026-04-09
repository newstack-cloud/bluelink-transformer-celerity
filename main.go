package main

import (
	"context"
	"embed"
	"fmt"
	"log"

	"github.com/newstack-cloud/bluelink-transformer-celerity/transformer"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/plugin"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/pluginservicev1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
)

//go:embed transformer_description.md
var embedded embed.FS

func main() {
	serviceClient, closeService, err := pluginservicev1.NewEnvServiceClient()
	if err != nil {
		log.Fatal(err.Error())
	}
	defer closeService()

	hostInfoContainer := pluginutils.NewHostInfoContainer()
	transformerServer := transformerv1.NewTransformerPlugin(
		transformer.NewTransformer(),
		hostInfoContainer,
		serviceClient,
	)

	transformerDescription, _ := embedded.ReadFile("transformer_description.md")
	config := plugin.ServePluginConfiguration{
		ID: "newstack-cloud/celerity",
		PluginMetadata: &pluginservicev1.PluginMetadata{
			PluginVersion:        version,
			DisplayName:          "Celerity",
			FormattedDescription: string(transformerDescription),
			RepositoryUrl:        "https://github.com/newstack-cloud/bluelink-transformer-celerity",
			Author:               "NewStack Cloud Limited",
		},
		ProtocolVersion: "1.0",
	}

	fmt.Println("Starting Bluelink Celerity Transformer Plugin Server...")
	close, err := plugin.ServeTransformerV1(
		context.Background(),
		transformerServer,
		serviceClient,
		hostInfoContainer,
		config,
	)
	if err != nil {
		log.Fatal(err.Error())
	}
	pluginutils.WaitForShutdown(close)
}
