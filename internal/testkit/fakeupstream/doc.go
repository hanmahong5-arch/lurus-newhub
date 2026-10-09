// Package fakeupstream is a protocol-level stand-in for the LLM vendors newhub
// relays to: OpenAI chat completions and Responses, Anthropic messages, Gemini
// generateContent and System One, each streaming and non-streaming, plus the
// failure shapes real vendors produce.
//
// One implementation, three places:
//
//   - Go tests import it (NewTest) instead of hand-writing an httptest server
//     and an SSE body per package;
//   - the local acceptance stack runs it as a process (cmd/fakeupstream),
//     next to the fake platform wallet (internal/testkit/fakeplatform);
//   - the UAT fault simulator (handler/faultsim.go) serves its fault modes
//     through it.
//
// Two properties make it useful for money tests, not just for "a 200 came
// back":
//
//   - Usage is deterministic. Every successful answer reports the configured
//     token counts (Config.Usage, changeable at runtime through
//     POST /_fake/usage), so a test can compute the exact charge from a price
//     list it holds itself rather than reading the price back from the system
//     under test.
//   - Every request is recorded (GET /_fake/requests). A suite that never
//     reached the upstream — wrong channel, wrong base URL, a cached answer —
//     can be told apart from one that did.
//
// The package depends on the standard library only. It is an upstream: it
// must not share code with the relay it is testing.
package fakeupstream
