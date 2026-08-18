package pingdom

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// Pingdom can accept a check update, answer 200, and not apply part of it. The
// only symptom is the same diff reappearing on the next plan, while Terraform
// reports "Modifications complete" -- so the cause looks like Terraform drift
// rather than the API declining to store something.
//
// After an update the read path refreshes these attributes from the API, so
// comparing what was requested against what comes back identifies exactly what
// was dropped, and a warning diagnostic reports it without failing the apply.

// unappliedCandidates are attributes the read path refreshes from the API and
// whose values map one-to-one onto what was requested.
//
// `tags` is deliberately excluded: Pingdom adds its own automatic tags, which
// the read path cannot distinguish from user tags, so comparing them would
// produce false warnings.
var unappliedCandidates = []string{
	"teamids",
	"userids",
	"integrationids",
	"probefilters",
	"custom_message",
}

// unappliedSetting is a value Terraform asked for that the check does not report
// back after a successful update.
type unappliedSetting struct {
	attr      string
	requested string
	reported  string
}

// snapshotRequested records the values being asked for. It must be called before
// the read that follows an update, since that read replaces them with whatever
// the API reports.
func snapshotRequested(d *schema.ResourceData) map[string]string {
	out := make(map[string]string, len(unappliedCandidates))
	for _, attr := range unappliedCandidates {
		out[attr] = formatAttrValue(d.Get(attr))
	}
	return out
}

// unappliedSettings compares a snapshot against the refreshed resource data.
func unappliedSettings(requested map[string]string, d *schema.ResourceData) []unappliedSetting {
	var out []unappliedSetting
	for _, attr := range unappliedCandidates {
		reported := formatAttrValue(d.Get(attr))
		if requested[attr] != reported {
			out = append(out, unappliedSetting{
				attr:      attr,
				requested: requested[attr],
				reported:  reported,
			})
		}
	}
	return out
}

// formatAttrValue renders an attribute for comparison and display. Sets are
// sorted so ordering cannot produce a spurious mismatch.
func formatAttrValue(v any) string {
	switch t := v.(type) {
	case *schema.Set:
		ids := make([]int, 0, t.Len())
		for _, item := range t.List() {
			if n, ok := item.(int); ok {
				ids = append(ids, n)
			}
		}
		sort.Ints(ids)
		return fmt.Sprint(ids)
	case string:
		return fmt.Sprintf("%q", t)
	default:
		return fmt.Sprint(v)
	}
}

// warnUnappliedSettings turns the comparison into a warning. It never fails the
// apply: the update did succeed, and the check may be usable without whatever
// was dropped.
func warnUnappliedSettings(id int, settings []unappliedSetting) diag.Diagnostics {
	if len(settings) == 0 {
		return nil
	}

	var detail strings.Builder
	detail.WriteString("Pingdom accepted the update but the check does not report " +
		"these values, so Terraform will plan them again on the next run:\n")
	for _, s := range settings {
		fmt.Fprintf(&detail, "\n  %s: requested %s, check reports %s",
			s.attr, s.requested, s.reported)
	}
	detail.WriteString("\n\nCommon causes: the referenced team, user or integration ID " +
		"does not exist or is not visible to this API token; or the account plan " +
		"does not include the feature.")

	return diag.Diagnostics{{
		Severity: diag.Warning,
		Summary:  fmt.Sprintf("Pingdom did not apply all requested settings to check %d", id),
		Detail:   detail.String(),
	}}
}
