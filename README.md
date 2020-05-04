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

## Wait a sec, can't you do this with $otherproject?
Perhaps. This is a hobby project, anything it does beyond what other projects do is a fun bonus.  That being said, there's no reason it can't shamelessly rip off other projects.

## Should I use this in production?
Maybe, if you're insane enough to deploy a beta-level project into production

## Current Goals
1. Needs more DNS features, especially DNSSEC
1. HTTP API still not implemented
1. Cache is still local and uses an unbounded amount of memory
1. There's probably more interesting telemetry we can add
1. Needs benchmarking against other solutions with comparable functionality
1. Needs benchmarking to see how it performs in general - mainly resource usage and latency
1. Docker container
