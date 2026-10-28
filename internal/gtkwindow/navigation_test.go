package gtkwindow

import "testing"

// The navigation rule is the whole security surface of the window: whatever it
// allows, WebKit loads. These cases are the ones that matter, and they hold for
// every build, because the rule is compiled into the stub too.
func TestSameOriginAllowsOnlyTheDashboardOrigin(t *testing.T) {
	const origin = "http://127.0.0.1:35883"
	allow := SameOrigin(origin)
	for _, want := range []struct {
		uri   string
		allow bool
		why   string
	}{
		{origin + "/", true, "the dashboard root"},
		{origin + "/#ticket=abc123", true, "the authenticated launch URL"},
		{origin + "/api/v1/foundation", true, "the dashboard's own API"},
		{origin + "/assets/index.js?v=2", true, "a dashboard asset"},
		{"http://127.0.0.1:35884/", false, "another loopback port is not this dashboard"},
		{"http://localhost:35883/", false, "a different host name is a different origin"},
		{"http://127.0.0.1/", false, "the default port is not the dashboard's port"},
		{"https://127.0.0.1:35883/", false, "the dashboard is not served over TLS"},
		{"http://127.0.0.1:35883@evil.example/", false, "userinfo must not smuggle another host"},
		{"http://evil.example:35883/", false, "an external host"},
		{"file:///etc/passwd", false, "local files are not reachable from the window"},
		{"javascript:alert(1)", false, "script URLs are not navigations"},
		{"data:text/html,<b>x</b>", false, "data URLs are not navigations"},
		{"about:blank", false, "nothing outside the dashboard is loaded"},
		{"", false, "an empty navigation"},
		{"not a url at all", false, "a malformed navigation"},
	} {
		if got := allow(want.uri); got != want.allow {
			t.Errorf("SameOrigin(%q)(%q) = %t, want %t (%s)", origin, want.uri, got, want.allow, want.why)
		}
	}
}

// An origin that is not itself a usable loopback origin must not make the rule
// permissive: a malformed or incomplete origin allows nothing at all.
func TestSameOriginRefusesToAllowAnythingForAnIncompleteOrigin(t *testing.T) {
	for _, origin := range []string{"", "http://127.0.0.1", "127.0.0.1:1234", "http://:1234", "://"} {
		allow := SameOrigin(origin)
		if allow("http://127.0.0.1:1234/") {
			t.Errorf("SameOrigin(%q) allowed a navigation; an unusable origin must allow none", origin)
		}
	}
}
