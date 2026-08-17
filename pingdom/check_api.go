package pingdom

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mbarper/go-pingdom/pingdom"
)

// This file works around three gaps in go-pingdom's check API surface. Each one
// is only reachable on update, and each one leaves the check in a state that
// disagrees with Terraform's state.

// checkResponse extends pingdom.CheckResponse with fields the Pingdom API
// returns but go-pingdom's response struct does not declare. Without
// custom_message here, the attribute can never be refreshed, so importing a
// check silently records an empty message and the next apply clears it.
type checkResponse struct {
	pingdom.CheckResponse
	CustomMessage string `json:"custom_message"`
}

// readCheck fetches a single check, including the fields checkResponse adds.
func readCheck(client *pingdom.Client, id int) (*checkResponse, error) {
	req, err := client.NewRequest(http.MethodGet, "/checks/"+strconv.Itoa(id), nil)
	if err != nil {
		return nil, err
	}

	var body struct {
		Check *checkResponse `json:"check"`
	}
	if _, err := client.Do(req, &body); err != nil {
		return nil, err
	}
	if body.Check == nil {
		return nil, errors.New("empty check in API response")
	}

	// go-pingdom backfills this in Checks.Read; the API only reports `teams`.
	body.Check.TeamIds = make([]int, len(body.Check.Teams))
	for i := range body.Check.Teams {
		body.Check.TeamIds[i] = body.Check.Teams[i].ID
	}

	return body.Check, nil
}

// isCheckGone reports whether err means the check no longer exists. Pingdom
// answers 403 for a check that is absent or not visible to the token, so both
// it and 404 count as gone.
func isCheckGone(err error) bool {
	var apiErr *pingdom.PingdomError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusNotFound
}

// go-pingdom builds PUT parameters by omitting keys whose value is empty, which
// makes some fields impossible to clear: the parameter simply never reaches the
// API, so the check keeps its old value while Terraform records the new empty
// one. The wrappers below re-add those keys for updates.
//
// Only PutParams is overridden. PostParams still resolves to the embedded
// implementation, which strips empty values -- correct for create, where an
// empty parameter would be rejected rather than treated as "clear this".

// httpCheck fixes clearing of HTTP basic auth and the shouldcontain /
// shouldnotcontain pair.
type httpCheck struct {
	*pingdom.HttpCheck
}

func (ck httpCheck) PutParams() map[string]string {
	m := ck.HttpCheck.PutParams()

	// Sent only when Username is non-empty, so dropping the credentials from
	// the config would leave the check still authenticating.
	if _, ok := m["auth"]; !ok {
		m["auth"] = ""
	}

	// go-pingdom sends whichever of the two is set and omits the other, so
	// switching between them leaves both set on the check -- a combination its
	// own Valid() rejects. Valid() guarantees at most one is non-empty here, so
	// sending both is safe and lets either be cleared.
	m["shouldcontain"] = ck.ShouldContain
	m["shouldnotcontain"] = ck.ShouldNotContain

	return m
}

// tcpCheck fixes clearing of the send/expect strings.
type tcpCheck struct {
	*pingdom.TCPCheck
}

func (ck tcpCheck) PutParams() map[string]string {
	m := ck.TCPCheck.PutParams()
	m["stringtosend"] = ck.StringToSend
	m["stringtoexpect"] = ck.StringToExpect
	return m
}
