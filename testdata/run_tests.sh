#!
# script for running dnsperf on container startup
echo "baselining blackhole server..."
dnsperf -s 172.16.0.69 -m tls < /app/junkdomains

echo "running test"
dnsperf -s 172.16.0.70 -m udp < /app/junkdomains

echo "outputting metrics"
curl 172.16.0.70:54321/metrics
