package media

import "encoding/binary"

// exifOrientation reads the EXIF orientation (1 to 8) of a JPEG, or 1 when there is none or the
// data cannot be read. Phones store a photo as the sensor saw it and record here how to turn it,
// and browsers honour that, so a thumbnail must too. Every offset is bounds-checked: the input is
// whatever a client uploaded.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	pos := 2
	for pos+4 <= len(data) {
		if data[pos] != 0xFF {
			return 1
		}
		marker := data[pos+1]
		if marker == 0xFF { // fill byte
			pos++
			continue
		}
		if marker == 0xD9 || marker == 0xDA { // end of image, or the start of the picture data
			return 1
		}
		size := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		if size < 2 || pos+2+size > len(data) {
			return 1
		}
		if marker == 0xE1 { // APP1
			seg := data[pos+4 : pos+2+size]
			if len(seg) >= 6 && string(seg[:6]) == "Exif\x00\x00" {
				return tiffOrientation(seg[6:])
			}
		}
		pos += 2 + size
	}
	return 1
}

// tiffOrientation finds tag 0x0112 in the first image directory of an EXIF TIFF block.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:4]) != 42 {
		return 1
	}
	off := int(bo.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off : off+2]))
	for i := 0; i < n; i++ {
		e := off + 2 + i*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:e+2]) != 0x0112 {
			continue
		}
		// type 3 (SHORT), count 1: the value sits in the first two bytes of the value field
		if bo.Uint16(t[e+2:e+4]) != 3 || bo.Uint32(t[e+4:e+8]) != 1 {
			return 1
		}
		if v := int(bo.Uint16(t[e+8 : e+10])); v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}
