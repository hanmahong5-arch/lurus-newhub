package common

// InstanceID returns this process's stable identity for observability
// purposes: attributing an authenticated /metrics scrape to a specific
// replica. It is deliberately not published on the public /api/health
// endpoint. Precedence: POD_NAME (set by the k8s downward API from
// metadata.name), then HOSTNAME (what an unmodified pod spec already has,
// and what local `go run` sees), then the literal "node".
//
// Deliberately NOT NodeHolderID (leader.go) — that identity appends a random
// suffix per process specifically so two processes sharing a hostname can't
// both believe they hold the same lease. An observability label must be the
// same value on every call from the same pod, or an operator diffing scrapes
// over time would see a new "instance" on every restart for no reason.
func InstanceID() string {
	if pod := GetEnvOrDefaultString("POD_NAME", ""); pod != "" {
		return pod
	}
	if host := GetEnvOrDefaultString("HOSTNAME", ""); host != "" {
		return host
	}
	return "node"
}
