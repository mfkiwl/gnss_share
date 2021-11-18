// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"

	ini "github.com/subpop/go-ini"
)

type Config struct {
	Socket     string `ini:"socket"`
	OwnerGroup string `ini:"group"`
	Driver     string `ini:"device_driver"`
	DevicePath string `ini:"device_path"`
	BaudRate   int    `ini:"device_baud_rate"`
	CachePath  string `ini:"agps_directory"`
}

func Parse(file string) (c *Config, err error) {
	contents, err := os.ReadFile(file)
	if err != nil {
		err = fmt.Errorf("config.Parse(): %w", err)
		return
	}

	o := ini.Options{AllowNumberSignComments: true}

	c = &Config{}
	if err = ini.UnmarshalWithOptions(contents, c, o); err != nil {
		err = fmt.Errorf("config.Parse(): %w", err)
	}

	return
}
