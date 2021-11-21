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

Run with `-h` to see the list of supported command line options.


### NOTE: the following signal functionality is not yet implemented:
In addition to the command line options, this application will respond to the
following signals:

- `SIGUSR` - The application will store AGPS data to file, and continue running

- `SIGTERM` - Application will store AGPS data to file, and quit

- `SIGINT` - Application will store AGPS data to file, and quit. Sending a
  subsequent `SIGINT` will cause it to quit immediately.

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
