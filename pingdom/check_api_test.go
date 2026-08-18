package pingdom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/mbarper/go-pingdom/pingdom"
)

// fakeCheckAPI stands in for the Pingdom v3.1 checks API. It keeps server-side
// state and echoes back everything it was told to store, in the response shape
// the real API uses (see go-pingdom's api_responses_test.go fixtures). Modelling
// it as a faithful echo is deliberate: anything that fails to round-trip
// through it is a provider defect rather than an API quirk.
//
// These are unit tests -- they need no credentials and no TF_ACC.
type fakeCheckAPI struct {
	check map[string]string
	puts  []map[string]string
	// gone makes the check answer 403, as Pingdom does for a deleted check.
	gone bool
	// status overrides the reported health status.
	status string
	// listed counts list-all-checks calls, which a refresh should not need.
	listed int
}

const fakeCheckID = 123

func (f *fakeCheckAPI) start(t *testing.T) (*Clients, func()) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/3.1/checks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			f.check = queryMap(r)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"check": map[string]any{"id": fakeCheckID},
			})
		case http.MethodGet:
			f.listed++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"checks": []map[string]any{{"id": fakeCheckID}},
			})
		}
	})
	mux.HandleFunc("/api/3.1/checks/"+strconv.Itoa(fakeCheckID), func(w http.ResponseWriter, r *http.Request) {
		if f.gone {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"statuscode": 403, "statusdesc": "Forbidden", "errormessage": "Check not found",
			}})
			return
		}
		switch r.Method {
		case http.MethodPut:
			got := queryMap(r)
			f.puts = append(f.puts, got)
			// The real API rejects the whole request when both content
			// matchers are present, even if one of them is empty.
			_, hasContain := got["shouldcontain"]
			_, hasNotContain := got["shouldnotcontain"]
			if hasContain && hasNotContain {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
					"statuscode": 400, "statusdesc": "Bad Request",
					"errormessage": "Invalid parameter:  shouldnotcontain",
				}})
				return
			}
			// Setting either matcher replaces the other.
			if v, ok := got["shouldcontain"]; ok && v != "" {
				f.check["shouldnotcontain"] = ""
			}
			if v, ok := got["shouldnotcontain"]; ok && v != "" {
				f.check["shouldcontain"] = ""
			}
			// Pingdom applies the supplied parameters on top of the stored check.
			for k, v := range got {
				f.check[k] = v
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "ok"})
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"check": f.response()})
		}
	})

	srv := httptest.NewServer(mux)
	client, err := pingdom.NewClientWithConfig(pingdom.ClientConfig{
		APIToken: "token",
		BaseURL:  srv.URL + "/api/3.1",
	})
	if err != nil {
		srv.Close()
		t.Fatalf("client: %s", err)
	}
	return &Clients{Pingdom: client}, srv.Close
}

func queryMap(r *http.Request) map[string]string {
	m := map[string]string{}
	for k, v := range r.URL.Query() {
		m[k] = v[0]
	}
	return m
}

// response renders the stored check the way the real API reports it.
func (f *fakeCheckAPI) response() map[string]any {
	c := f.check
	resp := map[string]any{
		"id":               fakeCheckID,
		"name":             c["name"],
		"hostname":         c["host"],
		"status":           "up",
		"paused":           c["paused"] == "true",
		"ipv6":             c["ipv6"] == "true",
		"notifywhenbackup": c["notifywhenbackup"] == "true",
		"custom_message":   c["custom_message"],
		"integrationids":   csvInts(c["integrationids"]),
		"userids":          csvInts(c["userids"]),
	}
	if f.status != "" {
		resp["status"] = f.status
	}
	for _, k := range []string{"resolution", "sendnotificationwhendown", "notifyagainevery", "responsetime_threshold"} {
		if n, _ := strconv.Atoi(c[k]); n != 0 {
			resp[k] = n
		}
	}

	tags := []map[string]any{}
	for _, t := range csvFields(c["tags"]) {
		tags = append(tags, map[string]any{"name": t, "type": "u", "count": 1})
	}
	resp["tags"] = tags

	// The API reports probe filters as an array, with a space after the colon.
	filters := []string{}
	for _, p := range csvFields(c["probe_filters"]) {
		filters = append(filters, strings.Replace(p, ":", ": ", 1))
	}
	resp["probe_filters"] = filters

	teams := []map[string]any{}
	for _, id := range csvInts(c["teamids"]) {
		teams = append(teams, map[string]any{"id": id, "name": "team"})
	}
	resp["teams"] = teams

	switch c["type"] {
	case checkTypeTCP:
		resp["type"] = map[string]any{"tcp": map[string]any{
			"port":           atoiOrZero(c["port"]),
			"stringtosend":   c["stringtosend"],
			"stringtoexpect": c["stringtoexpect"],
		}}
	case checkTypeDNS:
		resp["type"] = map[string]any{"dns": map[string]any{
			"expectedip": c["expectedip"],
			"nameserver": c["nameserver"],
		}}
	case checkTypePing:
		resp["type"] = "ping"
	default:
		// Pingdom always injects its own bot User-Agent.
		headers := map[string]string{
			"User-Agent": "Pingdom.com_bot_version_1.4_(http://www.pingdom.com/)",
		}
		for k, v := range c {
			if strings.HasPrefix(k, "requestheader") {
				if name, val, ok := strings.Cut(v, ":"); ok {
					headers[name] = val
				}
			}
		}
		details := map[string]any{
			"url":                  c["url"],
			"encryption":           c["encryption"] == "true",
			"postdata":             c["postdata"],
			"shouldcontain":        c["shouldcontain"],
			"shouldnotcontain":     c["shouldnotcontain"],
			"verify_certificate":   c["verify_certificate"] == "true",
			"ssl_down_days_before": atoiOrZero(c["ssl_down_days_before"]),
			"requestheaders":       headers,
		}
		if p := atoiOrZero(c["port"]); p != 0 {
			details["port"] = p
		}
		if user, pass, ok := strings.Cut(c["auth"], ":"); ok {
			details["username"] = user
			details["password"] = pass
		}
		resp["type"] = map[string]any{"http": details}
	}
	return resp
}

func atoiOrZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func csvFields(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func csvInts(s string) []int {
	out := []int{}
	for _, p := range csvFields(s) {
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// applyCheck runs the real resource create or update path for cfg and returns
// the resulting Terraform state.
func applyCheck(t *testing.T, meta *Clients, prior *terraform.InstanceState, cfg map[string]any) *terraform.InstanceState {
	t.Helper()
	ctx := context.Background()
	r := resourcePingdomCheck()
	if prior == nil {
		prior = &terraform.InstanceState{}
	}

	diff, err := r.Diff(ctx, prior, terraform.NewResourceConfigRaw(cfg), meta)
	if err != nil {
		t.Fatalf("diff: %s", err)
	}
	if diff == nil {
		return prior
	}
	state, diags := r.Apply(ctx, prior, diff, meta)
	if diags.HasError() {
		t.Fatalf("apply: %v", diags)
	}
	return state
}

// refreshCheck runs the real read path and returns the refreshed state.
func refreshCheck(t *testing.T, meta *Clients, state *terraform.InstanceState) *terraform.InstanceState {
	t.Helper()
	d := resourcePingdomCheck().Data(state)
	if diags := resourcePingdomCheckRead(context.Background(), d, meta); diags.HasError() {
		t.Fatalf("read: %v", diags)
	}
	return d.State()
}

func baseHTTPConfig() map[string]any {
	return map[string]any{
		"name": "prod-api",
		"host": "api.example.com",
		"type": checkTypeHTTP,
	}
}

// TestCheckUpdateClearsFields covers the core bug: an update rebuilds the whole
// check, so a field removed from the config has to actually be cleared on the
// check rather than silently retained.
func TestCheckUpdateClearsFields(t *testing.T) {
	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()

	cfg := baseHTTPConfig()
	cfg["username"] = "svc"
	cfg["password"] = "s3cret"
	cfg["shouldcontain"] = "OK"
	cfg["postdata"] = "ping"
	state := applyCheck(t, meta, nil, cfg)

	if got := fake.check["auth"]; got != "svc:s3cret" {
		t.Fatalf("create should have set auth, got %q", got)
	}

	// Remove all four from the config.
	applyCheck(t, meta, state, baseHTTPConfig())

	for _, key := range []string{"auth", "shouldcontain", "postdata"} {
		if got := fake.check[key]; got != "" {
			t.Errorf("update did not clear %q on the check: still %q", key, got)
		}
	}
}

// TestCheckUpdateSwitchesContainMatch guards the case that left the check in a
// state the API itself rejects: shouldcontain and shouldnotcontain both set.
func TestCheckUpdateSwitchesContainMatch(t *testing.T) {
	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()

	cfg := baseHTTPConfig()
	cfg["shouldcontain"] = "OK"
	state := applyCheck(t, meta, nil, cfg)

	switched := baseHTTPConfig()
	switched["shouldnotcontain"] = "ERROR"
	applyCheck(t, meta, state, switched)

	if got := fake.check["shouldcontain"]; got != "" {
		t.Errorf("shouldcontain should have been cleared, still %q", got)
	}
	if got := fake.check["shouldnotcontain"]; got != "ERROR" {
		t.Errorf("shouldnotcontain = %q, want %q", got, "ERROR")
	}
}

// TestCheckUpdateClearsTCPStrings is the TCP equivalent: stringtosend and
// stringtoexpect were also omitted from the PUT when empty.
func TestCheckUpdateClearsTCPStrings(t *testing.T) {
	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()

	cfg := map[string]any{
		"name":           "tcp-check",
		"host":           "api.example.com",
		"type":           checkTypeTCP,
		"port":           443,
		"stringtosend":   "PING",
		"stringtoexpect": "PONG",
	}
	state := applyCheck(t, meta, nil, cfg)

	cleared := map[string]any{
		"name": "tcp-check",
		"host": "api.example.com",
		"type": checkTypeTCP,
		"port": 443,
	}
	applyCheck(t, meta, state, cleared)

	for _, key := range []string{"stringtosend", "stringtoexpect"} {
		if got := fake.check[key]; got != "" {
			t.Errorf("update did not clear %q: still %q", key, got)
		}
	}
}

// TestCheckProbeFiltersRoundTrip guards against the truncation that made an
// unrelated change destroy every probe filter but the first.
func TestCheckProbeFiltersRoundTrip(t *testing.T) {
	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()

	cfg := baseHTTPConfig()
	cfg["probefilters"] = "region:NA,region:EU"
	state := applyCheck(t, meta, nil, cfg)

	if got := state.Attributes["probefilters"]; got != "region:EU,region:NA" {
		t.Fatalf("state probefilters = %q, want both filters", got)
	}

	// A rename must not disturb the filters.
	renamed := baseHTTPConfig()
	renamed["name"] = "prod-api-renamed"
	renamed["probefilters"] = "region:NA,region:EU"
	applyCheck(t, meta, state, renamed)

	got := csvFields(fake.check["probe_filters"])
	sort.Strings(got)
	if len(got) != 2 || got[0] != "region:EU" || got[1] != "region:NA" {
		t.Errorf("probe filters after rename = %v, want both preserved", got)
	}
}

// TestCheckCustomMessageRefresh covers the field go-pingdom's response struct
// omits: without it, an import records an empty message and the next apply
// clears it on the check.
func TestCheckCustomMessageRefresh(t *testing.T) {
	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()

	cfg := baseHTTPConfig()
	cfg["custom_message"] = "paged by oncall"
	state := applyCheck(t, meta, nil, cfg)

	// Drop it from state the way a fresh import would, then refresh.
	imported := state.DeepCopy()
	delete(imported.Attributes, "custom_message")
	refreshed := refreshCheck(t, meta, imported)

	if got := refreshed.Attributes["custom_message"]; got != "paged by oncall" {
		t.Errorf("custom_message not refreshed from the API: got %q", got)
	}
}

// TestCheckPausedRefresh covers reading the real Paused flag rather than
// inferring pause state from the health status, which could never observe an
// unpause performed outside Terraform.
func TestCheckPausedRefresh(t *testing.T) {
	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()

	cfg := baseHTTPConfig()
	cfg["paused"] = true
	state := applyCheck(t, meta, nil, cfg)
	if got := state.Attributes["paused"]; got != "true" {
		t.Fatalf("paused = %q after create, want true", got)
	}

	// Someone unpauses the check in the Pingdom UI. Status stays a health value.
	fake.check["paused"] = "false"
	refreshed := refreshCheck(t, meta, state)
	if got := refreshed.Attributes["paused"]; got != "false" {
		t.Errorf("unpause outside Terraform not detected: paused = %q", got)
	}

	// A check that reports the pause only through its status must still be seen
	// as paused, since the boolean is omitempty and may be absent.
	fake.check["paused"] = ""
	fake.status = "paused"
	again := refreshCheck(t, meta, refreshed)
	if got := again.Attributes["paused"]; got != "true" {
		t.Errorf(`status="paused" not honoured: paused = %q`, got)
	}
}

// TestCheckIdempotent asserts the provider round-trips a fully specified check:
// re-planning the identical config must produce no diff.
func TestCheckIdempotent(t *testing.T) {
	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()

	cfg := map[string]any{
		"name":                     "prod-api",
		"host":                     "api.example.com",
		"type":                     checkTypeHTTP,
		"url":                      "/health",
		"encryption":               true,
		"port":                     443,
		"resolution":               5,
		"sendnotificationwhendown": 2,
		"notifyagainevery":         3,
		"notifywhenbackup":         true,
		"responsetime_threshold":   5000,
		"verify_certificate":       true,
		"ssl_down_days_before":     14,
		"custom_message":           "api is down",
		"tags":                     "prod,api",
		"probefilters":             "region:NA,region:EU",
		"integrationids":           []any{33333333, 44444444},
		"userids":                  []any{111},
		"teamids":                  []any{222},
		"username":                 "svc",
		"password":                 "s3cret",
		"postdata":                 "ping",
		"shouldcontain":            "OK",
		"requestheaders":           map[string]any{"X-Test": "yes"},
		"ipv6":                     true,
	}

	state := applyCheck(t, meta, nil, cfg)
	diff, err := resourcePingdomCheck().Diff(
		context.Background(), state, terraform.NewResourceConfigRaw(cfg), meta,
	)
	if err != nil {
		t.Fatal(err)
	}
	if diff != nil && !diff.Empty() {
		keys := make([]string, 0, len(diff.Attributes))
		for k := range diff.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			a := diff.Attributes[k]
			t.Errorf("spurious diff: %s %q -> %q", k, a.Old, a.New)
		}
	}
}

// TestCheckReadDropsMissingCheck asserts a deleted check clears the ID instead
// of erroring, without listing every check to find out.
func TestCheckReadDropsMissingCheck(t *testing.T) {
	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()

	state := applyCheck(t, meta, nil, baseHTTPConfig())
	if fake.listed != 0 {
		t.Errorf("read listed all checks %d time(s); reading the check is enough", fake.listed)
	}

	fake.gone = true
	d := resourcePingdomCheck().Data(state)
	if diags := resourcePingdomCheckRead(context.Background(), d, meta); diags.HasError() {
		t.Fatalf("a missing check should not error: %v", diags)
	}
	if d.Id() != "" {
		t.Errorf("expected the resource ID to be cleared, got %q", d.Id())
	}
}

// TestCheckForResourceKeepsExplicitZeros pins the d.Get behaviour that the
// clearing fixes depend on: an explicit false or 0 must survive into the
// request rather than be discarded as "unset".
func TestCheckForResourceKeepsExplicitZeros(t *testing.T) {
	r := resourcePingdomCheck()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]any{})
	for k, v := range map[string]any{
		"name":                 "prod-api",
		"host":                 "api.example.com",
		"type":                 checkTypeHTTP,
		"verify_certificate":   false,
		"ssl_down_days_before": 0,
		"notifywhenbackup":     false,
	} {
		if err := d.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}

	ck, err := checkForResource(d)
	if err != nil {
		t.Fatal(err)
	}
	params := ck.PutParams()
	for key, want := range map[string]string{
		"verify_certificate":   "false",
		"ssl_down_days_before": "0",
		"notifywhenbackup":     "false",
	} {
		got, ok := params[key]
		if !ok {
			t.Errorf("%s missing from PUT params", key)
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// TestExpandTmsMetadata guards the typo that read m["weight"] for the width.
func TestExpandTmsMetadata(t *testing.T) {
	md := expandTmsMetadata(map[string]any{
		"disable_websecurity": true,
		"height":              768,
		"width":               1024,
	})
	if md.Width != 1024 {
		t.Errorf("Width = %d, want 1024", md.Width)
	}
	if md.Height != 768 {
		t.Errorf("Height = %d, want 768", md.Height)
	}
	if !md.DisableWebSecurity {
		t.Error("DisableWebSecurity = false, want true")
	}
}

// TestTmsCheckActiveDefault guards against sending active=false for a config
// that simply omits the attribute, which deactivated the check.
func TestTmsCheckActiveDefault(t *testing.T) {
	r := resourcePingdomTmsCheck()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]any{})
	if err := d.Set("name", "tms"); err != nil {
		t.Fatal(err)
	}
	if err := d.Set("steps", []any{map[string]any{"fn": "go_to", "args": map[string]any{"url": "example.com"}}}); err != nil {
		t.Fatal(err)
	}

	ck, err := toTmsCheck(d)
	if err != nil {
		t.Fatal(err)
	}
	if !ck.Active {
		t.Error("Active = false for a config that omits `active`; want true")
	}
	if !strings.Contains(ck.RenderForJSONAPI(), `"active":true`) {
		t.Errorf("payload should mark the check active: %s", ck.RenderForJSONAPI())
	}
}

func TestIsCheckGone(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"403", &pingdom.PingdomError{StatusCode: 403}, true},
		{"404", &pingdom.PingdomError{StatusCode: 404}, true},
		{"500", &pingdom.PingdomError{StatusCode: 500}, false},
		{"other", fmt.Errorf("dial tcp: connection refused"), false},
	} {
		if got := isCheckGone(tc.err); got != tc.want {
			t.Errorf("%s: isCheckGone = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeProbeFilters(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"region:NA", "region:NA"},
		{"region: NA", "region:NA"},
		{"region:NA,region:EU", "region:EU,region:NA"},
		{"region: NA, region: EU", "region:EU,region:NA"},
		{"region:EU,region:NA", "region:EU,region:NA"},
		{"region:NA,", "region:NA"},
	} {
		if got := normalizeProbeFilters(tc.in); got != tc.want {
			t.Errorf("normalizeProbeFilters(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCheckProbeFiltersSpacingIdempotent covers a config written the way the
// docs show it, with a space after the colon: it must not produce a diff.
func TestCheckProbeFiltersSpacingIdempotent(t *testing.T) {
	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()

	cfg := baseHTTPConfig()
	cfg["probefilters"] = "region: NA, region: EU"
	state := applyCheck(t, meta, nil, cfg)

	diff, err := resourcePingdomCheck().Diff(
		context.Background(), state, terraform.NewResourceConfigRaw(cfg), meta,
	)
	if err != nil {
		t.Fatal(err)
	}
	if diff != nil && !diff.Empty() {
		t.Errorf("spaced probefilters produced a diff: %v", diff.Attributes)
	}
}

// TestValidateCheckValues unit-tests the shared validation. These are the
// conditions go-pingdom only enforces once an apply is under way.
func TestValidateCheckValues(t *testing.T) {
	base := func(t string) checkValues {
		return checkValues{checkType: t, name: "n", hostname: "h", unknown: map[string]bool{}}
	}
	for _, tc := range []struct {
		name    string
		values  func() checkValues
		wantErr string
	}{
		{"empty name", func() checkValues { v := base(checkTypeHTTP); v.name = ""; return v }, `"name" must not be empty`},
		{"empty host", func() checkValues { v := base(checkTypeHTTP); v.hostname = ""; return v }, `"host" must not be empty`},
		{
			"empty host but unknown",
			func() checkValues {
				v := base(checkTypeHTTP)
				v.hostname = ""
				v.unknown["host"] = true
				return v
			},
			"",
		},
		{"dns without expectedip", func() checkValues { v := base(checkTypeDNS); v.nameServer = "ns"; return v }, `"expectedip" is required`},
		{"dns without nameserver", func() checkValues { v := base(checkTypeDNS); v.expectedIP = "1.2.3.4"; return v }, `"nameserver" is required`},
		{
			"dns with unknown expectedip",
			func() checkValues {
				v := base(checkTypeDNS)
				v.nameServer = "ns"
				v.unknown["expectedip"] = true
				return v
			},
			"",
		},
		{
			"dns complete",
			func() checkValues {
				v := base(checkTypeDNS)
				v.expectedIP = "1.2.3.4"
				v.nameServer = "ns"
				return v
			},
			"",
		},
		{"tcp without port", func() checkValues { return base(checkTypeTCP) }, `"port" is required`},
		{"tcp port out of range", func() checkValues { v := base(checkTypeTCP); v.port = 70000; return v }, `"port" is required`},
		{
			"tcp with unknown port",
			func() checkValues {
				v := base(checkTypeTCP)
				v.unknown["port"] = true
				return v
			},
			"",
		},
		{"tcp with port", func() checkValues { v := base(checkTypeTCP); v.port = 443; return v }, ""},
		{
			"http with both contain matches",
			func() checkValues {
				v := base(checkTypeHTTP)
				v.shouldContain = "a"
				v.shouldNotContain = "b"
				return v
			},
			`must not be set at the same time`,
		},
		{
			"http with one contain match",
			func() checkValues {
				v := base(checkTypeHTTP)
				v.shouldNotContain = "b"
				return v
			},
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCheckValues(tc.values())
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %s", err)
			case tc.wantErr == "":
			case err == nil:
				t.Fatalf("expected an error containing %q, got none", tc.wantErr)
			case !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidateCheckAtPlan drives the CustomizeDiff through a real plan. An empty
// host must be rejected before any API call; unknown values must not be.
func TestValidateCheckAtPlan(t *testing.T) {
	// The sentinel the SDK uses for a value that is not yet known.
	const unknown = "74D93920-ED26-11E3-AC10-0800200C9A66"

	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()
	r := resourcePingdomCheck()

	for _, tc := range []struct {
		name    string
		cfg     map[string]any
		wantErr string
	}{
		{
			name:    "http with an empty host",
			cfg:     map[string]any{"name": "n", "host": "", "type": checkTypeHTTP, "ipv6": true},
			wantErr: `"host" must not be empty`,
		},
		{
			name:    "dns with an empty expectedip",
			cfg:     map[string]any{"name": "n", "host": "h", "type": checkTypeDNS, "nameserver": "ns"},
			wantErr: `"expectedip" is required`,
		},
		{
			name: "host not yet known",
			cfg:  map[string]any{"name": "n", "host": unknown, "type": checkTypeHTTP},
		},
		{
			name: "dns values not yet known",
			cfg:  map[string]any{"name": "n", "host": "h", "type": checkTypeDNS, "nameserver": unknown, "expectedip": unknown},
		},
		{
			name: "ipv6 http check is fine",
			cfg:  map[string]any{"name": "n", "host": "2a02:e980:1f:1::8", "type": checkTypeHTTP, "ipv6": true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Diff(context.Background(), nil, terraform.NewResourceConfigRaw(tc.cfg), meta)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected plan error: %s", err)
			case tc.wantErr == "":
			case err == nil:
				t.Fatalf("expected a plan error containing %q, got none", tc.wantErr)
			case !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestCheckUpdateAddsContainMatch reproduces the production failure: an HTTP
// check with no content matcher and no credentials gains a shouldcontain. The
// request must not carry shouldnotcontain or auth at all -- Pingdom answers
// "400 Invalid parameter" for an empty parameter it did not expect, and refuses
// the two matchers together under any circumstances.
func TestCheckUpdateAddsContainMatch(t *testing.T) {
	fake := &fakeCheckAPI{}
	meta, stop := fake.start(t)
	defer stop()

	state := applyCheck(t, meta, nil, baseHTTPConfig())

	cfg := baseHTTPConfig()
	cfg["shouldcontain"] = "=11="
	cfg["notifywhenbackup"] = true
	applyCheck(t, meta, state, cfg)

	put := fake.puts[len(fake.puts)-1]
	if got, ok := put["shouldnotcontain"]; ok {
		t.Errorf("PUT must not carry shouldnotcontain alongside shouldcontain, got %q", got)
	}
	if got, ok := put["auth"]; ok {
		t.Errorf("PUT must not carry an empty auth when no credentials were ever set, got %q", got)
	}
	if got := put["shouldcontain"]; got != "=11=" {
		t.Errorf("shouldcontain = %q, want %q", got, "=11=")
	}
	if got := fake.check["shouldcontain"]; got != "=11=" {
		t.Errorf("stored shouldcontain = %q, want %q", got, "=11=")
	}
}
