1. Implement a 'pipeline server' that uses channel/gr pipelining to handle queries, just to see if it's faster and/or more readable
1. https://tools.ietf.org/html/rfc7766
  * Idle timeouts to prevent sudden connection closure and DoSing servers
  ```
     -  If the server needs to close a dormant connection to reclaim
        resources, it should wait until the connection has been idle for a
        period on the order of two minutes
  ```
  ```
       To mitigate the risk of unintentional server overload, DNS clients
   MUST take care to minimise the idle time of established DNS-over-TCP
   sessions made to any individual server.  DNS clients SHOULD close the
   TCP connection of an idle session, unless an idle timeout has been
   established using some other signalling mechanism, for example,
   [edns-tcp-keepalive].
  ```
  * we won't comply with this suggestion
  ```
     There is no clear guidance today in any RFC as to when a DNS client
   should close a TCP connection, and there are no specific
   recommendations with regard to DNS client idle timeouts.  However, at
   the time of writing, it is common practice for clients to close the
   TCP connection after sending a single request (apart from the SOA/
   AXFR case).
  ```
  * pipelining
  ```
     In order to achieve performance on par with UDP, DNS clients SHOULD
   pipeline their queries.  When a DNS client sends multiple queries to
   a server, it SHOULD NOT wait for an outstanding reply before sending
   the next query.  Clients SHOULD treat TCP and UDP equivalently when
   considering the time at which to send a particular query.
  ```
  * connection/upstream limit (note that the servers seem to be comfortable with more than 1 connection and even with pipelining, I'm pretty sure multiple connections will always outperform)
  ```
  It is RECOMMENDED that for any given
   client/server interaction there SHOULD be no more than one connection
   for regular queries, one for zone transfers, and one for each
   protocol that is being used on top of TCP (for example, if the
   resolver was using TLS).
  ```
  * TCP Fast Open? recommended by google and referred to in the spec as 'non-normative', might be fun
  * Not really normal for my use case, but ACL?
  ```
     Operators of recursive servers are advised to ensure that they only
   accept connections from expected clients (for example, by the use of
   an Access Control List (ACL)) and do not accept them from unknown
   sources.
  ```
1. routing options, allow discarding internal traffic
1. Needs more DNS features, especially DNSSEC
1. HTTP API still not implemented
1. Cache is still local and uses an unbounded amount of memory
1. There's probably more interesting telemetry we can add
1. Needs benchmarking against other solutions with comparable functionality
1. Needs benchmarking to see how it performs in general - mainly resource usage and latency
1. ~Docker container~
