package config

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
)

// StoreNameFor returns the name handlers resolve this config namespace by.
func StoreNameFor(configName string, spec *core.MappingNode) string {
	if spec == nil {
		return configName
	}
	specNameNode, ok := pluginutils.GetValueByPath("$.name", spec)
	specName := core.StringValue(specNameNode)
	if ok && specName != "" {
		return specName
	}
	return configName
}
