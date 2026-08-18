# Changelog

## Unreleased

BUG FIXES:

* `pingdom_check`: updates no longer leave the check disagreeing with Terraform
  state. Removing `username`/`password`, `shouldcontain`, `shouldnotcontain`,
  `stringtosend` or `stringtoexpect` from a configuration now actually clears
  them on the check, instead of being recorded as empty in state while the check
  kept its old value. Switching a check from `shouldcontain` to
  `shouldnotcontain` previously left both set, a combination the Pingdom API
  rejects. A request never carries both content matchers, and an empty parameter
  is only ever sent for a field whose prior state held a value, because Pingdom
  answers `400 Invalid parameter` for an unexpected empty one.
* `pingdom_check`: type-specific requirements are validated during `plan`
  instead of failing part-way through `apply` with a message naming a Go struct
  field: `expectedip`/`nameserver` for `dns` checks, `port` for `tcp` checks,
  and `shouldcontain`/`shouldnotcontain` not being set together.
* `pingdom_check`: `probefilters` keeps every filter. Only the first was read
  back, so any subsequent update deleted the rest from the check. Filters are
  now normalised and sorted, so a value written as `"region: NA"` no longer
  produces a permanent diff.
* `pingdom_check`: `custom_message` is refreshed from the API. It could not be
  read before, so importing a check recorded an empty message and the next
  apply cleared it.
* `pingdom_check`: `paused` is read from the API's pause flag rather than
  inferred from the health status, so pausing or unpausing a check outside
  Terraform is detected.
* `pingdom_tms_check`: `width` in the `metadata` block is no longer ignored (it
  was read from a misspelled key).
* `pingdom_tms_check`: `active` now defaults to `true`. A configuration that
  omitted it sent `active: false` on every create and update, deactivating the
  check.

IMPROVEMENTS:

* `pingdom_check` and `pingdom_tms_check`: refreshing no longer lists every
  check before reading the one it needs, removing an extra API call per
  resource per refresh.
* `pingdom_check`: read attributes with `d.Get` rather than `d.GetOk`, which
  cannot distinguish an unset value from an explicit zero.

## 1.1.3 (October 20, 2020)

BREAKING CHANGES:

* This release removes support for the deprecated v2.X Pingdom APIs.

NEW FEATURES:

* Add support for the v3.1 Pingdom API (#77)

IMPROVEMENTS:

* Documentation improvements (#73, #76)
* Add GitHub actions workflows for linting and testing (#75)

## 1.1.2 (September 13, 2020)

NEW FEATURES:

* Add support for managing teams, contacts, users (#36)
* Allow adding users to teams (#61)

IMPROVEMENTS:

* Add responsetimethreshold to checks (#36)
* CI improvements (#48)
* Documentation improvements (#45)
* Uses latest patch version of go in CI builds (#50)
* Update to terraform 0.12.18 (#51, #54)
* Migrate to terraform-plugin-sdk (#65)
* Sort tags on write to prevent unnecessary diffs (#58)
* Documentation improvements (#53, #66, #67)
* Use GitHub actions for builds and releases (#72)

BUG FIXES:

* Include existing probefilter values on reads (#47)
* Fix issue importing contacts (#60)

## 1.1.1 (October 5, 2019)

IMPROVEMENTS:

* Add sensitive flags for secret values (#44)
* Publish releases from Travis CI (#41)

## 1.0.0 (July 3, 2019)

NEW FEATURES:

* Add TCP Checks (#21)
* Add support for Public Reports (#21)
* Add support for integrations/webhooks in checks (#14)
* Add support for probe filters (#13)
* Checks support paused parameter (#22)
* Add support for tags on checks (#8)
* Add support for importing existing checks (#9)
* Add contact IDs to schema (#3)
* Add option to use legacy notifications
* Add spport for multi-user accounts (#1)

IMPROVEMENTS:

* Use go modules to manage dependencies (#30)
* Update to go-pingdom v1.0.0
* Update to terraform 0.12.3 (#38)
* Documentation updates (#12)

BUG FIXES:

* Cannot use imported resource without forced re-creation (#31)
* Fix teams response (#27)
* Stop using `id` check schema in (#11)
* Add default value for check URLs (#4)

## 0.2.0 (October 17, 2014)

IMPROVEMENTS:

* Add support for Terraform 0.3.0

## 0.1.1 (September 16, 2014)

FEATURES:

* Add support for managing ping type checks

BUG FIXES:

* Don't override check resource values on create

## 0.1.0 (September 7, 2014)

* Initial release
