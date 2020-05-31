package main

// Manages information about upstream servers

import (
	"fmt"
	"time"
)

type UpstreamName string
type UpstreamWeight float64
type Upstream struct {
	// The hostname of the upstream
	Name UpstreamName

	// The port to connect to
	Port int

	// The current weight score of this upstream
	weight UpstreamWeight

	// if set and in the future, wait for this time before making connections
	wakeupTime time.Time
}

func (u *Upstream) GetAddress() string {
	port := u.Port
	if port == 0 {
		port = 853
	}
	return fmt.Sprintf("%s:%d", u.Name, port)
}

func (u *Upstream) GetWeight() UpstreamWeight {
	return u.weight
}

func (u *Upstream) SetWeight(w UpstreamWeight) {
	u.weight = w
}

// Sets a wakeup time until which no connections should be made.
func (u *Upstream) Cooldown(t time.Duration) {
	u.wakeupTime = time.Now().Add(t)
}

// returns whether or not the wakeupTime is after the current time
// if so, this upstream shouldn't get more connections.
func (u *Upstream) IsCooling() (ret bool) {
	return u.wakeupTime.After(time.Now())
}

// returns the actual time when this upstream will be ready for connections.
func (u *Upstream) WakeupTime() (wakeupTime time.Time) {
	return u.wakeupTime
}
