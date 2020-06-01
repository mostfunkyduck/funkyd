package main

// PipelineConnector manages the connection pool and assigns connections to queries.
import (
	"fmt"
)

type PipelineConnector struct {
	pipelineServerWorker

	// connection pool
	connPool ConnPool

	// Client for making outbound connections
	client Client

	addingChannel chan Query

	closingChannel chan Query
}

func (c *PipelineConnector) AddUpstream(u *Upstream) {
	c.connPool.AddUpstream(u)
}

func (c *PipelineConnector) Start() {
	go func() {
		for {
			select {
			case query := <-c.addingChannel:
				if err := c.connPool.Add(query.Conn); err != nil {
					Logger.Log(LogMessage{
						Level: ERROR,
						Context: LogContext{
							"what":  "error adding connection to pool",
							"error": err.Error(),
							// this is expensive, but it's rare enough that it should be fine
							"query": fmt.Sprintf("%v", query),
						},
					})
				}
			case query := <-c.closingChannel:
				c.connPool.CloseConnection(query.Conn)
			case query := <-c.inboundQueryChannel:
				assignedQuery, err := c.AssignConnection(query)
				if err != nil {
					Logger.Log(LogMessage{
						Level: ERROR,
						Context: LogContext{
							"what":  "connection manager failed to assign connection to query",
							"query": assignedQuery.Msg.String(),
							"next":  "dispatching to be SERVFAILed",
						},
					})
					// fail to PipelineFinisher
					go c.Fail(query)
				}
				// dispatch to PipelineQuerier
				go c.Dispatch(assignedQuery)
			case <-c.cancelChannel:
				logCancellation("PipelineConnector")
				return
			}
		}
	}()
}

func (c *PipelineConnector) AssignConnection(q Query) (assignedQuery Query, err error) {
	assignedQuery = q
	connEntry, upstream := c.connPool.Get()
	if (upstream != Upstream{}) {
		var finalError error
		// we need to make a new connection
		retries := GetConfiguration().UpstreamRetries
		if retries == 0 {
			retries = 2
		}
		for i := assignedQuery.ConnectionRetries; i < retries; i++ {
			connEntry, err = c.connPool.NewConnection(upstream, c.client.Dial)
			assignedQuery.ConnectionRetries++
			if err != nil {
				Logger.Log(LogMessage{
					Level: WARNING,
					Context: LogContext{
						"what":     "failed to make connection to upstream",
						"attempt":  fmt.Sprintf("%d/%d", i, GetConfiguration().UpstreamRetries),
						"address":  upstream.GetAddress(),
						"upstream": Logger.Sprintf(DEBUG, "%v", upstream),
					},
				})
				finalError = fmt.Errorf("failed to connect to [%s]: %s: %s", upstream.GetAddress(), err, finalError)

				continue
			}

			break
		}
		if err != nil {
			return Query{}, fmt.Errorf("failed to make any connections to upstream %s: [%s]", upstream.GetAddress(), finalError)
		}
	}
	assignedQuery.Conn = connEntry
	return assignedQuery, nil
}
