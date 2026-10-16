package pagearchive

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
)

// Bound the generic plist decoder before it allocates containers or traverses
// attacker-provided references. Resource limits alone apply too late here.
func checkPlistLimits(content []byte, stop <-chan struct{}) error {
	if !bytes.HasPrefix(content, []byte("bplist")) {
		decoder := xml.NewDecoder(bytes.NewReader(content))
		depth, count := 0, 0
		for {
			if err := stopped(stop); err != nil {
				return err
			}
			token, err := decoder.Token()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			switch token.(type) {
			case xml.StartElement:
				depth++
				count++
			case xml.EndElement:
				depth--
			}
			if depth > 128 || count > 1000000 {
				return errors.New("plist structure exceeds limits")
			}
		}
	}
	if len(content) < 40 {
		return errors.New("truncated binary plist")
	}
	readInteger := func(offset, width uint64) (uint64, bool) {
		if width < 1 || width > 8 || offset > uint64(len(content)) || width > uint64(len(content))-offset {
			return 0, false
		}
		var number uint64
		for _, octet := range content[offset : offset+width] {
			number = number<<8 | uint64(octet)
		}
		return number, true
	}
	trailer := uint64(len(content) - 32)
	offsetWidth, referenceWidth := uint64(content[trailer+6]), uint64(content[trailer+7])
	count, _ := readInteger(trailer+8, 8)
	top, _ := readInteger(trailer+16, 8)
	table, _ := readInteger(trailer+24, 8)
	if count < 1 || count > 1000000 || top >= count || offsetWidth < 1 || offsetWidth > 8 || referenceWidth < 1 || referenceWidth > 8 || table < 8 || table > trailer || count > (trailer-table)/offsetWidth {
		return errors.New("invalid binary plist object table")
	}
	state := make([]uint8, count)
	var edges uint64
	var visit func(uint64, int) error
	visit = func(identifier uint64, depth int) error {
		if err := stopped(stop); err != nil {
			return err
		}
		if depth > 128 || identifier >= count || state[identifier] == 1 {
			return errors.New("cyclic or excessive plist nesting")
		}
		if state[identifier] == 2 {
			return nil
		}
		offset, ok := readInteger(table+identifier*offsetWidth, offsetWidth)
		if !ok || offset < 8 || offset >= table {
			return errors.New("invalid binary plist object offset")
		}
		state[identifier] = 1
		tag := content[offset]
		kind, length, start := tag>>4, uint64(tag&15), offset+1
		if kind >= 4 && kind != 8 && length == 15 {
			if start >= table || content[start]>>4 != 1 || content[start]&15 > 3 {
				return errors.New("invalid binary plist length")
			}
			width := uint64(1) << (content[start] & 15)
			length, ok = readInteger(start+1, width)
			if !ok {
				return errors.New("invalid binary plist length")
			}
			start += 1 + width
		}
		if start > table {
			return errors.New("invalid binary plist payload")
		}
		switch kind {
		case 4, 5, 6:
			width := uint64(1)
			if kind == 6 {
				width = 2
			}
			if length > (table-start)/width {
				return errors.New("binary plist string or data exceeds payload")
			}
		case 10, 12, 13:
			if kind == 13 {
				if length > 1000000/2 {
					return errors.New("plist dictionary exceeds limit")
				}
				length *= 2
			}
			if length > 1000000 || length > (table-start)/referenceWidth || edges > 1000000-length {
				return errors.New("plist references exceed limit")
			}
			edges += length
			for index := uint64(0); index < length; index++ {
				reference, ok := readInteger(start+index*referenceWidth, referenceWidth)
				if !ok {
					return errors.New("invalid plist reference")
				}
				if err := visit(reference, depth+1); err != nil {
					return err
				}
			}
		}
		state[identifier] = 2
		return nil
	}
	return visit(top, 0)
}
