// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"

	"gitlab.com/postmarketOS/gnss_share/internal/pool"
)

type Server struct {
	socket    string
	sockGroup string
	connPool  *pool.Pool
	sock      net.Listener
	startChan chan<- bool
	stopChan  chan<- bool
}

// Create a new Server. The server will send 'true' to startChan when the first
// client connects, and 'true' to stopChan when the last client disconnects.
// Messages received from the connPool are forwarded to the connected clients.
func New(socket string, sockGroup string, startChan chan<- bool, stopChan chan<- bool, connPool *pool.Pool) (s *Server) {
	s = &Server{
		socket:    socket,
		sockGroup: sockGroup,
		startChan: startChan,
		stopChan:  stopChan,
		connPool:  connPool,
	}

	return
}

func (s *Server) Start() (err error) {
	if err := os.RemoveAll(s.socket); err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	s.sock, err = net.Listen("unix", s.socket)
	if err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}
	defer s.sock.Close()

	if err := os.Chmod(s.socket, 0660); err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	group, err := user.LookupGroup(s.sockGroup)
	if err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	gid, err := strconv.ParseInt(group.Gid, 10, 16)
	if err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	if err := os.Chown(s.socket, -1, int(gid)); err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	// connection handler
	fmt.Printf("Starting GNSS server, accepting connections at: %s\n", s.socket)
	go s.connectionHandler()

	s.connectionHandler()
	return nil
}

func (s *Server) connectionHandler() error {
	for {
		conn, err := (s.sock).Accept()
		if err != nil {
			return fmt.Errorf("server.connectionHandler: %w", err)
		}

		client := pool.Client{
			Conn: &conn,
			Send: make(chan []byte),
		}

		if len(s.connPool.Clients) == 0 {
			// client is first one in the connPool
			s.startChan <- true
		}

		s.connPool.Register <- &client

		go s.clientConnection(&client)

		fmt.Println("New client connected")

	}
}

// Routine run for each client connection
func (s *Server) clientConnection(c *pool.Client) {
	defer func() {
		s.connPool.Unregister <- c
		(*c.Conn).Close()
	}()

	for {
		msg := <-c.Send
		if _, err := (*c.Conn).Write(msg); err != nil {
			break
		}
	}

	// client disconnected
	fmt.Println("Client disconnected")
	if len(s.connPool.Clients) == 1 {
		// client is last one in the pool
		fmt.Println("No clients connected, closing GNSS")
		s.stopChan <- true
	}
}
