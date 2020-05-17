#!/bin/bash
# you want it to be interactive so it can ctrl-c
sudo docker run -it --network funkyd_testnetwork funkyd/dnsperf
