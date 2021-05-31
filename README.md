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

# Installation

### Dependencies:

- [Trio](https://github.com/python-trio/trio)
- [Trio-Serial](https://github.com/joernheissler/trio-serial)
- [pynmea2](https://github.com/Knio/pynmea2)

This project uses meson to "build" and install things:

```
$ meson _build
$ meson install -C _build
```

To run locally from the source repo:

```
$ python3 -m gnss_share
```

# Development

### New GNSS device support

Support for additional gnss devices can be added by implementing a new 'driver', see `stm_agps.py` for an example.
At a minimum, `gnss_share` expects that the following methods are implemented in the driver:

- context manager support (`__aenter__`, `__aexit__`)
- `load(directory: str) -> None`
- `store(directory: str) -> None`
- `open() -> None`
- `close() -> None`
- `readline() -> bytes`
- `reset() -> None`
- `settime() -> None`

This application uses the Python Trio library for async coroutines, so blocking tasks in the driver must be made async.
