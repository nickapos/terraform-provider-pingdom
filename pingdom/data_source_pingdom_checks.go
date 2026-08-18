package pingdom

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func dataSourcePingdomChecks() *schema.Resource {
	return &schema.Resource{
		ReadContext: dataSourcePingdomChecksRead,

		Schema: map[string]*schema.Schema{
			"checks": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"check_id": {Type: schema.TypeInt, Computed: true},
						"name":     {Type: schema.TypeString, Computed: true},
						"type":     {Type: schema.TypeString, Computed: true},
						"status":   {Type: schema.TypeString, Computed: true},
						"paused":   {Type: schema.TypeBool, Computed: true},
						"tags": {
							Type:     schema.TypeList,
							Computed: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
						"teamids": {
							Type:     schema.TypeList,
							Computed: true,
							Elem:     &schema.Schema{Type: schema.TypeInt},
						},
						"userids": {
							Type:     schema.TypeList,
							Computed: true,
							Elem:     &schema.Schema{Type: schema.TypeInt},
						},
						"integrationids": {
							Type:     schema.TypeList,
							Computed: true,
							Elem:     &schema.Schema{Type: schema.TypeInt},
						},
					},
				},
			},
		},
	}
}

func dataSourcePingdomChecksRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*Clients).Pingdom

	checks, err := client.Checks.List()
	if err != nil {
		return diag.Errorf("Error retrieving checks: %s", err)
	}

	out := make([]map[string]any, 0, len(checks))
	for _, ck := range checks {
		tags := make([]string, 0, len(ck.Tags))
		for _, tag := range ck.Tags {
			tags = append(tags, tag.Name)
		}
		sort.Strings(tags)

		// Checks.List does not backfill TeamIds from teams the way Read does,
		// so derive it here -- while keeping any teamids the response supplied
		// directly, which is the same distinction readCheck preserves.
		teamIDs := ck.TeamIds
		if len(ck.Teams) > 0 {
			teamIDs = make([]int, 0, len(ck.Teams))
			for _, team := range ck.Teams {
				teamIDs = append(teamIDs, team.ID)
			}
		}

		checkType := ck.Type.Name
		if checkType == "" {
			checkType = checkTypePing
		}

		out = append(out, map[string]any{
			"check_id":       ck.ID,
			"name":           ck.Name,
			"type":           checkType,
			"status":         ck.Status,
			"paused":         ck.Paused || ck.Status == "paused",
			"tags":           tags,
			"teamids":        teamIDs,
			"userids":        ck.UserIds,
			"integrationids": ck.IntegrationIds,
		})
	}

	if err := d.Set("checks", out); err != nil {
		return diag.Errorf("Error setting checks: %s", err)
	}

	d.SetId(strconv.FormatInt(time.Now().Unix(), 10))
	return nil
}
