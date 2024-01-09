module github.com/kwasnick/circuit/v3/benchmarking

go 1.16

require (
	github.com/afex/hystrix-go v0.0.0-20180502004556-fa1af6a1f4f5
	github.com/cenk/backoff v2.2.1+incompatible // indirect
	github.com/cep21/circuit/v3 v3.2.0
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a // indirect
	github.com/iand/circuit v0.0.4
	github.com/peterbourgon/g2s v0.0.0-20170223122336-d4e7ad98afea // indirect
	github.com/rubyist/circuitbreaker v2.2.1+incompatible
	github.com/smartystreets/goconvey v1.6.4 // indirect
	github.com/sony/gobreaker v0.4.1
	github.com/streadway/handy v0.0.0-20200128134331-0f66f006fb2e
)

replace github.com/cep21/circuit/v3 => ../
