/*
Package hystrixadaptive implements ClosedToOpen by wrapping the Hystrix opener and
deferring trips when failures are mostly timeouts while headroom is below its cap

Additive headroom "extra" sits on top of BaselineLatency; timeouts and slow successes
raise it (capped by MaxExtraLatency); successes faster than BaselineLatency lower it;
set per-request deadlines on circuit.Execution, not in this package
*/
package hystrixadaptive
