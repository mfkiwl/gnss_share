# Copyright(c) 2021 by craftyguy "Clayton Craft" <clayton@craftyguy.net>
# Distributed under GPLv3+ (see COPYING) WITHOUT ANY WARRANTY.
import logging


class LoggedException(Exception):
    """ Exception class that sends the message to the logger """
    def __init__(self, *args):
        self.__log = logging.getLogger()
        self.__log.critical(args[0])
        super().__init__(args[0])
