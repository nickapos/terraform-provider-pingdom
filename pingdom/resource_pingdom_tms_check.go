package pingdom

import (
	"context"
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/mbarper/go-pingdom/pingdom"
)

func resourcePingdomTmsCheck() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourcePingdomTmsCheckCreate,
		ReadContext:   resourcePingdomTmsCheckRead,
		UpdateContext: resourcePingdomTmsCheckUpdate,
		DeleteContext: resourcePingdomTmsCheckDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"steps": {
				Type:     schema.TypeList,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"fn": {
							Type:     schema.TypeString,
							Required: true,
						},
						"args": {
							Type:     schema.TypeMap,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
					},
				},
			},
			"active": {
				Type:     schema.TypeBool,
				Optional: true,
				// TMSCheck.Active is serialised unconditionally, so without a
				// default an omitted `active` sent false on every create and
				// update and deactivated the check.
				Default: true,
			},
			"contact_ids": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
			},
			"custom_message": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"integration_ids": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
			},
			"interval": {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      10,
				ValidateFunc: validation.IntInSlice([]int{5, 10, 20, 60, 720, 1440}),
			},
			"metadata": {
				Type:     schema.TypeList,
				MinItems: 0,
				MaxItems: 1,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"authentication": {
							Type:     schema.TypeMap,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
						"disable_websecurity": {
							Type:     schema.TypeBool,
							Optional: true,
						},
						"height": {
							Type:     schema.TypeInt,
							Optional: true,
						},
						"width": {
							Type:     schema.TypeInt,
							Optional: true,
						},
					},
				},
			},
			"region": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "us-east",
				ValidateFunc: validation.StringInSlice([]string{"us-east", "us-west", "eu", "au"}, false),
			},
			"send_notification_when_down": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  1,
			},
			"security_level": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "high",
				ValidateFunc: validation.StringInSlice([]string{"high", "low"}, false),
			},
			"tags": {
				Type:     schema.TypeString,
				Optional: true,
				StateFunc: func(val any) string {
					return sortString(val.(string), ",")
				},
			},
			"team_ids": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
			},
		},
	}
}

func convertInterfaceMapToStringMap(m map[string]any) map[string]string {
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = v.(string)
	}
	return result
}

func convertIntSliceToTypeSet(l []int) *schema.Set {
	result := schema.NewSet(
		func(integrationId any) int { return integrationId.(int) },
		[]any{},
	)
	for _, item := range l {
		result.Add(item)
	}
	return result
}

func expandTmsCheckSteps(l []any) []pingdom.TMSCheckStep {
	if len(l) == 0 || l[0] == nil {
		return nil
	}

	steps := make([]pingdom.TMSCheckStep, 0, len(l))
	for _, tfMapRaw := range l {
		tfMap, ok := tfMapRaw.(map[string]any)
		if !ok {
			continue
		}
		step := pingdom.TMSCheckStep{}
		if args, ok := tfMap["args"].(map[string]any); ok {
			step.Args = convertInterfaceMapToStringMap(args)
		}
		if fn, ok := tfMap["fn"].(string); ok && fn != "" {
			step.Fn = fn
		}
		steps = append(steps, step)
	}

	return steps
}

func expandTmsMetadata(m map[string]any) *pingdom.TMSCheckMetaData {
	metadata := pingdom.TMSCheckMetaData{}

	if v, ok := m["authentication"]; ok {
		metadata.Authentications = v
	}

	if v, ok := m["disable_websecurity"]; ok {
		metadata.DisableWebSecurity = v.(bool)
	}

	if v, ok := m["height"]; ok {
		metadata.Height = v.(int)
	}

	if v, ok := m["width"]; ok {
		metadata.Width = v.(int)
	}

	return &metadata
}

func expandReferenceIds(l []any) []int {
	var intSlice []int
	for i := range l {
		intSlice = append(intSlice, l[i].(int))
	}
	return intSlice
}

func toTmsCheck(d *schema.ResourceData) (*pingdom.TMSCheck, error) {
	tmsCheck := pingdom.TMSCheck{}

	// required
	if v, ok := d.GetOk("name"); ok {
		tmsCheck.Name = v.(string)
	}

	// required
	if v, ok := d.GetOk("steps"); ok {
		interfaceSlice := v.([]any)
		tmsCheck.Steps = expandTmsCheckSteps(interfaceSlice)
	}

	// d.Get, not d.GetOk: `active` is serialised without omitempty, so an
	// explicit false has to reach the API rather than be read as "unset".
	tmsCheck.Active = d.Get("active").(bool)

	if v, ok := d.GetOk("contact_ids"); ok {
		interfaceSlice := v.(*schema.Set).List()
		tmsCheck.ContactIDs = expandReferenceIds(interfaceSlice)
	}

	if v, ok := d.GetOk("custom_message"); ok {
		tmsCheck.CustomMessage = v.(string)
	}

	if v, ok := d.GetOk("integration_ids"); ok {
		interfaceSlice := v.(*schema.Set).List()
		tmsCheck.IntegrationIDs = expandReferenceIds(interfaceSlice)
	}

	if v, ok := d.GetOk("interval"); ok {
		tmsCheck.Interval = int64(v.(int))
	}

	if v, ok := d.GetOk("metadata"); ok {
		interfaceList := v.([]any)
		tmsCheck.Metadata = expandTmsMetadata((interfaceList[0]).(map[string]any))
	}

	if v, ok := d.GetOk("region"); ok {
		tmsCheck.Region = v.(string)
	}

	if v, ok := d.GetOk("send_notification_when_down"); ok {
		tmsCheck.SendNotificationWhenDown = v.(int)
	}

	if v, ok := d.GetOk("security_level"); ok {
		tmsCheck.SeverityLevel = v.(string)
	}

	if v, ok := d.GetOk("tags"); ok {
		list := strings.Split(v.(string), ",")
		sort.Strings(list)
		tmsCheck.Tags = list
	}

	if v, ok := d.GetOk("team_ids"); ok {
		interfaceSlice := v.(*schema.Set).List()
		tmsCheck.TeamIDs = expandReferenceIds(interfaceSlice)
	}

	return &tmsCheck, nil
}

func resourcePingdomTmsCheckCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*Clients).Pingdom

	check, err := toTmsCheck(d)
	if err != nil {
		return diag.FromErr(err)
	}

	log.Printf("[DEBUG] TMS Check create configuration: %#v, %#v", d.Get("name"), d.Get("steps"))

	ck, err := client.TMSCheck.Create(check)
	if err != nil {
		return diag.FromErr(err)
	}

	d.SetId(strconv.Itoa(ck.ID))

	return resourcePingdomTmsCheckRead(ctx, d, meta)
}

func resourcePingdomTmsCheckRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*Clients).Pingdom

	id, err := strconv.Atoi(d.Id())
	if err != nil {
		return diag.Errorf("Error retrieving id for TMS check: %s", err)
	}
	// Reading the check is enough to tell whether it still exists; listing every
	// check first only added an extra API call per resource per refresh.
	ck, err := client.TMSCheck.Read(id)
	if err != nil {
		if isCheckGone(err) {
			d.SetId("")
			return nil
		}
		return diag.Errorf("Error retrieving TMS check: %s", err)
	}

	if err := d.Set("name", ck.Name); err != nil {
		return diag.FromErr(err)
	}

	steps := make([]map[string]any, 0, len(ck.Steps))
	for _, stepObj := range ck.Steps {
		step := map[string]any{}
		step["args"] = stepObj.Args
		step["fn"] = stepObj.Fn
		steps = append(steps, step)
	}

	var metadata []map[string]any
	if ck.Metadata != nil {
		m := map[string]any{}
		for k, v := range map[string]any{
			"authentication":      ck.Metadata.Authentications,
			"disable_websecurity": ck.Metadata.DisableWebSecurity,
			"height":              ck.Metadata.Height,
			"width":               ck.Metadata.Width,
		} {
			m[k] = v
		}
		metadata = append(metadata, m)
	}

	// We need to sort the strings here as the pingdom API returns them sorted by
	// number of occurances across all checks
	sort.Strings(ck.Tags)

	for k, v := range map[string]any{
		"name":                        ck.Name,
		"steps":                       steps,
		"active":                      ck.Active,
		"contact_ids":                 convertIntSliceToTypeSet(ck.ContactIDs),
		"custom_message":              ck.CustomMessage,
		"integration_ids":             convertIntSliceToTypeSet(ck.IntegrationIDs),
		"interval":                    ck.Interval,
		"metadata":                    metadata,
		"region":                      ck.Region,
		"send_notification_when_down": ck.SendNotificationWhenDown,
		"security_level":              ck.SeverityLevel,
		"tags":                        strings.Join(ck.Tags, ","),
		"team_ids":                    convertIntSliceToTypeSet(ck.TeamIDs),
	} {
		if err := d.Set(k, v); err != nil {
			return diag.FromErr(err)
		}
	}

	return nil
}

func resourcePingdomTmsCheckUpdate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*Clients).Pingdom

	id, err := strconv.Atoi(d.Id())
	if err != nil {
		return diag.Errorf("Error retrieving id for resource: %s", err)
	}

	check, err := toTmsCheck(d)
	if err != nil {
		return diag.FromErr(err)
	}

	_, err = client.TMSCheck.Update(id, check)
	if err != nil {
		return diag.Errorf("Error updating TMS check: %s", err)
	}

	return resourcePingdomTmsCheckRead(ctx, d, meta)
}

func resourcePingdomTmsCheckDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*Clients).Pingdom

	id, err := strconv.Atoi(d.Id())
	if err != nil {
		return diag.Errorf("Error retrieving id for resource: %s", err)
	}

	log.Printf("[INFO] Deleting TMS Check: %v", id)

	_, err = client.TMSCheck.Delete(id)
	if err != nil {
		return diag.Errorf("Error deleting TMS check: %s", err)
	}

	return nil
}
