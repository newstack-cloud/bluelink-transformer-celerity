package handler

import "github.com/newstack-cloud/bluelink-transformer-celerity/shared"

var runtimes = map[string]map[string]string{
	shared.AWSServerless: {
		shared.CelerityRuntimeNode24:    "nodejs24.x",
		shared.CelerityRuntimePython313: "python3.13",
	},
}

func getTargetRuntime(celerityRuntime string, deployTarget string) (string, bool) {
	runtime, ok := runtimes[deployTarget][celerityRuntime]
	return runtime, ok
}
