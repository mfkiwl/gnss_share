# Copyright(c) 2021 by Angus Ainslee
# Copyright(c) 2021 by Purism SPC
# Copyright(c) 2021 by craftyguy "Clayton Craft" <clayton@craftyguy.net>
# Distributed under GPLv3+ (see COPYING) WITHOUT ANY WARRANTY.
import logging
import os
import pynmea2
import trio
from datetime import datetime

from .logger import LoggedException


class STM_AGPS:

    def __init__(self, serial_port, baud=None):
        self.__log = logging.getLogger(__name__)
        if not os.path.exists(serial_port):
            raise LoggedException("Serial port does not exist: "
                                  f"{serial_port}")
        self._ser_port = serial_port
        self._location = b""
        self._buf = bytearray()

    async def __aenter__(self):
        await self.open()
        return self

    async def __aexit__(self, exc_type, exc_value, traceback):
        await self.close()

    async def close(self):
        await self._ser.aclose()

    async def open(self):
        try:
            self._ser = await trio.open_file(self._ser_port,
                                             "w+b", buffering=0)
        except Exception as e:
            raise LoggedException(e)

    async def readline(self):
        # based on this implementation of readline:
        # https://github.com/pyserial/pyserial/issues/216#issuecomment-369414522
        idx = self._buf.find(b'\n')
        if idx >= 0:
            line = self._buf[:idx+1]
            self._buf = bytearray(self._buf[idx+1:])
            return bytes(line)
        while True:
            data = await self._ser.receive_some(40)
            idx = data.find(b'\n')
            if idx >= 0:
                line = self._buf + data[:idx+1]
                self._buf = bytearray(data[idx+1:])
                return bytes(line)
            else:
                self._buf.extend(data)
            # sleep to prevent spinning faster than the device can write
            await trio.sleep(0.5)

    async def _write(self, data):
        await self._ser.write(data)

    async def _serial_write_cmd(self, cmd, expect=None):
        # number of times to poll serial output for ACK after sending command
        polling_loops = 50

        self.__log.info(f"cmd: {cmd}")
        await self._write(str(cmd).encode("ascii"))
        await self._write(b'\r\n')
        if expect:
            for i in range(polling_loops):
                line = await self.readline()
                line = line[:-1]
                self.__log.info(f"read: {line}")
                if expect.encode("ascii") in line:
                    self.__log.info(f"found: {expect}: {line}")
                    return True, line

            self.__log.info(f"not found: {expect}: {line}")
            return False, line

        # wait for cmd completion
        for i in range(polling_loops):
            line = await self.readline()
            line = line[:-1]
            self.__log.info(f"read: {line}")
            if str(cmd).encode("ascii") in line:
                return True, line

        return False, line

    async def _store_to_file(self, cmd, ack, file):
        msg = pynmea2.GGA('P', cmd, ())
        result, line = await self._serial_write_cmd(msg, ack)
        if not result:
            raise LoggedException("Unable to get data from device")
        async with await trio.open_file(file, 'wb') as f:
            while True:
                if cmd.encode() in line:
                    return
                if line.startswith(ack.encode()):
                    self.__log.info(line)
                    await f.write(line + b'\n')
                line = await self.readline()
                line = line[:-1]

    async def _load_from_file(self, ack, file):
        async with await trio.open_file(file, 'rb') as f:
            while line := await f.readline():
                await self._serial_write_cmd(line.strip(), ack)

    async def reset(self):
        msg = pynmea2.GGA('P', 'STMGPSRESET', ())
        await self._serial_write_cmd(msg)
        await trio.sleep(1)

    async def store(self, dir):
        almanac_path = os.path.join(dir, 'almanac.txt')
        ephemeris_path = os.path.join(dir, 'ephemeris.txt')

        # reset device in case it is stuck
        await self.reset()

        await self._store_almanac(almanac_path)
        await self._store_ephemeris(ephemeris_path)

    async def load(self, dir):
        almanac_path = os.path.join(dir, 'almanac.txt')
        ephemeris_path = os.path.join(dir, 'ephemeris.txt')

        for file in [almanac_path, ephemeris_path]:
            if not os.path.exists(file):
                self.__log.warn(f"AGPS file not found: {file}")
                self.__log.warn("*NOT* loading AGPS data")
                return

        # reset device in case it is stuck, and set time
        await self.reset()
        await self.set_time()

        await self._load_almanac(almanac_path)
        await self._load_ephemeris(ephemeris_path)

    async def _store_ephemeris(self, file='ephemeris.txt'):
        await self._store_to_file('STMDUMPEPHEMS', '$PSTMEPHEM', file)

    async def _store_almanac(self, file='almanac.txt'):
        await self._store_to_file('STMDUMPALMANAC', '$PSTMALMANAC', file)

    async def _load_ephemeris(self, file='ephemeris.txt'):
        await self._load_from_file('$PSTMEPHEMOK', file)

    async def _load_almanac(self, file='almanac.txt'):
        await self._load_from_file('$PSTMALMANACOK', file)

    async def set_time(self):
        await self.reset()
        now = datetime.utcnow()

        # INITTIME expects values to be 2 or 4 digits long
        msg = pynmea2.GGA('P', 'STMINITTIME', (
            now.strftime('%d'),
            now.strftime('%m'),
            now.strftime('%Y'),
            now.strftime('%H'),
            now.strftime('%M'),
            now.strftime('%S'),
            ))
        ret, line = await self._serial_write_cmd(msg, "STMINITTIMEOK")
        if not ret:
            raise LoggedException(f"ERROR: {line}")
        self.__log.info(line)
