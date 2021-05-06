#!/usr/bin/env python3
# Copyright(c) 2021 by craftyguy "Clayton Craft" <clayton@craftyguy.net>
# Distributed under GPLv3+ (see COPYING) WITHOUT ANY WARRANTY.
import argparse
import configparser
import grp
import logging
import os
import signal
import stat
import trio

from logging.handlers import SysLogHandler

from .logger import LoggedException
from .stm_agps import STM_AGPS

# List of NMEA prefixes to send to clients.
# This list is the same one that geoclue monitors.
GNSS_PREFIXES = {
    b"$GAGGA",  # Galieo
    b"$GBGGA",  # BeiDou
    b"$BDGGA",  # BeiDou
    b"$GLGGA",  # GLONASS
    b"$GNGGA",  # GNSS (combined)
    b"$GPGGA",  # GPS, SBAS, QZSS
}

# new root logger
logger = logging.getLogger("gnss_share")


class GnssShare:
    drivers = {
        'stm': STM_AGPS
    }

    def __init__(self, config):
        self.__log = logging.getLogger(__name__)
        # References to open connections are stored here
        self._open_connections = []
        # Reference to open device/driver
        self._active_driver = None
        # Holds the last NMEA location sentence retrieved from the device
        self._location = b""
        self._socket_path = config['gnss_share'].get('socket')
        self._socket_owner_group = config['gnss_share'].get('group')
        self._device_path = config['gnss_share'].get('device_path')
        self._agps_dir = config['gnss_share'].get('agps_directory')
        driver = config['gnss_share'].get('device_driver')

        if driver not in self.drivers:
            raise LoggedException(f"Driver {driver} is unknown!")

        if not os.path.exists(self._device_path):
            raise LoggedException("Serial device not found: "
                                  f"{self._device_path}")

        os.makedirs(self._agps_dir, exist_ok=True)

        self._driver = self.drivers[driver]

    async def load(self, task_status=trio.TASK_STATUS_IGNORED):
        """ Wrapper for running driver.load() """
        self.__log.info("running load()")

        async with self._driver(self._device_path) as driver:
            await driver.load(self._agps_dir)

        # signal to nursery that this task is done (if not called from nursery,
        # this is a noop)
        task_status.started()

    async def store(self):
        """ Wrapper for running driver.store() """
        self.__log.info("running store()")

        async with self._driver(self._device_path) as driver:
            await driver.store(self._agps_dir)

    async def _get_location(self):
        """ Returns last received location from gnss driver, or None """
        if not self._active_driver:
            self.__log.warn("Tried to read from inactive device!")
            return b""
        return self._location

    async def _handle_socket_connection(self, conn):
        """ Handler for client connections over a socket """
        self.__log.info(f"Got new connection from client on "
                        f"{conn.socket.getsockname()}")
        self._open_connections.append(conn)
        try:
            while True:
                location = await self._get_location()
                if location:
                    await conn.send_all(location)
                # Send data to clients at this rate.
                # TODO: is this too fast or too slow?
                await trio.sleep(1)
        except trio.BrokenResourceError:
            self.__log.info("A socket client disconnected")
        finally:
            self._open_connections.remove(conn)
            await conn.aclose()
            self.__log.info("Number of connected clients: "
                            f"{len(self._open_connections)}")

    async def _start_sock_server(self):
        """ Task to start a socket server and listen for new connections """
        if os.path.exists(self._socket_path):
            if (os.path.islink(self._socket_path)
                    or os.path.isfile(self._socket_path)):
                raise LoggedException("Unable to use existing file as socket: "
                                      f"{self._socket_path}")
            os.remove(self._socket_path)

        self._sock = trio.socket.socket(trio.socket.AF_UNIX,
                                        trio.socket.SOCK_STREAM)
        await self._sock.bind(self._socket_path)

        # set socket group owner and make R/W
        os.chown(self._socket_path, os.getuid(),
                 grp.getgrnam(self._socket_owner_group).gr_gid)
        os.chmod(self._socket_path, stat.S_IRWXG | stat.S_IRWXU)

        self._sock.listen()
        await trio.serve_listeners(self._handle_socket_connection,
                                   [trio.SocketListener(self._sock)])

    async def _run_loop(self):
        """
        Main loop, for managing the gnss driver and getting data for clients
        """
        while True:
            if len(self._open_connections) > 0:
                if self._active_driver is None:
                    self._active_driver = self._driver(self._device_path)
                    await self._active_driver.open()
                line = await self._active_driver.readline()
                prefix = line.split(b',')[0]
                if prefix in GNSS_PREFIXES:
                    self._location = line
                # polling loop delay when clients are connected
                # TODO: is this adequate? Any faster and CPU utilization
                # climbs...
                await trio.sleep(.2)
            else:
                if self._active_driver is not None:
                    self.__log.info("No more clients connected, closing "
                                    "device")
                    await self._active_driver.close()
                    self._active_driver = None
                # polling loop delay when no clients are connected
                # TODO: is this adequate?
                await trio.sleep(2)

    async def _signal_receiver(self):
        """
        Catch specific signals, and raise a SystemExit exception to stop
        the parent nursery
        """
        with trio.open_signal_receiver(signal.SIGTERM,
                                       signal.SIGINT) as signal_aiter:
            async for sig in signal_aiter:
                self.__log.info(f"Caught signal: {sig}")
                if sig == signal.SIGINT:
                    self.__log.warn("****************************************")
                    self.__log.warn("App exit requested.. hit C-c to "
                                    "quit now.")
                    self.__log.warn("****************************************")
                raise SystemExit

    async def run(self):
        try:
            async with trio.open_nursery() as nursery:
                # start socket server as soon as possible so it can accept
                # connections
                nursery.start_soon(self._start_sock_server)

                # load() should complete before the main loop, which reads from
                # the gnss device. So it's started right now, and blocks
                await nursery.start(self.load)

                # signal reciever started here, since calling store() before
                # load() doesn't make sense
                nursery.start_soon(self._signal_receiver)

                # main loop
                nursery.start_soon(self._run_loop)

        except (SystemExit):
            # Only store() on SystemExit, since it is thrown when certain
            # signals are received. Everything else might lead to
            # inconsistent/incomplete agps data
            await self.store()


def main(version=None):
    parser = argparse.ArgumentParser(description='Manager for GNSS devices')
    parser.add_argument('--version', action='version',
                        version=f"version: {version}")
    parser.add_argument('--syslog', action='store_true',
                        help=('Write output to syslog instead of stdout'))
    parser.add_argument('-v', '--verbose', action='store_true',
                        help=("Enable verbose output"))
    group = parser.add_mutually_exclusive_group()
    parser.add_argument('-c', '--config', default='/etc/gnss_share.conf',
                        help=('Configuration file to use '
                              '(default: %(default)s).'))
    group.add_argument('-s', '--store', nargs='?',
                       const='/var/cache/gnss_share',
                       help=('Dump almanac and ephemeris data. A directory '
                             'can be specified (default: %(const)s).'))
    group.add_argument('-l', '--load', nargs='?',
                       const='/var/cache/gnss_share',
                       help=('Load almanac and ephemeris data. A directory '
                             'can be specified (default: %(const)s). '
                             'Note: this expects almanac data to be in a '
                             ' file "almanac.txt", and ephemeris in a file '
                             '"ephemeris.txt"'))

    args = parser.parse_args()

    if args.syslog:
        # syslog socket on Linux
        syslog_handler = SysLogHandler('/dev/log')
        # syslog uses first word as tag
        syslog_handler.setFormatter(
            logging.Formatter('%(name)s %(levelname)s: %(message)s'))
        syslog_handler.setLevel(logging.DEBUG)
        logger.addHandler(syslog_handler)
    else:
        logger.addHandler(logging.StreamHandler())

    if args.verbose:
        logger.setLevel(logging.INFO)

    config_file = None
    for file in [args.config, './gnss_share.conf']:
        if os.path.exists(file):
            config_file = file
            break
    if not config_file:
        raise LoggedException("Unable to find config file at: \n"
                              f"{args.config}\n"
                              "./gnss_share.conf")
    logger.info(f"Using configuration file: {config_file}")
    config = configparser.ConfigParser()
    config.read(config_file)

    gnss_share = GnssShare(config)
    if args.load:
        trio.run(gnss_share.load)
    elif args.store:
        trio.run(gnss_share.store)
    else:
        trio.run(gnss_share.run)


if __name__ == "__main__":
    main()
