// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package gnss

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gitlab.com/postmarketOS/gnss_share/internal/nmea"
	"go.bug.st/serial"
)

type StmCommon struct {
	path    string
	scanner *bufio.Scanner
	writer  io.Writer
	open    func() (err error)
	close   func() (err error)
	ready   func() (bool, error)
}

type Stm struct {
	StmCommon
	device *os.File
}

type StmSerial struct {
	StmCommon
	serConf serial.Mode
	serPort serial.Port
}

func NewStmSerial(path string, baud int) *StmSerial {
	s := &StmSerial{
		serConf: serial.Mode{
			BaudRate: baud,
		},
		StmCommon: StmCommon{
			path: path,
		},
	}

	s.StmCommon.open = func() (err error) {
		s.serPort, err = serial.Open(s.path, &s.serConf)
		if err != nil {
			err = fmt.Errorf("gnss/StmSerial.Open(): %w", err)
			return
		}
		s.scanner = bufio.NewScanner(s.serPort)
		s.writer = s.serPort

		return
	}

	s.StmCommon.close = func() (err error) {
		if s.serPort != nil {
			err = s.serPort.Close()
			if err != nil {
				err = fmt.Errorf("gnss/StmSerial.Close: %w", err)
				return
			}
		}
		return
	}

	s.StmCommon.ready = func() (bool, error) {
		return true, nil
	}

	return s
}

func NewStm(path string) *Stm {
	s := &Stm{
		StmCommon: StmCommon{
			path: path,
		},
	}

	s.StmCommon.open = func() (err error) {
		// Using syscall.Open will open the file in non-pollable mode, which
		// results in a significant reduction in CPU usage on ARM64 systems,
		// and no noticeable impact on x86_64. We don't need to poll the file
		// since it's just a constant stream of new data from the kernel's GNSS
		// subsystem
		fd, err := syscall.Open(s.path, os.O_RDWR, 0666)
		if err != nil {
			err = fmt.Errorf("gnss/Stm.Open(): %w", err)
			return
		}
		s.device = os.NewFile(uintptr(fd), s.path)

		s.scanner = bufio.NewScanner(s.device)
		s.writer = s.device

		if ready, err := s.ready(); !ready {
			return fmt.Errorf("gnss/StmCommon.Start: device not ready: %s", err)
		}
		return
	}

	s.StmCommon.close = func() (err error) {
		err = s.device.Close()
		if err != nil {
			err = fmt.Errorf("gnss/Stm.Close: %w", err)
		}
		return
	}

	s.StmCommon.ready = func() (bool, error) {
		// device sends this message when it has booted
		resp := nmea.Sentence{
			Type: "GPTXT",
			Data: []string{"DEFAULT LIV CONFIGURATION"},
		}.String()

		tries := 100
		c := 0
		for s.scanner.Scan() {
			if c > tries {
				return false, fmt.Errorf("gnss/StmCommon.open: timed out waiting for device")
			}
			line := s.scanner.Text()
			// Contains() is used because sometimes the device will prefix a
			// message with a NULL byte or do other undocumented crazy things like
			// that.
			if strings.Contains(line, resp) {
				return true, nil
			}
			c++
		}
		return false, nil
	}

	return s
}

func (s *StmCommon) Start(sendCh chan<- []byte, stop <-chan bool, errCh chan<- error) {
	err := s.open()
	if err != nil {
		errCh <- fmt.Errorf("gnss/stm.Start: %w", err)
		return
	}
	defer s.close()

	if err := s.configureMessages(); err != nil {
		errCh <- err
		return
	}

scanLoop: // used to break out of select when a 'stop' is received
	for s.scanner.Scan() {
		select {
		case <-stop:
			err := s.close()
			if err != nil {
				fmt.Println(err)
			}
			break scanLoop
		default:
			sendCh <- s.scanner.Bytes()
		}
	}
	if err := s.scanner.Err(); err != nil {
		errCh <- fmt.Errorf("gnss/stm.Start: %w", err)
		return
	}
}

func (s *StmCommon) Save(dir string) (err error) {
	s.open()
	defer s.close()

	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return
	}

	err = s.saveEphemerides(filepath.Join(dir, "ephemerides.txt"))
	if err != nil {
		return
	}

	err = s.saveAlamanac(filepath.Join(dir, "almanac.txt"))
	if err != nil {
		return
	}

	return
}

func (s *StmCommon) Load(dir string) (err error) {
	s.open()
	defer s.close()

	err = s.loadEphemerides(filepath.Join(dir, "ephemerides.txt"))
	if err != nil {
		return
	}

	err = s.loadAlmanac(filepath.Join(dir, "almanac.txt"))
	if err != nil {
		return
	}

	return
}

func (s *StmCommon) restoreParams() (err error) {
	_, err = s.sendCmd(nmea.Sentence{Type: "PSTMRESTOREPAR"}.String(), true)
	if err != nil {
		return
	}
	_, err = s.sendCmd(nmea.Sentence{Type: "PSTMSRR"}.String(), false)
	return
}

func (s *StmCommon) configureMessages() (err error) {
	s.pause()
	defer s.resume()

	ok := true

	// check that the lower 32-bits have only what we want enabled
	out, err := s.sendCmd(nmea.Sentence{Type: "PSTMGETPAR", Data: []string{"1201"}}.String(), true)
	for _, l := range out {
		if strings.Contains(l, "PSTMSETPAR,1201") {
			msg := strings.Split(l, "*")[0]
			fields := strings.Split(msg, ",")
			if len(fields) < 3 {
				continue
			}
			var value uint64
			if value, err = strconv.ParseUint(fields[2], 0, 64); err != nil {
				continue
			}

			ok = ok && value&(1<<1) != 0 // GPGGA
			ok = ok && value&(1<<6) != 0 // GPRMC
		}
	}
	out, err = s.sendCmd(nmea.Sentence{Type: "PSTMGETPAR", Data: []string{"1228"}}.String(), true)
	for _, l := range out {
		if strings.Contains(l, "PSTMSETPAR,1228") {
			fields := strings.Split(strings.Split(l, "*")[0], ",")
			if len(fields) < 3 {
				continue
			}
			var value uint64
			if value, err = strconv.ParseUint(fields[2], 0, 64); err != nil {
				continue
			}

			ok = ok && (value == 0) // no messages in this range
		}
	}

	if !ok {
		fmt.Println("limiting messages from the gnss module")
		// configure the module to only report types relevant for location
		var msgList uint64
		msgList |= 1 << 1 // GPGGA
		msgList |= 1 << 6 // GPRMC
		// // 1 sets for the current config block (not persistent)
		// // 201 is for setting the lower 32-bits
		err = s.setParam(3, 201, msgList, 0)
		if err != nil {
			// not fatal
			fmt.Println(err)
		}
		// clear upper 32-bits, since we don't want to enable any messages in that
		// range
		err = s.setParam(3, 228, 0, 0)

		// increase the reporting period to reduce CPU usage from processing new
		// messages
		err = s.setParam(3, 303, 1, 0)

		_, err = s.sendCmd(nmea.Sentence{Type: "PSTMSAVEPAR"}.String(), true)
		_, err = s.sendCmd(nmea.Sentence{Type: "PSTMSRR"}.String(), false)
	}

	return
}

// setParam sets parameters in the given configuration data block. See the STM
// Teseo Liv3f gps software manual sections for PSTMSETPAR and relevant CBD for
// possible IDs/values to use.
func (s *StmCommon) setParam(confBlock int, id int, value uint64, mode int) (err error) {

	msgListCmd := nmea.Sentence{
		Type: "PSTMSETPAR",
		Data: []string{
			fmt.Sprintf("%d%d", confBlock, id),
			// fmt.Sprintf("%d", id),
			fmt.Sprintf("0x%08x", value),
			fmt.Sprintf("%d", mode),
		},
	}
	out, err := s.sendCmd(msgListCmd.String(), true)

	for _, o := range out {
		if strings.Contains(o, "PSTMSETPARERROR") {
			return fmt.Errorf("error setting parameter at conf block %d, id %d: %d", confBlock, id, value)
		}
	}

	return
}

func (s *StmCommon) saveEphemerides(path string) (err error) {
	fmt.Printf("Storing ephemerides to: %q\n", path)

	err = s.pause()
	if err != nil {
		return
	}
	defer s.resume()

	out, err := s.sendCmd(nmea.Sentence{Type: "PSTMDUMPEPHEMS"}.String(), true)
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.saveEphemerides: %w", err)
	}

	fd, err := os.Create(path)
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.Save: error saving to file %q: %w", path, err)
		return
	}
	defer fd.Close()

	for _, l := range out {
		if strings.HasPrefix(l, "$PSTMEPHEM,") {
			fd.Write([]byte(fmt.Sprintf("%s\n", l)))
		}
	}
	return
}

func (s *StmCommon) saveAlamanac(path string) (err error) {
	fmt.Printf("Storing almanac to: %q\n", path)

	err = s.pause()
	if err != nil {
		return
	}
	defer s.resume()

	out, err := s.sendCmd(nmea.Sentence{Type: "PSTMDUMPALMANAC"}.String(), true)
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.saveAlmanac: %w", err)
	}

	fd, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.saveAlamanac: error saving to file %q: %w", path, err)
	}
	defer fd.Close()

	for _, l := range out {
		if strings.HasPrefix(l, "$PSTMALMANAC,") {
			fd.Write([]byte(fmt.Sprintf("%s\n", l)))
		}
	}
	return
}

func (s *StmCommon) sendCmd(cmd string, isAcked bool) (out []string, err error) {
	err = s.write([]byte(cmd))
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.sendCmd: %w", err)
		return
	}

	if !isAcked {
		return
	}

	// TODO: time out at some point...
	c := 0
	for s.scanner.Scan() {
		line := s.scanner.Text()
		fmt.Printf("read: %s\n", line)

		// Command it echo'd back when it is complete.
		if line == cmd {
			break
		}

		out = append(out, line)
		c++
	}

	if err = s.scanner.Err(); err != nil {
		err = fmt.Errorf("gnss/StmCommon.sendCmd: %w", err)
		return
	}
	return
}

func (s *StmCommon) pause() (err error) {
	_, err = s.sendCmd(nmea.Sentence{Type: "PSTMGPSSUSPEND"}.String(), true)
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.pause: %w", err)
	}

	return
}

func (s *StmCommon) resume() (err error) {
	_, err = s.sendCmd(nmea.Sentence{Type: "PSTMGPSRESTART"}.String(), false)
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.pause: %w", err)
	}

	return
}

func (s *StmCommon) write(data []byte) (err error) {
	fmt.Printf("write: %s\n", string(data))
	// add crlf
	_, err = s.writer.Write(append(data, 0x0D, 0x0A))
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.write: %w", err)
		return
	}

	return
}

func (s *StmCommon) batchSendCmd(cmds []string, strict bool) (out []string, err error) {

	for _, c := range cmds {
		out, err = s.sendCmd(c, true)
		if err != nil {
			err = fmt.Errorf("gnss/StmCommon.loadAlmanac: %s", err)
			if strict {
				return
			}
			fmt.Println(err)
		}
	}
	return
}

func (s *StmCommon) loadEphemerides(path string) (err error) {
	fd, err := os.Open(path)
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.loadAlmanac: %w", err)
		return
	}
	defer fd.Close()

	err = s.pause()
	if err != nil {
		return
	}
	defer s.resume()

	var lines []string
	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	_, err = s.batchSendCmd(lines, false)
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.loadEphemerides: %w", err)
	}

	return
}

func (s *StmCommon) loadAlmanac(path string) (err error) {
	fd, err := os.Open(path)
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.loadAlmanac: %w", err)
		return
	}
	defer fd.Close()

	err = s.pause()
	if err != nil {
		return
	}
	defer s.resume()

	var lines []string

	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	_, err = s.batchSendCmd(lines, false)
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.loadAlmanac: %w", err)
	}

	return
}
