package wasmgen

import (
	"strings"
	"testing"
)

// buildExportSurfaceWASM builds a minimal WASM blob containing only a magic
// header and an export section listing the supplied entries. The module is not
// runnable, but it exercises the export-surface validator independently of
// wazero compilation.
func buildExportSurfaceWASM(entries []exportEntry) []byte {
	var section []byte
	section = append(section, encodeU32(uint32(len(entries)))...)
	for _, e := range entries {
		nameBytes := []byte(e.name)
		section = append(section, encodeU32(uint32(len(nameBytes)))...)
		section = append(section, nameBytes...)
		section = append(section, e.kind)
		section = append(section, encodeU32(e.index)...)
	}

	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	out = append(out, sectionIDExport)
	out = append(out, encodeU32(uint32(len(section)))...)
	out = append(out, section...)
	return out
}

type exportEntry struct {
	name  string
	kind  byte
	index uint32
}

func encodeU32(v uint32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
			continue
		}
		out = append(out, b)
		return out
	}
}

func requiredExportEntries() []exportEntry {
	return []exportEntry{
		{name: "memory", kind: exportKindMemory, index: 0},
		{name: "alloc", kind: exportKindFunction, index: 0},
		{name: "dealloc", kind: exportKindFunction, index: 1},
		{name: "generate", kind: exportKindFunction, index: 2},
		{name: "result_ptr", kind: exportKindFunction, index: 3},
		{name: "result_len", kind: exportKindFunction, index: 4},
		{name: "result_free", kind: exportKindFunction, index: 5},
	}
}

func TestValidateImportExportSurface_AcceptsBoundaryGlobals(t *testing.T) {
	entries := append(requiredExportEntries(),
		exportEntry{name: "__data_end", kind: exportKindGlobal, index: 0},
		exportEntry{name: "__heap_base", kind: exportKindGlobal, index: 1},
	)
	if err := validateImportExportSurface(buildExportSurfaceWASM(entries)); err != nil {
		t.Fatalf("validate with __data_end and __heap_base globals: %v", err)
	}
}

func TestValidateImportExportSurface_AcceptsSingleBoundaryGlobal(t *testing.T) {
	entries := append(requiredExportEntries(),
		exportEntry{name: "__data_end", kind: exportKindGlobal, index: 0},
	)
	if err := validateImportExportSurface(buildExportSurfaceWASM(entries)); err != nil {
		t.Fatalf("validate with only __data_end global: %v", err)
	}
}

func TestValidateImportExportSurface_RejectsUnknownExtraExport(t *testing.T) {
	entries := append(requiredExportEntries(),
		exportEntry{name: "surprise", kind: exportKindFunction, index: 6},
	)
	err := validateImportExportSurface(buildExportSurfaceWASM(entries))
	if err == nil {
		t.Fatalf("validate accepted unknown export")
	}
	if !strings.Contains(err.Error(), "surprise") {
		t.Fatalf("error should name unknown export, got %v", err)
	}
}

func TestValidateImportExportSurface_RejectsBoundaryExportWrongKind(t *testing.T) {
	entries := append(requiredExportEntries(),
		exportEntry{name: "__data_end", kind: exportKindFunction, index: 6},
	)
	err := validateImportExportSurface(buildExportSurfaceWASM(entries))
	if err == nil {
		t.Fatalf("validate accepted __data_end with wrong kind")
	}
	if !strings.Contains(err.Error(), "__data_end") {
		t.Fatalf("error should name boundary export, got %v", err)
	}
}

func TestValidateImportExportSurface_RejectsMissingRequired(t *testing.T) {
	entries := requiredExportEntries()[:len(requiredExportEntries())-1]
	entries = append(entries,
		exportEntry{name: "__data_end", kind: exportKindGlobal, index: 0},
		exportEntry{name: "__heap_base", kind: exportKindGlobal, index: 1},
	)
	err := validateImportExportSurface(buildExportSurfaceWASM(entries))
	if err == nil {
		t.Fatalf("validate accepted module missing required export")
	}
	if !strings.Contains(err.Error(), "result_free") {
		t.Fatalf("error should name missing required export, got %v", err)
	}
}

func TestValidateImportExportSurface_AcceptsBaselineRequiredOnly(t *testing.T) {
	if err := validateImportExportSurface(buildExportSurfaceWASM(requiredExportEntries())); err != nil {
		t.Fatalf("validate baseline required exports: %v", err)
	}
}
