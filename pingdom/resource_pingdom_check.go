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

		// Surface the type-specific requirements at plan time rather than
		// part-way through an apply.
		CustomizeDiff: validateCheckForType,

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

// validateCheckForType mirrors the per-type validation go-pingdom performs in
// Check.Valid(). That runs inside Create and Update, so on its own it only
// reports a problem part-way through an apply -- naming a Go struct field
// (`ExpectedIP`) rather than the attribute the user wrote, and, on update,
// after the SDK has already recorded the intended values in state. Running the
// same checks as a CustomizeDiff moves them to plan time, where the error names
// the resource and the attribute and nothing has been written yet.
//
// The conditions are kept identical to Valid() so this can only reject what the
// API layer would reject anyway.
func validateCheckForType(_ context.Context, d *schema.ResourceDiff, _ any) error {
	// A value interpolated from a resource that does not exist yet is unknown
	// during plan and reads as the zero value. Treating that as "empty" would
	// reject a perfectly good configuration, so unknown attributes are skipped
	// here; Update revalidates once the real value is available.
	return validateCheckValues(collectCheckValues(d.Get, func(attr string) bool {
		return !d.NewValueKnown(attr)
	}))
}

func checkValuesFromResourceData(d *schema.ResourceData) checkValues {
	// By apply time every value is known.
	return collectCheckValues(d.Get, func(string) bool { return false })
}

// checkValues is the subset of a check that Valid() inspects. Both
// *schema.ResourceDiff (plan time) and *schema.ResourceData (apply time)
// implement Get, but not through a shared interface, so the values are
// collected into this struct and validated in one place.
type checkValues struct {
	checkType        string
	name             string
	hostname         string
	expectedIP       string
	nameServer       string
	port             int
	shouldContain    string
	shouldNotContain string
	// unknown reports attributes whose value is not yet known, which must not
	// be reported as missing.
	unknown map[string]bool
}

func collectCheckValues(get func(string) any, isUnknown func(string) bool) checkValues {
	v := checkValues{
		checkType:        get("type").(string),
		name:             get("name").(string),
		hostname:         get("host").(string),
		expectedIP:       get("expectedip").(string),
		nameServer:       get("nameserver").(string),
		port:             get("port").(int),
		shouldContain:    get("shouldcontain").(string),
		shouldNotContain: get("shouldnotcontain").(string),
		unknown:          map[string]bool{},
	}
	for _, attr := range []string{
		"type", "name", "host", "expectedip", "nameserver",
		"port", "shouldcontain", "shouldnotcontain",
	} {
		if isUnknown(attr) {
			v.unknown[attr] = true
		}
	}
	return v
}

// validateCheckValues mirrors the validation go-pingdom performs in
// Check.Valid(), which otherwise only runs once Create or Update is already
// under way -- reporting a Go struct field name rather than the attribute the
// user wrote, and, on update, after the SDK has recorded the intended values in
// state. The conditions are kept identical to Valid() so this can only reject
// what the API layer would reject anyway.
func validateCheckValues(v checkValues) error {
	// validCommonParameters rejects these for every check type. Note that
	// Required in the schema only means "present", so an interpolated empty
	// string reaches the API unless it is caught here.
	for attr, value := range map[string]string{"name": v.name, "host": v.hostname} {
		if !v.unknown[attr] && value == "" {
			return fmt.Errorf("%q must not be empty", attr)
		}
	}

	switch v.checkType {
	case checkTypeDNS:
		for attr, value := range map[string]string{"expectedip": v.expectedIP, "nameserver": v.nameServer} {
			if !v.unknown[attr] && value == "" {
				return fmt.Errorf("%q is required for %q checks and must not be empty", attr, checkTypeDNS)
			}
		}
	case checkTypeTCP:
		if !v.unknown["port"] && (v.port < 1 || v.port > 65535) {
			return fmt.Errorf("%q is required for %q checks and must be between 1 and 65535, got %d", "port", checkTypeTCP, v.port)
		}
	case checkTypeHTTP:
		if !v.unknown["shouldcontain"] && !v.unknown["shouldnotcontain"] &&
			v.shouldContain != "" && v.shouldNotContain != "" {
			return fmt.Errorf("%q and %q must not be set at the same time", "shouldcontain", "shouldnotcontain")
		}
	}
	return nil
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

// clearedParams returns the PUT parameters that must be sent empty so Pingdom
// removes a value the check currently carries.
//
// Empty parameters are deliberately kept to a minimum: Pingdom answers 400
// "Invalid parameter" when it receives an unexpected empty one, so a parameter
// is only emptied when the prior state actually held a value for it. During
// create the prior state is empty, so nothing is returned.
func clearedParams(d *schema.ResourceData, checkType string) []string {
	wasSet := func(attr string) bool {
		old, _ := d.GetChange(attr)
		s, _ := old.(string)
		return s != ""
	}
	isSet := func(attr string) bool { return d.Get(attr).(string) != "" }

	var clear []string
	switch checkType {
	case checkTypeHTTP:
		// Only one of the pair may appear in a request at all, and setting
		// either one replaces the other, so an explicit clear is needed only
		// when the config now sets neither.
		if !isSet("shouldcontain") && !isSet("shouldnotcontain") {
			switch {
			case wasSet("shouldcontain"):
				clear = append(clear, "shouldcontain")
			case wasSet("shouldnotcontain"):
				clear = append(clear, "shouldnotcontain")
			}
		}
		// go-pingdom renders the credentials as a single "auth" parameter.
		if !isSet("username") && wasSet("username") {
			clear = append(clear, "auth")
		}
	case checkTypeTCP:
		for _, attr := range []string{"stringtosend", "stringtoexpect"} {
			if !isSet(attr) && wasSet(attr) {
				clear = append(clear, attr)
			}
		}
	}
	return clear
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
		return httpCheck{HttpCheck: &pingdom.HttpCheck{
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
		}, clear: clearedParams(d, checkTypeHTTP)}, nil
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
		return tcpCheck{TCPCheck: &pingdom.TCPCheck{
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
		}, clear: clearedParams(d, checkTypeTCP)}, nil
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

	// The same per-type checks CustomizeDiff runs at plan time. They should
	// never fire here -- if they do, the value changed between plan and apply,
	// so say so explicitly rather than letting go-pingdom report a Go field
	// name from inside the API layer.
	if err := validateCheckValues(checkValuesFromResourceData(d)); err != nil {
		return diag.Errorf("Refusing to update check %d: %s. "+
			"This was valid when the plan was created, so the value was lost between plan and apply", id, err)
	}

	log.Printf("[DEBUG] Check update configuration: %#v", check.PutParams()["name"])

	// Captured before the read below replaces these with whatever the API
	// reports, so the two can be compared.
	requested := snapshotRequested(d)

	_, err = client.Checks.Update(id, check)
	if err != nil {
		return diag.Errorf("Error updating check: %s", err)
	}

	diags := resourcePingdomCheckRead(ctx, d, meta)
	if diags.HasError() {
		return diags
	}

	// The update succeeded, but Pingdom may have silently declined part of it.
	return append(diags, warnUnappliedSettings(id, unappliedSettings(requested, d))...)
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
