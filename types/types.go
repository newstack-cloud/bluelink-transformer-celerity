package types

import (
	"github.com/newstack-cloud/bluelink/libs/blueprint/linktypes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/schema"
)

// LinkedResource pairs a resolved link edge with the target resource spec.
type LinkedResource struct {
	Name     string
	Resource *schema.Resource
	Edge     *linktypes.ResolvedLink
}
