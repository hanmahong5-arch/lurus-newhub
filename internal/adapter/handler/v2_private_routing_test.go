package handler

import "testing"

// The verdict is the one string a buyer reads off the console badge, so its
// derivation is pinned here rather than left to integration testing. The
// non-obvious rule: a private-endpoint channel the dispatch guard will REFUSE
// must not count toward "we are on-prem" — otherwise a tenant whose only
// private channel is misconfigured would see a green badge while every request
// 500s.
func TestDerivePrivateRoutingVerdict(t *testing.T) {
	usablePrivate := privateRoutingChannel{Type: 57, Intranet: true, WillBeBlocked: false}
	blockedPrivate := privateRoutingChannel{Type: 57, Intranet: false, WillBeBlocked: true}
	external := privateRoutingChannel{Type: 1, Intranet: false}

	cases := []struct {
		name    string
		resp    privateRoutingResponse
		want    string
		because string
	}{
		{
			name: "only a usable private endpoint",
			resp: privateRoutingResponse{
				PrivateEndpointChannels: []privateRoutingChannel{usablePrivate},
			},
			want:    PrivateRoutingVerdictAllOnPrem,
			because: "one intranet private endpoint and nothing else is the green case",
		},
		{
			name: "private endpoint plus an external provider",
			resp: privateRoutingResponse{
				PrivateEndpointChannels: []privateRoutingChannel{usablePrivate},
				ExternalChannels:        []privateRoutingChannel{external},
			},
			want:    PrivateRoutingVerdictMixed,
			because: "a reachable external channel means traffic can still egress",
		},
		{
			name: "only a BLOCKED private endpoint",
			resp: privateRoutingResponse{
				PrivateEndpointChannels: []privateRoutingChannel{blockedPrivate},
				BlockedChannels:         []privateRoutingChannel{blockedPrivate},
			},
			want:    PrivateRoutingVerdictNoPrivate,
			because: "a channel the guard refuses cannot make a tenant on-prem — it serves nothing",
		},
		{
			name: "blocked private endpoint alongside a usable one",
			resp: privateRoutingResponse{
				PrivateEndpointChannels: []privateRoutingChannel{blockedPrivate, usablePrivate},
				BlockedChannels:         []privateRoutingChannel{blockedPrivate},
			},
			want:    PrivateRoutingVerdictAllOnPrem,
			because: "the blocked row is noise for routing purposes; it emits no traffic at all",
		},
		{
			name:    "no channels at all",
			resp:    privateRoutingResponse{},
			want:    PrivateRoutingVerdictNoPrivate,
			because: "an empty tenant is not on-prem, it is unconfigured",
		},
		{
			name: "external only",
			resp: privateRoutingResponse{
				ExternalChannels: []privateRoutingChannel{external},
			},
			want:    PrivateRoutingVerdictNoPrivate,
			because: "no private endpoint configured, regardless of how many external ones there are",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := derivePrivateRoutingVerdict(tc.resp); got != tc.want {
				t.Fatalf("got %q, want %q — %s", got, tc.want, tc.because)
			}
		})
	}
}
