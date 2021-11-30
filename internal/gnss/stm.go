// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package gnss

import (
	"bufio"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/tarm/serial"
	"gitlab.com/postmarketOS/gnss_share/internal/nmea"
)

type Stm interface {
	open() (err error)
	close() (err error)
	ready() (bool, error)
	Restore() (err error)
	Reset() (err error)
	SetParam(cdbId int, value uint64) (err error)
	GetParam(cdbId int) (val uint64, err error)
}

type StmCommon struct {
	Stm
	path    string
	scanner *bufio.Scanner
	writer  io.Writer
}

// StmGnss is a STM module connected through the GNSS subsystem in the Linux
// kernel. It is commonly available through /dev/gnssN
type StmGnss struct {
	StmCommon
	device *os.File
}

// StmSerial is a STM module accessed directly over a serial interface on the
// system, e.g. via /dev/ttyN or /dev/ttyUSBN. It is *not* using the GNSS
// subsystem in the Linux kernel.
type StmSerial struct {
	StmCommon
	serConf serial.Config
	serPort *serial.Port
}

func NewStmSerial(path string, baud int) *StmSerial {
	s := StmSerial{
		serConf: serial.Config{
			Name: path,
			Baud: baud,
		},
		StmCommon: StmCommon{
			path: path,
		},
	}
	s.StmCommon.Stm = &s

	return &s
}

func (s *StmSerial) open() (err error) {
	s.serPort, err = serial.OpenPort(&s.serConf)
	if err != nil {
		err = fmt.Errorf("gnss/StmSerial.Open(): %w", err)
		return
	}
	s.scanner = bufio.NewScanner(s.serPort)
	s.writer = s.serPort

	return
}

func (s *StmSerial) close() (err error) {
	if s.serPort != nil {
		err = s.serPort.Close()
		if err != nil {
			err = fmt.Errorf("gnss/StmSerial.Close: %w", err)
			return
		}
	}
	return
}

func (s *StmSerial) ready() (bool, error) {
	return true, nil
}

func NewStmGnss(path string) *StmGnss {
	s := StmGnss{
		StmCommon: StmCommon{
			path: path,
		},
	}
	s.StmCommon.Stm = &s

	return &s
}

func (s *StmGnss) open() (err error) {
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

func (s *StmGnss) close() (err error) {
	err = s.device.Close()
	if err != nil {
		err = fmt.Errorf("gnss/Stm.Close: %w", err)
	}
	return
}

func (s *StmGnss) ready() (bool, error) {
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

func (s *StmCommon) Start(sendCh chan<- []byte, stop <-chan bool, errCh chan<- error) {
	err := s.open()
	if err != nil {
		errCh <- fmt.Errorf("gnss/stm.Start: %w", err)
		return
	}
	defer s.close()

scanLoop: // used to break out of select when a 'stop' is received
	for s.scanner.Scan() {
		select {
		case <-stop:
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

// GetParam returns the parameter value for the given CDB ID. See the STM Teseo
// Liv3f gps software manual sections for PSTMSETPAR and relevant CBD for
// possible IDs/values to use.
func (s *StmCommon) GetParam(cdbId int) (val uint64, err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}
	defer s.close()

	s.pause()
	defer s.resume()

	out, err := s.sendCmd(nmea.Sentence{Type: "PSTMGETPAR", Data: []string{fmt.Sprintf("%d", cdbId)}}.String(), true)
	if err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}

	for _, l := range out {
		if strings.Contains(l, "PSTMGETPARERROR") {
			err = fmt.Errorf("gnss/StmCommon.GetParam: PSTMGETPARERROR returned by module")
			return
		}
		if strings.Contains(l, fmt.Sprintf("PSTMSETPAR,%d", cdbId)) {
			msg := strings.Split(l, "*")[0]
			fields := strings.Split(msg, ",")
			if len(fields) < 3 {
				err = fmt.Errorf("gnss/StmCommon.GetParam: not enough fields in response from module")
				return
			}
			// try to parse with big.Parse first, sometimes module response is
			// in scientific notation...
			var valBig *big.Float
			valBig, _, err = big.ParseFloat(fields[2], 10, 0, big.ToNearestEven)
			if err == nil {
				val, _ = valBig.Uint64()
				return
			}
			// try parsing with strconv next
			val, err = strconv.ParseUint(fields[2], 0, 64)
			if err == nil {
				return
			}

			// value is in a format that needs to be handled...
			err = fmt.Errorf("gnss/StmCommon.GetParam: Unable to parse returned value: %q", fields[2])
			return
		}
	}
	err = fmt.Errorf("gnss/StmCommon.GetParam: no response sent by module")
	return
}

// SetParam sets parameters in the given configuration data block. See the STM
// Teseo Liv3f gps software manual sections for PSTMSETPAR and relevant CBD for
// possible IDs/values to use.
func (s *StmCommon) SetParam(cdbId int, value uint64) (err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}
	defer s.close()

	s.pause()
	// resume only on error, since system is reset on success

	msgListCmd := nmea.Sentence{
		Type: "PSTMSETPAR",
		Data: []string{
			fmt.Sprintf("%d%d", 3, cdbId),
			fmt.Sprintf("0x%08x", value),
			// TODO: exposing the OR and AND functionality in the 4th optional
			// parameter to STMSETPAR would be nice
			fmt.Sprintf("%d", 0),
		},
	}
	out, err := s.sendCmd(msgListCmd.String(), true)
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.SetParam: %w", err)
		s.resume()
		return
	}

	for _, o := range out {
		if strings.Contains(o, "PSTMSETPARERROR") {
			s.resume()
			return fmt.Errorf("error setting parameter at conf block %d, id %d: %d", 1, cdbId, value)
		}
	}

	_, err = s.sendCmd(nmea.Sentence{Type: "PSTMSAVEPAR"}.String(), true)
	if err != nil {
		s.resume()
		return
	}
	_, err = s.sendCmd(nmea.Sentence{Type: "PSTMSRR"}.String(), false)
	return
}

func (s *StmCommon) Reset() (err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.Reset: %w", err)
		return
	}

	defer s.close()
	s.pause()
	_, err = s.sendCmd(nmea.Sentence{Type: "PSTMSRR"}.String(), false)
	return
}

func (s *StmCommon) Restore() (err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}

	defer s.close()
	s.pause()
	// resume only on error, since system is reset on success

	_, err = s.sendCmd(nmea.Sentence{Type: "PSTMRESTOREPAR"}.String(), true)
	if err != nil {
		s.resume()
		return
	}
	_, err = s.sendCmd(nmea.Sentence{Type: "PSTMSRR"}.String(), false)
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
