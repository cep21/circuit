// Package hystrixadaptive provides a ClosedToOpen implementation that composes the
// standard Hystrix opener and adapts open decisions when failures are dominated by
// timeouts while extra latency headroom is elevated (ambient slowness).
package hystrixadaptive
