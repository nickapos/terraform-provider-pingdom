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
// makes some fields impossible to clear: the parameter never reaches the API, so
// the check keeps its old value while Terraform records the new empty one.
//
// The obvious fix -- always send the key -- is wrong. Pingdom rejects the whole
// request with "400 Bad Request: Invalid parameter" when it receives an empty
// parameter it did not expect, and it refuses shouldcontain and
// shouldnotcontain together under any circumstances, even with one of them
// empty. So an empty parameter is sent only when there is genuinely a value to
// remove, which the caller determines from the prior state and passes in as
// clear. Anything not being cleared is left exactly as go-pingdom rendered it.
//
// Only PutParams is overridden. PostParams still resolves to the embedded
// implementation, which strips empty values -- correct for create, where nothing
// exists to clear.

// httpCheck fixes clearing of HTTP basic auth and the shouldcontain /
// shouldnotcontain pair.
type httpCheck struct {
	*pingdom.HttpCheck
	// clear names PUT parameters to send empty, e.g. "auth", "shouldcontain".
	clear []string
}

func (ck httpCheck) PutParams() map[string]string {
	m := ck.HttpCheck.PutParams()

	// Send at most one of the pair. go-pingdom already picks one, but it falls
	// back to shouldnotcontain="" whenever ShouldContain is empty, which is
	// exactly the rejected combination once shouldcontain is also present.
	delete(m, "shouldcontain")
	delete(m, "shouldnotcontain")
	switch {
	case ck.ShouldContain != "":
		m["shouldcontain"] = ck.ShouldContain
	case ck.ShouldNotContain != "":
		m["shouldnotcontain"] = ck.ShouldNotContain
	}

	applyClearParams(m, ck.clear)
	return m
}

// tcpCheck fixes clearing of the send/expect strings.
type tcpCheck struct {
	*pingdom.TCPCheck
	clear []string
}

func (ck tcpCheck) PutParams() map[string]string {
	m := ck.TCPCheck.PutParams()
	applyClearParams(m, ck.clear)
	return m
}

// applyClearParams adds each named parameter as empty, unless a value is
// already being sent for it.
func applyClearParams(m map[string]string, clear []string) {
	for _, key := range clear {
		if _, ok := m[key]; !ok {
			m[key] = ""
		}
	}
}
