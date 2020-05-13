package main

import (
	"fmt"
)

func (u *Upstream) GetAddress() string {
	port := u.Port
	if port == 0 {
		port = 853
	}
	return fmt.Sprintf("%s:%d", u.Name, port)
}
