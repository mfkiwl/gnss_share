An app for sharing GNSS location data, with support multiple clients and
loading/saving AGPS data.

This is meant to replace things like `gpsd`, and `gps-share`, and work together
with `geoclue`* or other clients that support fetching NMEA location data over
sockets.

*To work with `geoclue`, [these patches are required.](https://gitlab.freedesktop.org/geoclue/geoclue/-/merge_requests/79/diffs) A future version of
`geoclue` should work out of the box after pointing it to the socket created by
this app.

# Configuration

`gnss_share.conf` can be used to change the listening socket, group owner for
socket, and other options. The application looks for this file in either the
current working directory, or in `/etc/gnss_share.conf`.

See this file for descriptions of supported options.

# Usage

```
usage: gnss_share COMMAND [OPTION...]
Commands:
  [none]        The default behavior if no command is specified is to run in server mode.
  store         Store almanac and ephemerides data and quit.
  load          Load almanac and ephemerides data and quit.
Options:
  -c string
        Configuration file to use. (default "/etc/gnss_share.conf")
  -h    Print help and quit.
```

In addition to the command line options, this application will respond to the
following signals when in "server" mode:

- `SIGUSR1` - The application will load AGPS data from the directory
  `agps_directory` specified in the configuration file, and continue running
  afterward.

- `SIGUSR2` - The application will store AGPS data to the directory
  `agps_directory` specified in the configuration file, and continue running
  afterward.

# Installation

### Dependencies:

- Go

Build the `gnss_share` application with:

```
$ go build ./cmd/gnss_share
```

# Development

### New GNSS device support

Support for additional gnss devices can be added by implementing the
`gnss_driver` interface, see `internal/gnss/gnss.go` for specifics.
