package main

import (
	"github.com/miekg/dns"
)

type PipelineFinisher struct {
	pipelineServerWorker

	// dedicated channel for servfails, this makes
	// the code more readable than just overriding one of the other channels
	servfailsChannel chan Query
}

func (p *PipelineFinisher) Start() {
	go func() {
		for {
			select {
			case <-p.cancelChannel:
				logCancellation("PipelineFinisher")
				return
			case q := <-p.servfailsChannel:
				failingQuery := q
				failingQuery.Reply = &dns.Msg{}
				failingQuery.Reply.SetRcode(failingQuery.Msg, dns.RcodeServerFailure)
				if err := q.W.WriteMsg(q.Reply); err != nil {
					Logger.Log(LogMessage{
						Level: CRITICAL,
						Context: LogContext{
							"what":  "failed to write servfail reply to client",
							"error": err.Error(),
							"reply": q.Reply.String(),
						},
					})
				}
				duration := q.Timer.ObserveDuration()
				logQuery("servfail", duration, failingQuery.Reply)
				go p.Fail(q)
			case q := <-p.inboundQueryChannel:
				if err := q.W.WriteMsg(q.Reply); err != nil {
					Logger.Log(LogMessage{
						Level: CRITICAL,
						Context: LogContext{
							"what":  "failed to write reply to client",
							"error": err.Error(),
							"reply": q.Reply.String(),
						},
					})
				}
				duration := q.Timer.ObserveDuration()
				logQuery(q.Upstream.GetAddress(), duration, q.Reply)
				if q.Conn != nil {
					// dispatch for readdition into the conn pool
					go p.Dispatch(q)
				}
			}
		}
	}()
}
