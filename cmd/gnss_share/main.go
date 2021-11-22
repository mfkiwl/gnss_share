// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"
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
		fmt.Printf("  %-12s\t%s\n", "[none]", "The default behavior if no command is specified is to run in \"server\" mode.")
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

	// connection broadcast pool
	connPool := pool.New()
	go connPool.Start()

	// connection handler
	fmt.Printf("Starting GNSS server, accepting connections at: %s\n", conf.Socket)
	go func() {
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
	}()

	// driver manager
	stopChan := make(chan bool)
	errChan := make(chan error)
	// register SIGUSR1/2 for handling load/store of almanac/ephemeris data
	// on-demand
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1, syscall.SIGUSR2)

	var oldCount int
	for {
		connCount := len(connPool.Clients)
		select {
		case err = <-errChan:
			log.Fatal(err)
		case sig := <-sigChan:
			switch sig {
			case syscall.SIGUSR1:
				fmt.Printf("received SIGUSR1, loading data from %q\n", conf.CachePath)
				active := connCount > 0
				// stop receiving/broadcasting location data from gnss
				// device
				if active {
					stopChan <- true
				}

				if err := (*driver).Load(conf.CachePath); err != nil {
					// not fatal
					fmt.Printf("error loading data: %s\n", err)
				}

				// resume receiving/broadcasting location data from gnss
				// device
				if active {
					go (*driver).Start(connPool.Broadcast, stopChan, errChan)
				}
			case syscall.SIGUSR2:
				fmt.Printf("received SIGUSR2, storing data to %q\n", conf.CachePath)
				active := connCount > 0
				// stop receiving/broadcasting location data from gnss
				// device
				if active {
					stopChan <- true
				}

				if err := (*driver).Save(conf.CachePath); err != nil {
					// not fatal
					fmt.Printf("error loading data: %s\n", err)
				}

				// resume receiving/broadcasting location data from gnss
				// device
				if active {
					go (*driver).Start(connPool.Broadcast, stopChan, errChan)
				}
			}
		default:
			if connCount != oldCount {
				if connCount > oldCount {
					fmt.Println("New client connected")
				} else if connCount < oldCount {
					fmt.Println("Client disconnected")
					if connCount == 0 {
						fmt.Println("No clients connected, closing GNSS")
						stopChan <- true
					}
				}
				fmt.Printf("Total connected clients: %d\n", len(connPool.Clients))

				// start gnss driver only when the first client (from 0
				// current connections, that is) connects
				if oldCount == 0 && connCount == 1 {
					go (*driver).Start(connPool.Broadcast, stopChan, errChan)
				}
			}
			oldCount = connCount
			time.Sleep(time.Second * 1)
		}

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
