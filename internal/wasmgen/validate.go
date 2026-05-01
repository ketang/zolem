package wasmgen

import (
	"bytes"
	"errors"
	"fmt"
)

const (
	sectionIDImport = 2
	sectionIDExport = 7

	exportKindFunction = 0x00
	exportKindTable    = 0x01
	exportKindMemory   = 0x02
	exportKindGlobal   = 0x03
)

func validateImportExportSurface(wasmBytes []byte) error {
	sections, err := parseSections(wasmBytes)
	if err != nil {
		return err
	}
	if importSection := sections[sectionIDImport]; importSection != nil {
		count, _, err := readU32(importSection, 0)
		if err != nil {
			return fmt.Errorf("read WASM import section: %w", err)
		}
		if count != 0 {
			return errors.New("WASM generator imports are not supported in v1")
		}
	}

	exports, err := parseExports(sections[sectionIDExport])
	if err != nil {
		return err
	}
	if len(exports) != len(requiredExports) {
		return fmt.Errorf("WASM generator must export exactly %d ABI entries", len(requiredExports))
	}
	for name, wantKind := range requiredExports {
		gotKind, ok := exports[name]
		if !ok {
			return fmt.Errorf("WASM generator missing %q export", name)
		}
		if gotKind != wantKind {
			return fmt.Errorf("WASM generator export %q has wrong kind", name)
		}
	}
	for name := range exports {
		if _, ok := requiredExports[name]; !ok {
			return fmt.Errorf("unsupported WASM generator export %q", name)
		}
	}
	return nil
}

func parseSections(wasmBytes []byte) (map[byte][]byte, error) {
	if len(wasmBytes) < 8 || !bytes.Equal(wasmBytes[:4], []byte{0x00, 0x61, 0x73, 0x6d}) {
		return nil, errors.New("invalid WASM magic header")
	}
	if !bytes.Equal(wasmBytes[4:8], []byte{0x01, 0x00, 0x00, 0x00}) {
		return nil, errors.New("unsupported WASM binary version")
	}

	sections := make(map[byte][]byte)
	for off := 8; off < len(wasmBytes); {
		id := wasmBytes[off]
		off++
		size, n, err := readU32(wasmBytes, off)
		if err != nil {
			return nil, fmt.Errorf("read WASM section size: %w", err)
		}
		off += n
		end := off + int(size)
		if end < off || end > len(wasmBytes) {
			return nil, errors.New("WASM section extends past end of module")
		}
		if id != 0 {
			if _, exists := sections[id]; exists {
				return nil, fmt.Errorf("duplicate WASM section %d", id)
			}
			sections[id] = wasmBytes[off:end]
		}
		off = end
	}
	return sections, nil
}

func parseExports(section []byte) (map[string]byte, error) {
	if section == nil {
		return nil, errors.New("WASM generator missing export section")
	}
	count, n, err := readU32(section, 0)
	if err != nil {
		return nil, fmt.Errorf("read WASM export count: %w", err)
	}
	off := n
	exports := make(map[string]byte, count)
	for i := uint32(0); i < count; i++ {
		name, consumed, err := readName(section, off)
		if err != nil {
			return nil, fmt.Errorf("read WASM export name: %w", err)
		}
		off += consumed
		if off >= len(section) {
			return nil, errors.New("WASM export missing kind")
		}
		kind := section[off]
		off++
		_, consumed, err = readU32(section, off)
		if err != nil {
			return nil, fmt.Errorf("read WASM export index: %w", err)
		}
		off += consumed
		exports[name] = kind
	}
	if off != len(section) {
		return nil, errors.New("WASM export section has trailing bytes")
	}
	return exports, nil
}

func readName(b []byte, off int) (string, int, error) {
	l, n, err := readU32(b, off)
	if err != nil {
		return "", 0, err
	}
	start := off + n
	end := start + int(l)
	if end < start || end > len(b) {
		return "", 0, errors.New("name extends past end of section")
	}
	return string(b[start:end]), n + int(l), nil
}

func readU32(b []byte, off int) (uint32, int, error) {
	var result uint32
	for i := 0; i < 5; i++ {
		if off+i >= len(b) {
			return 0, 0, errors.New("unexpected end of LEB128")
		}
		c := b[off+i]
		result |= uint32(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return result, i + 1, nil
		}
	}
	return 0, 0, errors.New("LEB128 value exceeds uint32")
}
