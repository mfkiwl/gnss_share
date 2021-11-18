// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package gnss

type GnssDriver interface {
	Load(dir string) (err error)
	Save(dir string) (err error)

	Start(sendCh chan<- []byte, stop <-chan bool, errCh chan<- error)
}

type GnssLine struct {
	Line  []byte
	Error error
}
