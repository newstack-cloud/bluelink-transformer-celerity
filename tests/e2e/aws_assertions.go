//go:build e2e

package e2e

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

const envVarsResolvedTimeout = 2 * time.Minute

// Reads each named deployed Lambda's
// configuration and fails if any environment variable value is empty or still
// carries an unresolved "${" substitution placeholder. It polls because Lambda
// configuration reads can briefly lag deploy-time updates (including link-
// maintained variables such as the config-store IDs).
func assertAllFunctionEnvVarsResolved(t *testing.T, h *Harness, functionNames ...string) {
	t.Helper()
	client := lambda.NewFromConfig(h.AWSConfig)
	for _, functionName := range functionNames {
		assertFunctionEnvVarsResolved(t, h, client, functionName)
	}
}

func assertFunctionEnvVarsResolved(
	t *testing.T,
	h *Harness,
	client *lambda.Client,
	functionName string,
) {
	t.Helper()
	lastOffenders := ""
	waitFor(t, envVarsResolvedTimeout, 5*time.Second,
		fmt.Sprintf("all env vars on function %s to be non-empty and resolved", functionName),
		func() (bool, error) {
			vars, err := functionEnvVars(h, client, functionName)
			if err != nil {
				return false, err
			}
			offenders := unresolvedEnvVars(vars)
			if summary := strings.Join(offenders, ", "); summary != lastOffenders {
				lastOffenders = summary
				if summary != "" {
					t.Logf("function %s has unresolved env vars: %s", functionName, summary)
				}
			}
			return len(offenders) == 0, nil
		})
}

// Fetches a deployed Lambda's environment variables by
// function name, returning an empty map when the function has no environment.
func functionEnvVars(
	h *Harness,
	client *lambda.Client,
	functionName string,
) (map[string]string, error) {
	out, err := client.GetFunctionConfiguration(h.Ctx, &lambda.GetFunctionConfigurationInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil {
		return nil, fmt.Errorf("get function configuration for %s: %w", functionName, err)
	}
	if out.Environment == nil {
		return map[string]string{}, nil
	}
	return out.Environment.Variables, nil
}

// Returns a sorted "NAME=value" list of env vars whose value
// is empty or still contains a "${" substitution marker (a value the deploy
// pipeline failed to resolve).
func unresolvedEnvVars(vars map[string]string) []string {
	var offenders []string
	for name, value := range vars {
		if value == "" || strings.Contains(value, "${") {
			offenders = append(offenders, fmt.Sprintf("%s=%q", name, value))
		}
	}
	sort.Strings(offenders)
	return offenders
}
