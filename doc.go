/*
Package hystrix is a Go implementation of Netflix's Hystrix library.  Most documentation is available on
the github README page https://github.com/cep21/hystrix/blob/master/README.md

Use case

Netflix describes most use cases on their wiki for Hystrix at https://github.com/Netflix/Hystrix/wiki.  Quoting the wiki:

   Give protection from and control over latency and failure from dependencies accessed (typically over the network) via third-party client libraries.
   Stop cascading failures in a complex distributed system.
   Fail fast and rapidly recover.
   Fallback and gracefully degrade when possible.
   Enable near real-time monitoring, alerting, and operational control.

It is a great library for microservice applications that require a large number of calls to many, small services where
any one of these calls could fail, or any of these services could be down or degraded.

Getting started

The godoc contains many examples.  Look at them for a good start on how to get started integrated and using the
Hystrix library for Go.

Circuit Flowchart

A circuits start Closed.  The default logic is to open a circuit if more than 20 requests have come in during a 10
second window, and over 50 of requests during that 10 second window are failing.

Once failed, the circuit waits 10 seconds before allowing a single request.  If that request succeeds, then the circuit
closes.  If it fails, then the circuit waits another 10 seconds before allowing another request (and so on).

Almost every part of this flow can be configured.  See the CommandProperties struct for information.

Metric tracking

All circuits record circuit stats that you can fetch out of the Circuit at any time.  In addition, you can also inject
your own circuit stat trackers by modifying the MetricsCollectors structure.

*/
package hystrix
