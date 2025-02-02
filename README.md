## Swarm  <!-- omit in toc -->

[https://swarm.ethereum.org](https://swarm.ethereum.org)

Swarm is a distributed storage platform and content distribution service, a native base layer service of the ethereum web3 stack. The primary objective of Swarm is to provide a decentralized and redundant store for dapp code and data as well as block chain and state data. Swarm is also set out to provide various base layer services for web3, including node-to-node messaging, media streaming, decentralised database services and scalable state-channel infrastructure for decentralised service economies.

## New Bee client

In the effort to release a production-ready version of Swarm, the Swarm dev team has migrated their effort to build the [new Bee client](https://github.com/holisticode/bee), a brand-new implementation of Swarm. The main reason for this switch was the availability of a more mature networking layer (libp2p) and the secondary reason being that the insight gained from developing Swarm taught us many lessons which can be implemented best from scratch. While Bee does not currently expose every feature in the original Swarm client, development is happening at lightspeed and soon, it will surpass Swarm in functionality and stability!

Please refer to [Swarm webpage](https://ethswarm.org) for more information about the state of the Bee client and to [the Bee documentation](https://docs.ethswarm.org) for info on installing and using the new client.

### Original Swarm client

The old Swarm client, contained in this repository, can still be used while the network exists, however no maintenance or upgrades are planned for it.

Please read the [The sun is setting for the old Swarm](https://medium.com/ethereum-swarm/the-sun-is-setting-for-the-old-swarm-network-46cdc8048f8b) network blog post for more information and also how to reach out for help with migration.

### Compatibility of Bee with the first Swarm

No compatibility on the network layer with the first Ethereum Swarm implementation can be provided, mainly due to the migration in underlying network protocol from devp2p to libp2p. This means that a Bee node cannot join first Swarm network and vice versa. Migrating data is possible, please get in touch for more info on how to approach this. 🐝 

### How to get in touch

Please use any of the following channels for help with migration or any other questions:

The Swarm team is reachable on [Mattermost](http://beehive.ethswarm.org/).
Join the Swarm Orange Lounge on [Telegram](https://t.me/joinchat/GoVG8RHYjUpD_-bEnLC4EQ).
Follow us on [Twitter](https://twitter.com/ethswarm).

[![Travis](https://travis-ci.org/holisticode/swarm.svg?branch=master)](https://travis-ci.org/holisticode/swarm)
[![Gitter](https://badges.gitter.im/Join%20Chat.svg)](https://gitter.im/holisticode/orange-lounge?utm_source=badge&utm_medium=badge&utm_campaign=pr-badge)
[![](https://godoc.org/github.com/nathany/looper?status.svg)](https://godoc.org/github.com/holisticode/swarm/)


## Table of Contents  <!-- omit in toc -->

- [Building the source](#building-the-source)
- [Running Swarm](#running-swarm)
  - [Verifying that your local Swarm node is running](#verifying-that-your-local-swarm-node-is-running)
  - [Ethereum Name Service resolution](#ethereum-name-service-resolution)
- [Documentation](#documentation)
- [Docker](#docker)
  - [Docker tags](#docker-tags)
  - [Swarm command line arguments](#swarm-command-line-arguments)
- [Developers Guide](#developers-guide)
  - [Go Environment](#go-environment)
  - [Vendored Dependencies](#vendored-dependencies)
  - [Testing](#testing)
  - [Profiling Swarm](#profiling-swarm)
  - [Metrics and Instrumentation in Swarm](#metrics-and-instrumentation-in-swarm)
    - [Visualizing metrics](#visualizing-metrics)
- [Public Gateways](#public-gateways)
- [Swarm Dapps](#swarm-dapps)
- [Contributing](#contributing)
- [License](#license)

## Building the source

It's recommended to use Go 1.14 to build Swarm.

To simply compile the `swarm` binary without a `GOPATH`:

```bash
$ git clone https://github.com/holisticode/swarm
$ cd swarm
$ make swarm
```

You will find the binary under `./build/bin/swarm`.

To build a vendored `swarm` using `go get` you must have `GOPATH` set. Then run:

```bash
$ go get -d github.com/holisticode/swarm
$ go install github.com/holisticode/swarm/cmd/swarm
```

## Running Swarm

```bash
$ swarm
```

If you don't have an account yet, then you will be prompted to create one and secure it with a password:

```
Your new account is locked with a password. Please give a password. Do not forget this password.
Passphrase:
Repeat passphrase:
```

If you have multiple accounts created, then you'll have to choose one of the accounts by using the `--bzzaccount` flag.

```bash
$ swarm --bzzaccount <your-account-here>

# example
$ swarm --bzzaccount 2f1cd699b0bf461dcfbf0098ad8f5587b038f0f1
```

### Verifying that your local Swarm node is running

When running, Swarm is accessible through an HTTP API on port 8500.

Confirm that it is up and running by pointing your browser to http://localhost:8500

### Ethereum Name Service resolution

The Ethereum Name Service is the Ethereum equivalent of DNS in the classic web. In order to use ENS to resolve names to Swarm content hashes (e.g. `bzz://theswarm.eth`), `swarm` has to connect to a `geth` instance, which is synced with the Ethereum mainnet. This is done using the `--ens-api` flag.

```bash
$ swarm --bzzaccount <your-account-here> \
        --ens-api '$HOME/.ethereum/geth.ipc'

# in our example
$ swarm --bzzaccount 2f1cd699b0bf461dcfbf0098ad8f5587b038f0f1 \
        --ens-api '$HOME/.ethereum/geth.ipc'
```

For more information on usage, features or command line flags, please consult the Documentation.

## Documentation

Swarm documentation can be found at [https://swarm-guide.readthedocs.io](https://swarm-guide.readthedocs.io).

## Docker

Swarm container images are available at Docker Hub: [holisticode/swarm](https://hub.docker.com/r/holisticode/swarm)

### Docker tags

* `latest` - latest stable release
* `edge` - latest build from `master`
* `v0.x.y` - specific stable release

### Swarm command line arguments

All Swarm command line arguments are supported and can be sent as part of the CMD field to the Docker container.

**Examples:**

Running a Swarm container from the command line

```bash
$ docker run -it holisticode/swarm \
                            --debug \
                            --verbosity 4
```


Running a Swarm container with custom ENS endpoint

```bash
$ docker run -it holisticode/swarm \
                            --ens-api http://1.2.3.4:8545 \
                            --debug \
                            --verbosity 4
```

Running a Swarm container with metrics enabled

```bash
$ docker run -it holisticode/swarm \
                            --debug \
                            --metrics \
                            --metrics.influxdb.export \
                            --metrics.influxdb.endpoint "http://localhost:8086" \
                            --metrics.influxdb.username "user" \
                            --metrics.influxdb.password "pass" \
                            --metrics.influxdb.database "metrics" \
                            --metrics.influxdb.host.tag "localhost" \
                            --verbosity 4
```

Running a Swarm container with tracing and pprof server enabled

```bash
$ docker run -it holisticode/swarm \
                            --debug \
                            --tracing \
                            --tracing.endpoint 127.0.0.1:6831 \
                            --tracing.svc myswarm \
                            --pprof \
                            --pprofaddr 0.0.0.0 \
                            --pprofport 6060
```

Running a Swarm container with a custom data directory mounted from a volume and a password file to unlock the swarm account

```bash
$ docker run -it -v $PWD/hostdata:/data \
                 -v $PWD/password:/password \
                 holisticode/swarm \
                            --datadir /data \
                            --password /password \
                            --debug \
                            --verbosity 4
```

## Developers Guide

### Go Environment

We assume that you have Go v1.11 installed, and `GOPATH` is set.

You must have your working copy under `$GOPATH/src/github.com/holisticode/swarm`.

Most likely you will be working from your fork of `swarm`, let's say from `github.com/nirname/swarm`. Clone or move your fork into the right place:

```bash
$ git clone git@github.com:nirname/swarm.git $GOPATH/src/github.com/holisticode/swarm
```


### Vendored Dependencies

Vendoring is done by Makefile rule `make vendor` which uses `go mod vendor` and additionally copies cgo dependencies into `vendor` directory from go modules cache.

If you want to add a new dependency, run `go get <import-path>`, vendor it `make vendor`, then commit the result.

If you want to update all dependencies to their latest upstream version, run `go get -u all` and vendor them with `make vendor`.

By default, `go` tool will use dependencies defined in `go.mod` file from modules cache. In order to import code from `vendor` directory, an additional flag `-mod=vendor` must be provided when calling `go run`, `go test`, `go build` and `go install`. If `vendor` directory is in sync with `go.mod` file by updating it with `make vendor`, there should be no difference to use the flag or not. All Swarm build tools are using code only from the `vendor` directory and it is encouraged to do the same in the development process, as well.


### Testing

This section explains how to run unit, integration, and end-to-end tests in your development sandbox.

Testing one library:

```bash
$ go test -v -cpu 4 ./api
```

Note: Using options -cpu (number of cores allowed) and -v (logging even if no error) is recommended.

Testing only some methods:

```bash
$ go test -v -cpu 4 ./api -run TestMethod
```

Note: here all tests with prefix TestMethod will be run, so if you got TestMethod, TestMethod1, then both!

Running benchmarks:

```bash
$ go test -v -cpu 4 -bench . -run BenchmarkJoin
```


### Profiling Swarm

This section explains how to add Go `pprof` profiler to Swarm

If `swarm` is started with the `--pprof` option, a debugging HTTP server is made available on port 6060.

You can bring up http://localhost:6060/debug/pprof to see the heap, running routines etc.

By clicking full goroutine stack dump (clicking http://localhost:6060/debug/pprof/goroutine?debug=2) you can generate trace that is useful for debugging.


### Metrics and Instrumentation in Swarm

This section explains how to visualize and use existing Swarm metrics and how to instrument Swarm with a new metric.

Swarm metrics system is based on the `go-metrics` library.

The most common types of measurements we use in Swarm are `counters` and `resetting timers`. Consult the `go-metrics` documentation for full reference of available types.

```go
// incrementing a counter
metrics.GetOrRegisterCounter("network/stream/received_chunks", nil).Inc(1)

// measuring latency with a resetting timer
start := time.Now()
t := metrics.GetOrRegisterResettingTimer("http/request/GET/time"), nil)
...
t := UpdateSince(start)
```

#### Visualizing metrics

Swarm supports an InfluxDB exporter. Consult the help section to learn about the command line arguments used to configure it:

```bash
$ swarm --help | grep metrics
```

We use Grafana and InfluxDB to visualise metrics reported by Swarm. We keep our Grafana dashboards under version control at https://github.com/holisticode/grafana-dashboards. You could use them or design your own.

We have built a tool to help with automatic start of Grafana and InfluxDB and provisioning of dashboards at https://github.com/nonsense/stateth, which requires that you have Docker installed.

Once you have `stateth` installed, and you have Docker running locally, you have to:

1. Run `stateth` and keep it running in the background

```bash
$ stateth --rm --grafana-dashboards-folder $GOPATH/src/github.com/holisticode/grafana-dashboards --influxdb-database metrics
```

2. Run `swarm` with at least the following params:

```bash
--metrics \
--metrics.influxdb.export \
--metrics.influxdb.endpoint "http://localhost:8086" \
--metrics.influxdb.username "admin" \
--metrics.influxdb.password "admin" \
--metrics.influxdb.database "metrics"
```

3. Open Grafana at http://localhost:3000 and view the dashboards to gain insight into Swarm.


## Public Gateways

Swarm offers a local HTTP proxy API that Dapps can use to interact with Swarm. The Ethereum Foundation is hosting a public gateway, which allows free access so that people can try Swarm without running their own node.

The Swarm public gateways are temporary and users should not rely on their existence for production services.

The Swarm public gateway can be found at https://swarm-gateways.net and is always running the latest `stable` Swarm release.

## Swarm Dapps

You can find a few reference Swarm decentralised applications at: https://swarm-gateways.net/bzz:/swarmapps.eth

Their source code can be found at: https://github.com/holisticode/swarm-dapps

## Contributing

Thank you for considering to help out with the source code! We welcome contributions from
anyone on the internet, and are grateful for even the smallest of fixes!

If you'd like to contribute to Swarm, please fork, fix, commit and send a pull request
for the maintainers to review and merge into the main code base. If you wish to submit more
complex changes though, please check up with the core devs first on [our Swarm gitter channel](https://gitter.im/holisticode/orange-lounge)
to ensure those changes are in line with the general philosophy of the project and/or get some
early feedback which can make both your efforts much lighter as well as our review and merge
procedures quick and simple.

Please make sure your contributions adhere to our coding guidelines:

 * Code must adhere to the official Go [formatting](https://golang.org/doc/effective_go.html#formatting) guidelines (i.e. uses [gofmt](https://golang.org/cmd/gofmt/)).
 * Code must be documented adhering to the official Go [commentary](https://golang.org/doc/effective_go.html#commentary) guidelines.
 * Pull requests need to be based on and opened against the `master` branch.
 * [Code review guidelines](https://github.com/holisticode/swarm/blob/master/docs/Code-Review-Guidelines.md).
 * Commit messages should be prefixed with the package(s) they modify.
   * E.g. "fuse: ignore default manifest entry"


## License

The swarm library (i.e. all code outside of the `cmd` directory) is licensed under the
[GNU Lesser General Public License v3.0](https://www.gnu.org/licenses/lgpl-3.0.en.html), also
included in our repository in the `COPYING.LESSER` file.

The swarm binaries (i.e. all code inside of the `cmd` directory) is licensed under the
[GNU General Public License v3.0](https://www.gnu.org/licenses/gpl-3.0.en.html), also included
in our repository in the `COPYING` file.
