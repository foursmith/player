package media

import (
	"encoding/binary"
	"testing"
)

func mp4Atom(kind string, payload []byte) []byte {
	box := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(box, uint32(8+len(payload)))
	copy(box[4:], kind)
	return append(box, payload...)
}

func mp4Words(values ...uint32) []byte {
	out := make([]byte, 4*len(values))
	for i, value := range values {
		binary.BigEndian.PutUint32(out[i*4:], value)
	}
	return out
}

func mp4Children(t *testing.T, data []byte, visit func(string, []byte) []byte) []byte {
	t.Helper()
	var out []byte
	for len(data) >= 8 {
		size := int(binary.BigEndian.Uint32(data))
		if size < 8 || size > len(data) {
			t.Fatal("invalid test movie atom")
		}
		out = append(out, visit(string(data[4:8]), data[8:size])...)
		data = data[size:]
	}
	return out
}
