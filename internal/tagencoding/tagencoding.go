// Copyright 2017, OpenCensus Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

// Package tagencoding contains the tag encoding
// used interally by the stats collector.
package tagencoding

import (
	"unsafe"
)

var sizeOfUint16 = (int)(unsafe.Sizeof(uint16(0)))

type Values struct {
	Buffer     []byte
	WriteIndex int
	ReadIndex  int
}

func (vb *Values) growIfRequired(expected int) {
	if len(vb.Buffer)-vb.WriteIndex < expected {
		tmp := make([]byte, 2*(len(vb.Buffer)+1)+expected)
		copy(tmp, vb.Buffer)
		vb.Buffer = tmp
	}
}

func (vb *Values) WriteValue(v []byte) {
	length := len(v)
	vb.growIfRequired(sizeOfUint16 + length)

	// writing length of v
	bytes := *(*[2]byte)(unsafe.Pointer(&length))
	vb.Buffer[vb.WriteIndex] = bytes[0]
	vb.WriteIndex++
	vb.Buffer[vb.WriteIndex] = bytes[1]
	vb.WriteIndex++

	if length == 0 {
		// No value was encoded for this key
		return
	}

	// writing v
	copy(vb.Buffer[vb.WriteIndex:], v)
	vb.WriteIndex += length
}

// ReadValue is the helper method to read the values when decoding valuesBytes to a map[Key][]byte.
func (vb *Values) ReadValue() []byte {
	// read length of v
	length := (int)(*(*uint16)(unsafe.Pointer(&vb.Buffer[vb.ReadIndex])))
	vb.ReadIndex += sizeOfUint16
	if length == 0 {
		// No value was encoded for this key
		return nil
	}

	// read value of v
	v := make([]byte, length)
	endIdx := vb.ReadIndex + length
	copy(v, vb.Buffer[vb.ReadIndex:endIdx])
	vb.ReadIndex = endIdx
	return v
}

func (vb *Values) Bytes() []byte {
	return vb.Buffer[:vb.WriteIndex]
}
