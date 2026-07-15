package aws

import (
	"fmt"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/validation"
)

func validateAssumeRoleDuration(
	key string,
	value *core.ScalarValue,
	pluginConfig core.PluginConfig,
) []*core.Diagnostic {
	stringVal := core.StringValueFromScalar(value)
	duration, err := time.ParseDuration(stringVal)
	if err != nil {
		return []*core.Diagnostic{
			{
				Level: core.DiagnosticLevelError,
				Message: fmt.Sprintf(
					"Invalid duration %q for field %q: %s",
					stringVal, key, err.Error(),
				),
			},
		}
	}

	if duration.Minutes() < 15 || duration.Hours() > 12 {
		return []*core.Diagnostic{
			{
				Level: core.DiagnosticLevelError,
				Message: fmt.Sprintf(
					"Duration %q for field %q must be between 15 minutes and 12 hours",
					stringVal, key,
				),
			},
		}
	}

	return []*core.Diagnostic{}
}

var partitionRegexp = regexp.MustCompile(`^aws(-[a-z]+)*$`)
var regionRegexp = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-\d{1,2}$`)
var accountIDRegexp = regexp.MustCompile(
	`^(aws|aws-managed|third-party|aws-marketplace|\d{12}|cw.{10})$`,
)

func validateARN(
	key string,
	value *core.ScalarValue,
	pluginConfig core.PluginConfig,
) []*core.Diagnostic {
	diagnostics := []*core.Diagnostic{}

	stringVal := core.StringValueFromScalar(value)
	parsedARN, err := arn.Parse(stringVal)
	if err != nil {
		return []*core.Diagnostic{
			{
				Level: core.DiagnosticLevelError,
				Message: fmt.Sprintf(
					"Invalid ARN %q for field %q: %s",
					stringVal, key, err.Error(),
				),
			},
		}
	}

	if parsedARN.Partition == "" {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelError,
			Message: fmt.Sprintf(
				"ARN %q for field %q is missing a partition value",
				stringVal,
				key,
			),
		})
	} else if !partitionRegexp.MatchString(parsedARN.Partition) {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelError,
			Message: fmt.Sprintf(
				"ARN %q for field %q has an invalid partition value: %s",
				stringVal, key, parsedARN.Partition,
			),
		})
	}

	if parsedARN.Region != "" && !regionRegexp.MatchString(parsedARN.Region) {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelError,
			Message: fmt.Sprintf(
				"ARN %q for field %q has an invalid region value: %s",
				stringVal, key, parsedARN.Region,
			),
		})
	}

	if parsedARN.AccountID != "" &&
		!accountIDRegexp.MatchString(parsedARN.AccountID) {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelError,
			Message: fmt.Sprintf(
				"ARN %q for field %q has an invalid account ID: %s",
				stringVal, key, parsedARN.AccountID,
			),
		})
	}

	if parsedARN.Resource == "" {
		diagnostics = append(diagnostics, &core.Diagnostic{
			Level: core.DiagnosticLevelError,
			Message: fmt.Sprintf(
				"ARN %q for field %q is missing a resource value",
				stringVal, key,
			),
		})
	}

	return diagnostics
}

var validateAssumeRoleSessionName = validation.WrapForPluginConfig(
	validation.AllOf(
		validation.StringLengthRange(2, 64),
		validation.StringMatchesPattern(
			regexp.MustCompile(`[\w+=,.@\-]*`),
		),
	),
)
