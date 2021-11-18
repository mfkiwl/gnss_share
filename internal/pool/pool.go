// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package pool

import (
	"net"
)

type Client struct {
	Send chan []byte
	Conn *net.Conn
}

type Pool struct {
	Register   chan *Client
	Unregister chan *Client
	Clients    map[*Client]bool
	Broadcast  chan []byte
}

func New() *Pool {
	return &Pool{
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Clients:    make(map[*Client]bool),
		Broadcast:  make(chan []byte),
	}
}

func (p *Pool) Start() {
	for {
		select {
		case c := <-p.Register:
			p.Clients[c] = true
		case c := <-p.Unregister:
			delete(p.Clients, c)
		case msg := <-p.Broadcast:
			for c := range p.Clients {
				c.Send <- append(msg, byte('\n'))
			}
		}
	}
}
