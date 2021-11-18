// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"strconv"
	"time"

	"gitlab.com/postmarketOS/gnss_share/internal/config"
	"gitlab.com/postmarketOS/gnss_share/internal/gnss"
	"gitlab.com/postmarketOS/gnss_share/internal/pool"
)

func usage() {
	flag.CommandLine.Usage()
}

func main() {
	var confFile string
	flag.StringVar(&confFile, "c", "/etc/gnss_share.conf", "Configuration file to use.")
	var help bool
	flag.BoolVar(&help, "h", false, "Print help and quit.")

	flag.Usage = func() {
		fmt.Println("usage: gnss_share COMMAND [OPTION...]")
		fmt.Println("Commands:")
		fmt.Printf("  %-12s\t%s\n", "[none]", "The default behavior if no command is specified is to run in daemon mode.")
		fmt.Printf("  %-12s\t%s\n", "store", "Store almanac and ephemerides data and quit.")
		fmt.Printf("  %-12s\t%s\n", "load", "Load almanac and ephemerides data and quit.")
		fmt.Println("Options:")
		flag.PrintDefaults()
	}

	flag.Parse()

	if help {
		usage()
		return
	}

	conf, err := config.Parse(confFile)
	if err != nil {
		log.Fatal(err)
	}

	var driver gnss.GnssDriver

	switch conf.Driver {
	case "stm":
		driver = gnss.NewStm(conf.DevicePath)
	case "stm_serial":
		driver = gnss.NewStmSerial(conf.DevicePath, conf.BaudRate)
	}

	switch cmd := flag.Arg(0); cmd {
	case "store":
		err := driver.Save(conf.CachePath)
		if err != nil {
			log.Fatal(err)
		}
		return
	case "load":
		err := driver.Load(conf.CachePath)
		if err != nil {
			log.Fatal(err)
		}
		return
	default:
		if flag.Arg(0) != "" {
			fmt.Printf("Unknown command: %q\n", flag.Arg(0))
			usage()
			return
		}
		// run mode
	}

	if err := startServer(conf, &driver); err != nil {
		log.Fatal(err)
	}

}

func startServer(conf *config.Config, driver *gnss.GnssDriver) error {
	// load 'driver'
	// start device if a connection is made
	// stop device if all connections are disconnected (use ref counting?)
	// make new 'server' struct ?

	if err := os.RemoveAll(conf.Socket); err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	sock, err := net.Listen("unix", conf.Socket)
	if err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}
	defer sock.Close()

	if err := os.Chmod(conf.Socket, 0660); err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	group, err := user.LookupGroup(conf.OwnerGroup)
	if err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	gid, err := strconv.ParseInt(group.Gid, 10, 16)
	if err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	if err := os.Chown(conf.Socket, -1, int(gid)); err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	connPool := pool.New()
	go connPool.Start()

	stopChan := make(chan bool)
	errChan := make(chan error)

	// manage the gps device based on whether clients are connected or not
	go func() {
		var count int
		fmt.Printf("Starting GNSS server, accepting connections at: %s\n", conf.Socket)
		for {
			select {
			case err = <-errChan:
				log.Fatal(err)
			default:
				newCount := len(connPool.Clients)
				if newCount != count {
					if newCount > count {
						fmt.Println("New client connected")
					} else if newCount < count {
						fmt.Println("Client disconnected")
						if newCount == 0 {
							fmt.Println("No clients connected, closing GNSS")
							stopChan <- true
						}
					}
					fmt.Printf("Total connected clients: %d\n", len(connPool.Clients))

					// start gnss driver only when the first client (from 0
					// current connections, that is) connects
					if count == 0 && newCount == 1 {
						go (*driver).Start(connPool.Broadcast, stopChan, errChan)
					}
				}
				count = newCount
			}

			time.Sleep(time.Second * 1)
		}
	}()

	// start socket connection handler
	for {
		conn, err := sock.Accept()
		if err != nil {
			log.Fatal(err)
		}

		client := pool.Client{
			Conn: &conn,
			Send: make(chan []byte),
		}

		connPool.Register <- &client

		go handleConnection(connPool, &client)
	}
}

func handleConnection(p *pool.Pool, c *pool.Client) {
	defer func() {
		p.Unregister <- c
		(*c.Conn).Close()
	}()

	for {
		msg := <-c.Send
		if _, err := (*c.Conn).Write(msg); err != nil {
			return
		}
	}
}
