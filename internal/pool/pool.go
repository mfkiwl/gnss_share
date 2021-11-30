// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package pool

import (
	"net"
	"sync"
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
	mu         sync.Mutex
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
			p.mu.Lock()
			p.Clients[c] = true
			p.mu.Unlock()
		case c := <-p.Unregister:
			p.mu.Lock()
			delete(p.Clients, c)
			p.mu.Unlock()
		case msg := <-p.Broadcast:
			msg = append(msg, byte('\n'))
			for c := range p.Clients {
				c.Send <- msg
			}
		}
	}
}

func (p *Pool) Count() (count int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	count = len(p.Clients)
	return
}
