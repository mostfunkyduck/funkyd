# FunkyD

## A TLS-by-design DNS server and forwarding resolver

FunkyD has the following goals:
 * Provide a full featured, lightweight DNS server that will always use DNS-over-TLS for outbound queries (not DoH, DoH is for chumps).
 * Provide a HTTP API for configuration, eventually allowing comparable features to cloud services like Route53 (meaning that you can add and remove records dynamically over the API, list hosted zones, stuff like that).  
 * Provide robust telemetry for monitoring and observing the system
 * Provide the ability to use a distributed cache for use in a clustered environment

## Setup
Currently, the project does not do its own installation.  It requires the following:
1. a link to /etc/systemd/system for the unit script.  If you are so fortunate as to not be using systemd, just script calling the binary with the conf file and share the result!
1. the `funkyd` binary should be in `/usr/local/sbin`
1. A JSON config file in /etc/funkyd.conf.  The syntax isn't documented yet, but can be seen in `config.go`
1. If log files are defined, they must exist

Eventually a container will exist that will make this easier

## Testing
The `Dockerfile` and `docker-compose.yml` in this repository are for running benchmark tests on funkyd.  Using assets in the repo, the compose file will start up a test server that points to a blackhole server that discards all inbound queries. There is then `Dockerfile.dnsperf`, which builds a local container with dnsperf installed that can be run to automatically launch tests on both the blackhole and the testbox.
For example:
```
sudo docker build -t mine/dnsperf -f ./Dockerfile.dnsperf .
sudo docker-compose up -d
sudo docker run mine/dnsperf
```
Benchmarks can be added by configuring `test/run_tests.sh` to run more tests.  For additional testing configurations, add them to test/funkyd/ and then add a service in the docker-compose file that will start a container with that configuration on a set IP.

## Should I use this?
Can't think of a reason not to!

## Current Goals
1. routing options, allow discarding internal traffic
1. Needs more DNS features, especially DNSSEC
1. HTTP API still not implemented
1. Cache is still local and uses an unbounded amount of memory
1. There's probably more interesting telemetry we can add
1. Needs benchmarking against other solutions with comparable functionality
1. Needs benchmarking to see how it performs in general - mainly resource usage and latency
1. Docker container
