package pingdom

import (
	"context"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func dataSourcePingdomCheck() *schema.Resource {
	return &schema.Resource{
		ReadContext: dataSourcePingdomCheckRead,

		Schema: map[string]*schema.Schema{
			"check_id": {
				Type:     schema.TypeInt,
				Required: true,
			},
			"name": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"host": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"type": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"status": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"paused": {
				Type:     schema.TypeBool,
				Computed: true,
			},
			"ipv6": {
				Type:     schema.TypeBool,
				Computed: true,
			},
			"resolution": {
				Type:     schema.TypeInt,
				Computed: true,
			},
			"sendnotificationwhendown": {
				Type:     schema.TypeInt,
				Computed: true,
			},
			"notifyagainevery": {
				Type:     schema.TypeInt,
				Computed: true,
			},
			"notifywhenbackup": {
				Type:     schema.TypeBool,
				Computed: true,
			},
			"responsetime_threshold": {
				Type:     schema.TypeInt,
				Computed: true,
			},
			"custom_message": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"tags": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"probefilters": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"integrationids": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
			},
			"userids": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
			},
			// teamids is derived from the teams the API reports. teams is
			// exposed alongside it, unmodified, so what the API actually returns
			// can be told apart from what the provider derives.
			"teamids": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
			},
			"teams": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"id":   {Type: schema.TypeInt, Computed: true},
						"name": {Type: schema.TypeString, Computed: true},
					},
				},
			},
		},
	}
}

func dataSourcePingdomCheckRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*Clients).Pingdom
	id := d.Get("check_id").(int)

	ck, err := readCheck(client, id)
	if err != nil {
		return diag.Errorf("Error retrieving check %d: %s", id, err)
	}

	tags := make([]string, 0, len(ck.Tags))
	for _, tag := range ck.Tags {
		tags = append(tags, tag.Name)
	}
	sort.Strings(tags)

	teams := make([]map[string]any, 0, len(ck.Teams))
	for _, team := range ck.Teams {
		teams = append(teams, map[string]any{"id": team.ID, "name": team.Name})
	}

	checkType := checkTypePing
	switch {
	case ck.Type.HTTP != nil:
		checkType = checkTypeHTTP
	case ck.Type.TCP != nil:
		checkType = checkTypeTCP
	case ck.Type.DNS != nil:
		checkType = checkTypeDNS
	}

	for attr, value := range map[string]any{
		"name":                     ck.Name,
		"host":                     ck.Hostname,
		"type":                     checkType,
		"status":                   ck.Status,
		"paused":                   ck.Paused || ck.Status == "paused",
		"ipv6":                     ck.IPv6,
		"resolution":               ck.Resolution,
		"sendnotificationwhendown": ck.SendNotificationWhenDown,
		"notifyagainevery":         ck.NotifyAgainEvery,
		"notifywhenbackup":         ck.NotifyWhenBackup,
		"responsetime_threshold":   ck.ResponseTimeThreshold,
		"custom_message":           ck.CustomMessage,
		"tags":                     tags,
		"probefilters":             ck.ProbeFilters,
		"integrationids":           ck.IntegrationIds,
		"userids":                  ck.UserIds,
		"teamids":                  ck.TeamIds,
		"teams":                    teams,
	} {
		if err := d.Set(attr, value); err != nil {
			return diag.Errorf("Error setting %s: %s", attr, err)
		}
	}

	d.SetId(strconv.Itoa(ck.ID))
	return nil
}
