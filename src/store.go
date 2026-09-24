package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const dbMagic = "DCDB"
const dbVersion = 3
const kdfIters = 65536
const keySaltLen = 16
const gcmIVLen = 12

func workCount(n int) int {
	w := runtime.NumCPU()
	if w > n {
		w = n
	}
	if w < 1 {
		w = 1
	}
	return w
}

func splitRanges(n int, w int) [][2]int {
	ranges := make([][2]int, 0, w)
	per := (n + w - 1) / w
	for k := 0; k < w; k++ {
		s := k * per
		if s >= n {
			break
		}
		e := s + per
		if e > n {
			e = n
		}
		ranges = append(ranges, [2]int{s, e})
	}
	return ranges
}

func parallelApply(rows []Row, fn func(i int, r Row) Row) []Row {
	out := make([]Row, len(rows))
	n := len(rows)
	w := 1
	if n >= 1024 {
		w = workCount(n)
	}
	if w == 1 {
		for i := 0; i < n; i++ {
			out[i] = fn(i, rows[i])
		}
		return out
	}
	var wg sync.WaitGroup
	for _, span := range splitRanges(n, w) {
		s := span[0]
		e := span[1]
		wg.Add(1)
		go func(s int, e int) {
			defer wg.Done()
			for i := s; i < e; i++ {
				out[i] = fn(i, rows[i])
			}
		}(s, e)
	}
	wg.Wait()
	return out
}

type BinaryWriter struct {
	buf  bytes.Buffer
	work [8]byte
}

func (this *BinaryWriter) WriteU32(v uint32) {
	binary.LittleEndian.PutUint32(this.work[:4], v)
	this.buf.Write(this.work[:4])
}

func (this *BinaryWriter) WriteValue(v Value) {
	this.buf.WriteByte(byte(v.kind))
	switch v.kind {
	case KInt:
		binary.LittleEndian.PutUint64(this.work[:8], uint64(v.i))
		this.buf.Write(this.work[:8])
	case KFloat:
		binary.LittleEndian.PutUint64(this.work[:8], math.Float64bits(v.f))
		this.buf.Write(this.work[:8])
	case KBool:
		if v.b {
			this.buf.WriteByte(1)
		} else {
			this.buf.WriteByte(0)
		}
	case KStr:
		this.WriteU32(uint32(len(v.s)))
		this.buf.WriteString(v.s)
	case KArr:
		this.WriteU32(uint32(len(v.a)))
		for i := 0; i < len(v.a); i++ {
			this.WriteValue(v.a[i])
		}
	case KObj:
		this.WriteU32(uint32(len(v.o)))
		for k, item := range v.o {
			this.WriteU32(uint32(len(k)))
			this.buf.WriteString(k)
			this.WriteValue(item)
		}
	}
}

func (this *BinaryWriter) Bytes() []byte {
	return this.buf.Bytes()
}

type BinaryReader struct {
	data []byte
	pos  int
}

func (this *BinaryReader) need(n int) error {
	if len(this.data)-this.pos < n {
		return fmt.Errorf("corrupt database file")
	}
	return nil
}

func (this *BinaryReader) ReadU32() (uint32, error) {
	if err := this.need(4); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint32(this.data[this.pos:])
	this.pos += 4
	return v, nil
}

func (this *BinaryReader) ReadStr() (string, error) {
	n, err := this.ReadU32()
	if err != nil {
		return "", err
	}
	if err := this.need(int(n)); err != nil {
		return "", err
	}
	s := string(this.data[this.pos : this.pos+int(n)])
	this.pos += int(n)
	return s, nil
}

func (this *BinaryReader) ReadValue() (Value, error) {
	if err := this.need(1); err != nil {
		return Value{}, err
	}
	k := Kind(this.data[this.pos])
	this.pos++
	switch k {
	case KNull:
		return NullValue(), nil
	case KInt:
		if err := this.need(8); err != nil {
			return Value{}, err
		}
		i := int64(binary.LittleEndian.Uint64(this.data[this.pos:]))
		this.pos += 8
		return IntValue(i), nil
	case KFloat:
		if err := this.need(8); err != nil {
			return Value{}, err
		}
		bits := binary.LittleEndian.Uint64(this.data[this.pos:])
		this.pos += 8
		return FloatValue(math.Float64frombits(bits)), nil
	case KBool:
		if err := this.need(1); err != nil {
			return Value{}, err
		}
		b := this.data[this.pos] != 0
		this.pos++
		return BoolValue(b), nil
	case KStr:
		s, err := this.ReadStr()
		return StrValue(s), err
	case KArr:
		n, err := this.ReadU32()
		if err != nil {
			return Value{}, err
		}
		if int(n) > len(this.data)-this.pos {
			return Value{}, fmt.Errorf("corrupt database file")
		}
		arr := make([]Value, 0, int(n))
		for i := 0; i < int(n); i++ {
			item, err := this.ReadValue()
			if err != nil {
				return Value{}, err
			}
			arr = append(arr, item)
		}
		return ArrValue(arr), nil
	case KObj:
		n, err := this.ReadU32()
		if err != nil {
			return Value{}, err
		}
		if int(n) > len(this.data)-this.pos {
			return Value{}, fmt.Errorf("corrupt database file")
		}
		m := make(map[string]Value, int(n))
		for i := 0; i < int(n); i++ {
			key, err := this.ReadStr()
			if err != nil {
				return Value{}, err
			}
			item, err := this.ReadValue()
			if err != nil {
				return Value{}, err
			}
			m[key] = item
		}
		return ObjValue(m), nil
	}
	return Value{}, fmt.Errorf("corrupt database file")
}

func encodeArrayParallel(buf *bytes.Buffer, elems []Value) {
	buf.WriteByte(byte(KArr))
	lo := make([]byte, 4)
	binary.LittleEndian.PutUint32(lo, uint32(len(elems)))
	buf.Write(lo)
	n := len(elems)
	w := workCount(n)
	chunks := make([][]byte, n)
	var wg sync.WaitGroup
	for _, span := range splitRanges(n, w) {
		s := span[0]
		e := span[1]
		wg.Add(1)
		go func(s int, e int) {
			defer wg.Done()
			for i := s; i < e; i++ {
				inner := &BinaryWriter{}
				inner.WriteValue(elems[i])
				chunks[i] = inner.Bytes()
			}
		}(s, e)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		buf.Write(chunks[i])
	}
}

func encodeValuePayload(doc Value) []byte {
	if doc.kind == KArr && len(doc.a) >= 1024 {
		var buf bytes.Buffer
		encodeArrayParallel(&buf, doc.a)
		return buf.Bytes()
	}
	writer := &BinaryWriter{}
	writer.WriteValue(doc)
	return writer.Bytes()
}

func deriveKeyV2(pass string) []byte {
	sum := sha256.Sum256([]byte(pass))
	return sum[:16]
}

func pbkdf2SHA256(pass string, salt []byte, iters int, dkLen int) []byte {
	key := []byte(pass)
	var out []byte
	for block := 1; len(out) < dkLen; block++ {
		mac := hmac.New(sha256.New, key)
		mac.Write(salt)
		var b4 [4]byte
		binary.BigEndian.PutUint32(b4[:], uint32(block))
		mac.Write(b4[:])
		v := mac.Sum(nil)
		t := append([]byte(nil), v...)
		for i := 1; i < iters; i++ {
			mac := hmac.New(sha256.New, key)
			mac.Write(v)
			v = mac.Sum(nil)
			for j := range t {
				t[j] ^= v[j]
			}
		}
		out = append(out, t...)
	}
	return out[:dkLen]
}

func deriveKeyV3(pass string, salt []byte) []byte {
	return pbkdf2SHA256(pass, salt, kdfIters, 32)
}

func cryptCTR(payload []byte, key []byte, iv []byte) []byte {
	block, _ := aes.NewCipher(key)
	stream := cipher.NewCTR(block, iv)
	out := make([]byte, len(payload))
	stream.XORKeyStream(out, payload)
	return out
}

func SaveDoc(path string, doc Value, key string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload := encodeValuePayload(doc)
	salt := make([]byte, keySaltLen)
	iv := make([]byte, gcmIVLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(iv); err != nil {
		return err
	}
	block, err := aes.NewCipher(deriveKeyV3(key, salt))
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString(dbMagic)
	buf.WriteByte(dbVersion)
	buf.Write(salt)
	buf.Write(iv)
	buf.Write(gcm.Seal(nil, iv, payload, nil))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func LoadDoc(path string, key string) (Value, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Value{}, err
	}
	if len(data) < 5 || string(data[:4]) != dbMagic {
		return Value{}, fmt.Errorf("%s is not a DCDB database file", path)
	}
	version := data[4]
	var payload []byte
	switch version {
	case 1:
		payload = data[5:]
	case 2:
		if len(data) < 25 {
			return Value{}, fmt.Errorf("corrupt database file")
		}
		iv := data[5:21]
		want := binary.LittleEndian.Uint32(data[21:25])
		payload = cryptCTR(data[25:], deriveKeyV2(key), iv)
		if crc32.ChecksumIEEE(payload) != want {
			return Value{}, fmt.Errorf("wrong key or corrupt database file")
		}
	case 3:
		head := 5 + keySaltLen + gcmIVLen
		if len(data) < head+16 {
			return Value{}, fmt.Errorf("corrupt database file")
		}
		salt := data[5 : 5+keySaltLen]
		iv := data[5+keySaltLen : head]
		block, err := aes.NewCipher(deriveKeyV3(key, salt))
		if err != nil {
			return Value{}, err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return Value{}, err
		}
		payload, err = gcm.Open(nil, iv, data[head:], nil)
		if err != nil {
			return Value{}, fmt.Errorf("wrong key or corrupt database file")
		}
	default:
		return Value{}, fmt.Errorf("unsupported database version %d", version)
	}
	reader := &BinaryReader{data: payload}
	return reader.ReadValue()
}
