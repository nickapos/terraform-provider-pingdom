package pingdom

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/mbarper/go-pingdom/pingdom"
)

const (
	checkTypeHTTP = "http"
	checkTypeTCP  = "tcp"
	checkTypePing = "ping"
	checkTypeDNS  = "dns"
)

func resourcePingdomCheck() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourcePingdomCheckCreate,
		ReadContext:   resourcePingdomCheckRead,
		UpdateContext: resourcePingdomCheckUpdate,
		DeleteContext: resourcePingdomCheckDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"host": {
				Type:     schema.TypeString,
				Required: true,
			},
			"type": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice([]string{checkTypeHTTP, checkTypeTCP, checkTypePing, checkTypeDNS}, false),
			},
			"custom_message": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"paused": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"ipv6": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"responsetime_threshold": {
				Type:     schema.TypeInt,
				Optional: true,
				Computed: true,
			},
			"resolution": {
				Type:         schema.TypeInt,
				Optional:     true,
				ForceNew:     false,
				Default:      5,
				ValidateFunc: validation.IntInSlice([]int{1, 5, 15, 30, 60}),
			},
			"sendnotificationwhendown": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  2,
			},
			"notifyagainevery": {
				Type:     schema.TypeInt,
				Optional: true,
				Computed: true,
			},
			"notifywhenbackup": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"integrationids": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
			},
			"encryption": {
				Type:     schema.TypeBool,
				Optional: true,
				Computed: true,
			},
			"url": {
				Type:             schema.TypeString,
				Optional:         true,
				Default:          "/",
				DiffSuppressFunc: diffSuppressIfNotHTTPCheck,
			},
			"port": {
				Type:         schema.TypeInt,
				Optional:     true,
				ForceNew:     false,
				Computed:     true,
				ValidateFunc: validation.IsPortNumber,
			},
			"username": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"password": {
				Type:      schema.TypeString,
				Optional:  true,
				ForceNew:  false,
				Sensitive: true,
			},
			"shouldcontain": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"shouldnotcontain": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"postdata": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"requestheaders": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"tags": {
				Type:     schema.TypeString,
				Optional: true,
				StateFunc: func(val any) string {
					return sortString(val.(string), ",")
				},
			},
			"probefilters": {
				Type:     schema.TypeString,
				Optional: true,
				// A check may carry several filters, comma separated. Normalise
				// spacing and order so they cannot differ from what the API
				// reports back.
				StateFunc: func(val any) string {
					return normalizeProbeFilters(val.(string))
				},
			},
			"userids": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
			},
			"teamids": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
			},
			"stringtosend": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"stringtoexpect": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"expectedip": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"nameserver": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"verify_certificate": {
				Type:             schema.TypeBool,
				Optional:         true,
				Default:          true,
				DiffSuppressFunc: diffSuppressIfNotHTTPCheck,
			},
			"ssl_down_days_before": {
				Type:             schema.TypeInt,
				Optional:         true,
				Default:          0,
				DiffSuppressFunc: diffSuppressIfNotHTTPCheck,
			},
		},
	}
}

type commonCheckParams struct {
	Name                     string
	Hostname                 string
	Resolution               int
	Paused                   bool
	IPv6                     bool
	ResponseTimeThreshold    int
	SendNotificationWhenDown int
	NotifyAgainEvery         int
	NotifyWhenBackup         bool
	IntegrationIds           []int
	UserIds                  []int
	TeamIds                  []int
	URL                      string
	Encryption               bool
	Port                     int
	Username                 string
	Password                 string
	ShouldContain            string
	ShouldNotContain         string
	PostData                 string
	RequestHeaders           map[string]string
	Tags                     string
	ProbeFilters             string
	StringToSend             string
	StringToExpect           string
	ExpectedIP               string
	NameServer               string
	VerifyCertificate        bool
	SSLDownDaysBefore        int
	CustomMessage            string
}

func diffSuppressIfNotHTTPCheck(k string, old string, new string, d *schema.ResourceData) bool {
	return d.Get("type").(string) != checkTypeHTTP
}

func sortString(input string, seperator string) string {
	list := strings.Split(input, seperator)
	sort.Strings(list)
	return strings.Join(list, seperator)
}

// normalizeProbeFilters canonicalises a comma separated probe filter list, so
// that "region: NA,region:EU" and "region:EU, region: NA" both reduce to
// "region:EU,region:NA". The API echoes filters back with a space after the
// colon and in an order of its own choosing, so config and API have to be
// reduced to the same form or the check shows a permanent diff.
func normalizeProbeFilters(input string) string {
	filters := strings.Split(input, ",")
	out := make([]string, 0, len(filters))
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if filter == "" {
			continue
		}
		if key, value, ok := strings.Cut(filter, ":"); ok {
			filter = strings.TrimSpace(key) + ":" + strings.TrimSpace(value)
		}
		out = append(out, filter)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// expandIntSet converts a schema.TypeSet of ints into a slice. It returns nil
// for an empty set, which go-pingdom renders as an empty parameter.
func expandIntSet(v any) []int {
	set, ok := v.(*schema.Set)
	if !ok {
		return nil
	}
	return expandReferenceIds(set.List())
}

// checkForResource builds the API representation of the check described by d.
//
// Every field is read with d.Get rather than d.GetOk. GetOk reports ok==false
// for any zero value, so it cannot distinguish "the user did not set this" from
// "the user emptied this", and it silently relies on each zero value happening
// to be the value we want to send anyway. That holds today only because this
// struct starts zeroed; d.Get states the intent directly and keeps a field
// whose "off" value is not the zero value from breaking quietly later. The
// schema supplies every default, so the post-diff value is always the intended
// one.
func checkForResource(d *schema.ResourceData) (pingdom.Check, error) {
	checkParams := commonCheckParams{
		Name:                     d.Get("name").(string),
		Hostname:                 d.Get("host").(string),
		Paused:                   d.Get("paused").(bool),
		IPv6:                     d.Get("ipv6").(bool),
		Resolution:               d.Get("resolution").(int),
		ResponseTimeThreshold:    d.Get("responsetime_threshold").(int),
		SendNotificationWhenDown: d.Get("sendnotificationwhendown").(int),
		NotifyAgainEvery:         d.Get("notifyagainevery").(int),
		NotifyWhenBackup:         d.Get("notifywhenbackup").(bool),
		IntegrationIds:           expandIntSet(d.Get("integrationids")),
		UserIds:                  expandIntSet(d.Get("userids")),
		TeamIds:                  expandIntSet(d.Get("teamids")),
		URL:                      d.Get("url").(string),
		Encryption:               d.Get("encryption").(bool),
		Port:                     d.Get("port").(int),
		Username:                 d.Get("username").(string),
		Password:                 d.Get("password").(string),
		ShouldContain:            d.Get("shouldcontain").(string),
		ShouldNotContain:         d.Get("shouldnotcontain").(string),
		PostData:                 d.Get("postdata").(string),
		RequestHeaders:           map[string]string{},
		// Sort alphabetically so the order written in the config cannot differ
		// from the order the API reports back.
		Tags:              sortString(d.Get("tags").(string), ","),
		ProbeFilters:      normalizeProbeFilters(d.Get("probefilters").(string)),
		StringToSend:      d.Get("stringtosend").(string),
		StringToExpect:    d.Get("stringtoexpect").(string),
		ExpectedIP:        d.Get("expectedip").(string),
		NameServer:        d.Get("nameserver").(string),
		VerifyCertificate: d.Get("verify_certificate").(bool),
		SSLDownDaysBefore: d.Get("ssl_down_days_before").(int),
		CustomMessage:     d.Get("custom_message").(string),
	}

	for k, v := range d.Get("requestheaders").(map[string]any) {
		checkParams.RequestHeaders[k] = v.(string)
	}

	checkType := d.Get("type")
	switch checkType {
	case checkTypeHTTP:
		return httpCheck{&pingdom.HttpCheck{
			Name:                     checkParams.Name,
			Hostname:                 checkParams.Hostname,
			Resolution:               checkParams.Resolution,
			Paused:                   checkParams.Paused,
			IPV6:                     checkParams.IPv6,
			ResponseTimeThreshold:    checkParams.ResponseTimeThreshold,
			SendNotificationWhenDown: checkParams.SendNotificationWhenDown,
			NotifyAgainEvery:         checkParams.NotifyAgainEvery,
			NotifyWhenBackup:         checkParams.NotifyWhenBackup,
			IntegrationIds:           checkParams.IntegrationIds,
			Encryption:               checkParams.Encryption,
			Url:                      checkParams.URL,
			Port:                     checkParams.Port,
			Username:                 checkParams.Username,
			Password:                 checkParams.Password,
			ShouldContain:            checkParams.ShouldContain,
			ShouldNotContain:         checkParams.ShouldNotContain,
			PostData:                 checkParams.PostData,
			RequestHeaders:           checkParams.RequestHeaders,
			Tags:                     checkParams.Tags,
			ProbeFilters:             checkParams.ProbeFilters,
			UserIds:                  checkParams.UserIds,
			TeamIds:                  checkParams.TeamIds,
			VerifyCertificate:        &checkParams.VerifyCertificate,
			SSLDownDaysBefore:        &checkParams.SSLDownDaysBefore,
			CustomMessage:            checkParams.CustomMessage,
		}}, nil
	case checkTypePing:
		return &pingdom.PingCheck{
			Name:                     checkParams.Name,
			Hostname:                 checkParams.Hostname,
			Resolution:               checkParams.Resolution,
			Paused:                   checkParams.Paused,
			ResponseTimeThreshold:    checkParams.ResponseTimeThreshold,
			SendNotificationWhenDown: checkParams.SendNotificationWhenDown,
			NotifyAgainEvery:         checkParams.NotifyAgainEvery,
			NotifyWhenBackup:         checkParams.NotifyWhenBackup,
			IntegrationIds:           checkParams.IntegrationIds,
			Tags:                     checkParams.Tags,
			ProbeFilters:             checkParams.ProbeFilters,
			UserIds:                  checkParams.UserIds,
			TeamIds:                  checkParams.TeamIds,
		}, nil
	case checkTypeTCP:
		return tcpCheck{&pingdom.TCPCheck{
			Name:                     checkParams.Name,
			Hostname:                 checkParams.Hostname,
			Resolution:               checkParams.Resolution,
			Paused:                   checkParams.Paused,
			IPV6:                     checkParams.IPv6,
			ResponseTimeThreshold:    checkParams.ResponseTimeThreshold,
			SendNotificationWhenDown: checkParams.SendNotificationWhenDown,
			NotifyAgainEvery:         checkParams.NotifyAgainEvery,
			NotifyWhenBackup:         checkParams.NotifyWhenBackup,
			IntegrationIds:           checkParams.IntegrationIds,
			Tags:                     checkParams.Tags,
			ProbeFilters:             checkParams.ProbeFilters,
			UserIds:                  checkParams.UserIds,
			TeamIds:                  checkParams.TeamIds,
			Port:                     checkParams.Port,
			StringToSend:             checkParams.StringToSend,
			StringToExpect:           checkParams.StringToExpect,
			CustomMessage:            checkParams.CustomMessage,
		}}, nil
	case checkTypeDNS:
		return &pingdom.DNSCheck{
			Name:                     checkParams.Name,
			Hostname:                 checkParams.Hostname,
			ExpectedIP:               checkParams.ExpectedIP,
			NameServer:               checkParams.NameServer,
			Resolution:               checkParams.Resolution,
			Paused:                   checkParams.Paused,
			IPV6:                     checkParams.IPv6,
			SendNotificationWhenDown: checkParams.SendNotificationWhenDown,
			NotifyAgainEvery:         checkParams.NotifyAgainEvery,
			NotifyWhenBackup:         checkParams.NotifyWhenBackup,
			IntegrationIds:           checkParams.IntegrationIds,
			Tags:                     checkParams.Tags,
			ProbeFilters:             checkParams.ProbeFilters,
			UserIds:                  checkParams.UserIds,
			TeamIds:                  checkParams.TeamIds,
		}, nil
	default:
		return nil, fmt.Errorf("unknown type for check '%v'", checkType)
	}
}

func resourcePingdomCheckCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*Clients).Pingdom

	check, err := checkForResource(d)
	if err != nil {
		return diag.FromErr(err)
	}

	log.Printf("[DEBUG] Check create configuration: %#v, %#v", d.Get("name"), d.Get("hostname"))

	ck, err := client.Checks.Create(check)
	if err != nil {
		return diag.FromErr(err)
	}

	d.SetId(strconv.Itoa(ck.ID))

	return resourcePingdomCheckRead(ctx, d, meta)
}

func resourcePingdomCheckRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*Clients).Pingdom

	id, err := strconv.Atoi(d.Id())
	if err != nil {
		return diag.Errorf("Error retrieving id for resource: %s", err)
	}
	// Reading the check is enough to tell whether it still exists; listing every
	// check first only added an extra API call per resource per refresh.
	ck, err := readCheck(client, id)
	if err != nil {
		if isCheckGone(err) {
			d.SetId("")
			return nil
		}
		return diag.Errorf("Error retrieving check: %s", err)
	}

	if err := d.Set("host", ck.Hostname); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("name", ck.Name); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("resolution", ck.Resolution); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("responsetime_threshold", ck.ResponseTimeThreshold); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("sendnotificationwhendown", ck.SendNotificationWhenDown); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("notifyagainevery", ck.NotifyAgainEvery); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("notifywhenbackup", ck.NotifyWhenBackup); err != nil {
		return diag.FromErr(err)
	}

	tags := []string{}
	for _, tag := range ck.Tags {
		tags = append(tags, tag.Name)
	}

	// We need to sort the strings here as the pingdom API returns them sorted by
	// number of occurances across all checks
	sort.Strings(tags)
	if err := d.Set("tags", strings.Join(tags, ",")); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("custom_message", ck.CustomMessage); err != nil {
		return diag.FromErr(err)
	}

	// Paused is the actual pause flag. Status reports health, so it cannot tell
	// an unpause apart from a check that simply has not been tested yet -- but
	// it does report "paused", so honour either signal. Setting this
	// unconditionally is what lets an unpause made outside Terraform be seen.
	if err := d.Set("paused", ck.Paused || ck.Status == "paused"); err != nil {
		return diag.FromErr(err)
	}

	integids := schema.NewSet(
		func(integrationId any) int { return integrationId.(int) },
		[]any{},
	)
	for _, integrationID := range ck.IntegrationIds {
		integids.Add(integrationID)
	}
	if err := d.Set("integrationids", integids); err != nil {
		return diag.FromErr(err)
	}

	userids := schema.NewSet(
		func(userId any) int { return userId.(int) },
		[]any{},
	)
	for _, userID := range ck.UserIds {
		userids.Add(userID)
	}
	if err := d.Set("userids", userids); err != nil {
		return diag.FromErr(err)
	}

	teamids := schema.NewSet(
		func(userId any) int { return userId.(int) },
		[]any{},
	)
	for _, userID := range ck.TeamIds {
		teamids.Add(userID)
	}
	if err := d.Set("teamids", teamids); err != nil {
		return diag.FromErr(err)
	}

	// Keep every filter, not just the first: dropping the rest here meant the
	// next update PUT only the surviving one and deleted the others from the
	// check. Set unconditionally so removing filters is detected as drift.
	if err := d.Set("probefilters", normalizeProbeFilters(strings.Join(ck.ProbeFilters, ","))); err != nil {
		return diag.FromErr(err)
	}

	if ck.Type.HTTP != nil {
		if err := d.Set("type", checkTypeHTTP); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("responsetime_threshold", ck.ResponseTimeThreshold); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("url", ck.Type.HTTP.Url); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("encryption", ck.Type.HTTP.Encryption); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("port", ck.Type.HTTP.Port); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("username", ck.Type.HTTP.Username); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("password", ck.Type.HTTP.Password); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("shouldcontain", ck.Type.HTTP.ShouldContain); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("shouldnotcontain", ck.Type.HTTP.ShouldNotContain); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("postdata", ck.Type.HTTP.PostData); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("verify_certificate", ck.Type.HTTP.VerifyCertificate); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("ssl_down_days_before", ck.Type.HTTP.SSLDownDaysBefore); err != nil {
			return diag.FromErr(err)
		}

		if v, ok := ck.Type.HTTP.RequestHeaders["User-Agent"]; ok {
			if strings.HasPrefix(v, "Pingdom.com_bot_version_") {
				delete(ck.Type.HTTP.RequestHeaders, "User-Agent")
			}
		}
		if err := d.Set("requestheaders", ck.Type.HTTP.RequestHeaders); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("ipv6", ck.IPv6); err != nil {
			return diag.FromErr(err)
		}

	} else if ck.Type.TCP != nil {
		if err := d.Set("type", checkTypeTCP); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("port", ck.Type.TCP.Port); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("stringtosend", ck.Type.TCP.StringToSend); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("stringtoexpect", ck.Type.TCP.StringToExpect); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("responsetime_threshold", ck.ResponseTimeThreshold); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("ipv6", ck.IPv6); err != nil {
			return diag.FromErr(err)
		}
	} else if ck.Type.DNS != nil {
		if err := d.Set("type", checkTypeDNS); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("expectedip", ck.Type.DNS.ExpectedIP); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("nameserver", ck.Type.DNS.NameServer); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("ipv6", ck.IPv6); err != nil {
			return diag.FromErr(err)
		}
	} else {
		if err := d.Set("type", checkTypePing); err != nil {
			return diag.FromErr(err)
		}
	}

	return nil
}

func resourcePingdomCheckUpdate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*Clients).Pingdom

	id, err := strconv.Atoi(d.Id())
	if err != nil {
		return diag.Errorf("Error retrieving id for resource: %s", err)
	}

	check, err := checkForResource(d)
	if err != nil {
		return diag.FromErr(err)
	}

	_, err = client.Checks.Update(id, check)
	if err != nil {
		return diag.Errorf("Error updating check: %s", err)
	}

	return resourcePingdomCheckRead(ctx, d, meta)
}

func resourcePingdomCheckDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*Clients).Pingdom

	id, err := strconv.Atoi(d.Id())
	if err != nil {
		return diag.Errorf("Error retrieving id for resource: %s", err)
	}

	log.Printf("[INFO] Deleting Check: %v", id)

	_, err = client.Checks.Delete(id)
	if err != nil {
		return diag.Errorf("Error deleting check: %s", err)
	}

	return nil
}
