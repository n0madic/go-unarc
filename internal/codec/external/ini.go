package external

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/n0madic/go-unarc/internal/method"
)

const (
	iniName         = "arc.ini"
	sectionHeading  = "[External compressor:"
	maxSectionBytes = 2048 - 1 // C copies a section into a MAX_METHOD_STRLEN buffer
	maxSectionLines = 200 - 1  // ... and splits it into at most MAX_PARAMETERS-1 lines
	maxVersions     = 1000 - 1 // MAX_COMPRESSION_METHODS-1 names per section
)

// Load registers the external compressors of the arc.ini beside unarc (RegisterExternalCompressors).
// Invalid sections are ignored, as in C.
//
// C read arc.ini from the folder of argv[0], that is from the current folder when unarc was found
// through PATH; here it is the folder of the executable. C also dropped the last byte of the file.
func Load(env *method.Env) {
	env.External = nil
	if env.ExeDir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(env.ExeDir, iniName))
	if err != nil || len(data) == 0 {
		return
	}
	sections := strings.Split("\n"+string(data), "\n[")
	for _, s := range sections[1:] {
		s = "[" + s
		if !strings.HasPrefix(s, sectionHeading) {
			continue
		}
		for _, t := range parseSection(s) {
			env.External = append(env.External, t.parser())
		}
	}
}

// parseSection mirrors AddExternalCompressor: one compressor per name in the heading, or none if any line
// is invalid.
func parseSection(section string) []*compressor {
	if len(section) > maxSectionBytes {
		section = section[:maxSectionBytes]
	}
	lines := strings.SplitN(section, "\n", maxSectionLines)
	head := strings.TrimSuffix(lines[0], "\r")
	if !strings.HasPrefix(head, sectionHeading) || !strings.HasSuffix(head, "]") {
		return nil
	}
	names := strings.SplitN(head[len(sectionHeading):len(head)-1], ",", maxVersions)
	versions := make([]*compressor, len(names))
	for i, n := range names {
		versions[i] = &compressor{
			name:       trimSpaces(n),
			datafile:   "$$arcdatafile$$.tmp",
			packedfile: "$$arcpackedfile$$.tmp",
		}
	}
	for _, line := range lines[1:] {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || line[0] == ';' {
			continue
		}
		key, value, ok := splitAssignment(line)
		if !ok {
			return nil
		}
		for _, v := range versions {
			if !v.set(key, value) {
				return nil
			}
		}
	}
	return versions
}

// splitAssignment splits "name = value": the name ends at a space or '=', the value starts after the spaces
// following '=' and must not be empty.
func splitAssignment(line string) (key, value string, ok bool) {
	i := 0
	for i < len(line) && isSpace(line[i]) {
		i++
	}
	start := i
	for i < len(line) && !isSpace(line[i]) && line[i] != '=' {
		i++
	}
	key = line[start:i]
	if i == len(line) {
		return "", "", false
	}
	if line[i] != '=' {
		for i++; i < len(line) && isSpace(line[i]); i++ {
		}
		if i == len(line) || line[i] != '=' {
			return "", "", false
		}
	}
	for i++; i < len(line) && isSpace(line[i]); i++ {
	}
	if i == len(line) {
		return "", "", false
	}
	return key, line[i:], true
}

// set applies one arc.ini parameter; it reports false for unknown names and invalid numbers.
// The compression-only parameters are checked but not kept.
func (t *compressor) set(key, value string) bool {
	text := func() string { return strings.ReplaceAll(value, "{compressor}", t.name) }
	switch key {
	case "mem", "dmem":
		n, ok := method.ParseInt(value)
		if ok {
			t.dmem = n * method.MB // a 32-bit MemSize, wrapping as in C
		}
		return ok
	case "cmem", "solid":
		_, ok := method.ParseInt(value)
		return ok
	case "packcmd":
	case "unpackcmd":
		t.unpackcmd = text()
	case "datafile":
		t.datafile = text()
	case "packedfile":
		t.packedfile = text()
	case "default":
		t.defaultopt = text()
	default:
		return false
	}
	return true
}

// trimSpaces is C trim_spaces.
func trimSpaces(s string) string {
	i, j := 0, len(s)
	for i < j && isSpace(s[i]) {
		i++
	}
	for j > i && isSpace(s[j-1]) {
		j--
	}
	return s[i:j]
}
