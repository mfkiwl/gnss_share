# Copyright(c) 2021 by craftyguy "Clayton Craft" <clayton@craftyguy.net>
# Distributed under GPLv3+ (see COPYING) WITHOUT ANY WARRANTY.
import logging

from .logger import LoggedException
from .stm_agps import STM_AGPS

try:
    from trio_serial import SerialStream
except ImportError:
    print("warning: trio-serial not found, some drivers may not work "
          "correctly")


class STM_AGPS_SERIAL(STM_AGPS):

    def __init__(self, serial_port, baud=9600):
        super().__init__(serial_port)
        self.__log = logging.getLogger(__name__)
        self._baud = baud
        # reminder: bytearrays are mutable
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
            self._ser = SerialStream(self._ser_port, baudrate=self._baud)
            await self._ser.aopen()
        except Exception as e:
            raise LoggedException(e)

    async def _write(self, data):
        await self._ser.send_all(data)
