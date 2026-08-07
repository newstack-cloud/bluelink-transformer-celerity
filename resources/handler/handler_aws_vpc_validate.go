package handler

import (
	"fmt"
	"strings"

	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
)

// Egress checks that follow from how AWS Lambda attaches to a VPC rather than
// from the abstract topology, kept apart from the portable ones for that reason.
//
// The fact they all rest on: a VPC-attached function is never assigned a public
// IPv4 address. A function in a public subnet therefore cannot use the internet
// gateway for IPv4 however the route table is written, and reaches IPv6
// destinations only. A function in a private subnet is unaffected, since it
// reaches IPv4 through the NAT gateway.
//
// Another target attaches compute differently, so none of this transfers. Cloud
// Run has no subnet-tier concept at all.
func awsServerlessEgressDiagnostics(r *ResolvedHandler) []*core.Diagnostic {
	if r.VPC == nil || r.VPCEgress == "" || r.VPCEgress == VPCEgressNone {
		return nil
	}
	if r.VPCSubnetType != SubnetTypePublic {
		return nil
	}

	if r.VPCEgress == VPCEgressInternet {
		return []*core.Diagnostic{ipv6OnlyInternetWarning(r)}
	}

	return cidrEgressDiagnostics(r)
}

// A public-subnet function still reaches the whole IPv6 internet, so asking for
// "internet" is honoured in part rather than refused. Warning rather than erroring
// keeps a working IPv6 deployment deployable, while telling the author the thing
// they would otherwise discover as a connection timeout to an IPv4-only third
// party.
func ipv6OnlyInternetWarning(r *ResolvedHandler) *core.Diagnostic {
	return &core.Diagnostic{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/handler %q requests %s of %q from a %q subnet, but a VPC-attached function "+
				"has no public IPv4 address, so it will reach IPv6 destinations only; an IPv4-only "+
				"destination will time out at runtime. Move the handler to a %q subnet in a VPC "+
				"with a NAT gateway to reach IPv4",
			r.Name, AnnotationKeyVPCEgress, r.VPCEgress, SubnetTypePublic, SubnetTypePrivate,
		),
		Range: diagnosticRangeFor(r),
	}
}

// An explicit destination list from a public subnet is judged on what it would
// actually open. The IPv4 entries cannot be reached and are dropped at deploy
// time, so a list with no IPv6 entries produces no rule at all: the author asked
// for something specific and would silently get nothing.
func cidrEgressDiagnostics(r *ResolvedHandler) []*core.Diagnostic {
	ipv4, ipv6 := splitEgressCIDRs(r.VPCEgress)
	if len(ipv4) == 0 {
		return nil
	}

	if len(ipv6) == 0 {
		return []*core.Diagnostic{{
			Level: core.DiagnosticLevelError,
			Message: fmt.Sprintf(
				"celerity/handler %q requests %s of %q from a %q subnet, but a VPC-attached "+
					"function has no public IPv4 address, so none of those destinations can be "+
					"reached and no outbound rule would be created; move the handler to a %q subnet "+
					"in a VPC with a NAT gateway, or name IPv6 destinations",
				r.Name, AnnotationKeyVPCEgress, r.VPCEgress, SubnetTypePublic, SubnetTypePrivate,
			),
			Range: diagnosticRangeFor(r),
		}}
	}

	// Some of the list is reachable, so the deployment does something useful and
	// only the IPv4 entries are inert.
	return []*core.Diagnostic{{
		Level: core.DiagnosticLevelWarning,
		Message: fmt.Sprintf(
			"celerity/handler %q requests %s including the IPv4 destination(s) %s from a %q "+
				"subnet, but a VPC-attached function has no public IPv4 address, so only the IPv6 "+
				"destinations will be opened",
			r.Name, AnnotationKeyVPCEgress, strings.Join(ipv4, ", "), SubnetTypePublic,
		),
		Range: diagnosticRangeFor(r),
	}}
}

// Splits a destination list by address family, ignoring entries that are not
// CIDRs at all: the deploy target rejects those with a message naming the accepted
// forms, and duplicating that judgement here risks the two disagreeing.
func splitEgressCIDRs(declared string) (ipv4 []string, ipv6 []string) {
	for part := range strings.SplitSeq(declared, ",") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "/") {
			continue
		}
		if strings.Contains(part, ":") {
			ipv6 = append(ipv6, part)
			continue
		}
		ipv4 = append(ipv4, part)
	}

	return ipv4, ipv6
}
