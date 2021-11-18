// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package nmea

import "fmt"

type Sentence struct {
	Type string
	Data []string
}

func checksum(s string) string {
	var sum uint8
	for i := 0; i < len(s); i++ {
		sum ^= s[i]
	}

	return fmt.Sprintf("%02X", sum)
}

func (s Sentence) String() string {
	sentence := s.Type
	for _, d := range s.Data {
		sentence = fmt.Sprintf("%s,%s", sentence, d)
	}

	if len(s.Data) == 0 {
		// always make sure the type is followed by a comma if there is no data
		sentence = fmt.Sprintf("%s,", sentence)
	}

	str := fmt.Sprintf("$%s*%s", sentence, checksum(sentence))
	return str
}

func (s Sentence) Bytes() []byte {
	return []byte(s.String())
}
