package directives

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/joejulian/helm-chart-bumper-action/internal/logutil"

	"go.uber.org/zap"
)

// ImageDirective describes one `# bump:` directive and the YAML scalar it applies to.
//
// Rule: the directive line must immediately precede the YAML key to update.
// The directive applies to the next non-empty, non-comment YAML line.
// The key line must be a scalar assignment: `key: value` on a single line.
//
// The directive is file-local; the file is inferred from where the directive appears.
//
// The YAMLPath is a best-effort indentation-based path suitable for yaml.PathString
// and yamlutil.SetString. It supports map keys and basic sequences.
//
// Example YAMLPath: $.image.tag or $.containers[0].image.tag
//
// NOTE: This is not a full YAML parser. It is intentionally strict and deterministic.
// If it can't unambiguously target a scalar assignment, it returns an error.
type ImageDirective struct {
	FilePath    string
	Line        int
	Key         string
	YAMLPath    string
	CurrentText string

	Image          string
	Strategy       string
	Constraint     string
	TagRegex       string
	AllowPrerelease bool
	Platform       string
}

var (
	reDirective = regexp.MustCompile(`^\s*#\s*bump:\s*(.*)$`)
)

// ScanFileForImageDirectives reads a YAML file as text and returns directives.
func ScanFileForImageDirectives(ctx context.Context, path string) ([]ImageDirective, error) {
	log := logutil.FromContext(ctx).With(zap.String("func", "directives.ScanFileForImageDirectives"), zap.String("path", path))
	log.Debug("scanning file for bump directives")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	// Allow longer lines (some values files can be large). 1MB cap.
	buf := make([]byte, 0, 64*1024)
	s.Buffer(buf, 1024*1024)

	var out []ImageDirective
	var pending *ImageDirective

	// indentation-driven path tracking
	stack := newPathStack()

	lineNo := 0
	for s.Scan() {
		lineNo++
		line := s.Text()

		m := reDirective.FindStringSubmatch(line)
		if m != nil {
			d, err := parseDirectiveArgs(m[1])
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
			d.FilePath = path
			d.Line = lineNo
			pending = &d
			continue
		}

		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}

		// Update stack based on this YAML content line.
		info, err := parseYAMLContentLine(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		stack.applyLine(info)

		// If we have a pending directive, it applies here.
		if pending != nil {
			if !info.isScalarKV {
				return nil, fmt.Errorf("%s:%d: bump directive must precede a scalar key (e.g. tag: \"1.2.3\"), but found a non-scalar line", path, lineNo)
			}
			pending.Key = info.key
			pending.CurrentText = info.valueText
			pending.YAMLPath = stack.currentPathWithLeaf(info)
			out = append(out, *pending)
			pending = nil
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if pending != nil {
		return nil, fmt.Errorf("%s:%d: bump directive had no following YAML key", pending.FilePath, pending.Line)
	}

	// stable order
	sort.Slice(out, func(i, j int) bool {
		if out[i].FilePath != out[j].FilePath {
			return out[i].FilePath < out[j].FilePath
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// parseDirectiveArgs parses `k=v` tokens separated by spaces.
// Values may be quoted with single or double quotes.
func parseDirectiveArgs(argStr string) (ImageDirective, error) {
	args, err := splitArgs(argStr)
	if err != nil {
		return ImageDirective{}, err
	}
	kv := map[string]string{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return ImageDirective{}, fmt.Errorf("invalid directive token %q (expected key=value)", a)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			return ImageDirective{}, fmt.Errorf("invalid directive token %q (empty key or value)", a)
		}
		kv[k] = v
	}

	img := kv["image"]
	if img == "" {
		return ImageDirective{}, fmt.Errorf("missing required directive field: image=")
	}
	// Require full path; no normalization.
	if !strings.Contains(img, "/") || !strings.Contains(img, ".") {
		return ImageDirective{}, fmt.Errorf("image must be a fully-qualified repository (e.g. ghcr.io/org/app); got %q", img)
	}

	strategy := kv["strategy"]
	if strategy == "" {
		strategy = "semver"
	}

	allowPrerelease := false
	if s, ok := kv["allowPrerelease"]; ok {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return ImageDirective{}, fmt.Errorf("allowPrerelease must be true/false, got %q", s)
		}
		allowPrerelease = b
	}

	return ImageDirective{
		Image:           img,
		Strategy:        strategy,
		Constraint:      kv["constraint"],
		TagRegex:        kv["tagRegex"],
		AllowPrerelease: allowPrerelease,
		Platform:        kv["platform"],
	}, nil
}

func splitArgs(s string) ([]string, error) {
	// simple state machine: split on spaces not in quotes
	var out []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ':
			if inSingle || inDouble {
				cur.WriteByte(c)
			} else {
				flush()
			}
		case '\t':
			if inSingle || inDouble {
				cur.WriteByte(c)
			} else {
				flush()
			}
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			} else {
				cur.WriteByte(c)
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote in directive")
	}
	flush()
	// Remove surrounding quotes on values in key=value tokens
	for i := range out {
		k, v, ok := strings.Cut(out[i], "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, "\"")
		v = strings.Trim(v, "'")
		out[i] = k + "=" + v
	}
	return out, nil
}

// --- YAML line parsing + indentation stack ---

type lineInfo struct {
	indent     int
	isListItem bool
	// For list items, key/value may be present inline after "- ".
	key        string
	valueText  string
	isScalarKV bool
	isMapStart bool
	// if true, this line indicates a list item but has no inline key
}

func parseYAMLContentLine(line string) (lineInfo, error) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	trim := strings.TrimSpace(line)
	if trim == "" {
		return lineInfo{}, nil
	}

	// List item?
	if strings.HasPrefix(strings.TrimLeft(line, " "), "-") {
		// Must be '-' at current indent.
		if indent >= len(line) || line[indent] != '-' {
			// line has non-space then '-', treat as not supported.
			return lineInfo{}, fmt.Errorf("unsupported indentation (dash not aligned)")
		}
		rest := strings.TrimSpace(line[indent+1:])
		if rest == "" {
			return lineInfo{indent: indent, isListItem: true}, nil
		}
		// Inline mapping: - key: value
		k, v, ok := strings.Cut(rest, ":")
		if ok {
			key := strings.TrimSpace(k)
			val := strings.TrimSpace(v)
			if key == "" {
				return lineInfo{}, fmt.Errorf("invalid list item mapping")
			}
			if val == "" {
				return lineInfo{indent: indent, isListItem: true, key: key, isMapStart: true, isScalarKV: false}, nil
			}
			return lineInfo{indent: indent, isListItem: true, key: key, valueText: val, isScalarKV: true}, nil
		}
		return lineInfo{indent: indent, isListItem: true}, nil
	}

	// Map key
	k, v, ok := strings.Cut(strings.TrimLeft(line, " "), ":")
	if !ok {
		return lineInfo{}, fmt.Errorf("unsupported YAML line (expected key: value)")
	}
	key := strings.TrimSpace(k)
	val := strings.TrimSpace(v)
	if key == "" {
		return lineInfo{}, fmt.Errorf("empty key")
	}
	if val == "" {
		return lineInfo{indent: indent, key: key, isMapStart: true}, nil
	}
	return lineInfo{indent: indent, key: key, valueText: val, isScalarKV: true}, nil
}

type stackStep struct {
	indent int
	kind   string // "key" or "index"
	key    string
	index  int
}

type pathStack struct {
	steps []stackStep
	// track list indices at each indent where a '-' occurs
	listIndexByIndent map[int]int
}

func newPathStack() *pathStack {
	return &pathStack{listIndexByIndent: map[int]int{}}
}

func (ps *pathStack) applyLine(li lineInfo) {
	// Pop to correct indent.
	ps.popToIndent(li.indent)

	if li.isListItem {
		// increment list index at this indent
		idx := 0
		if prev, ok := ps.listIndexByIndent[li.indent]; ok {
			idx = prev + 1
		}
		ps.listIndexByIndent[li.indent] = idx

		// Ensure we have an index step at this indent.
		if len(ps.steps) == 0 || !(ps.steps[len(ps.steps)-1].kind == "index" && ps.steps[len(ps.steps)-1].indent == li.indent) {
			ps.steps = append(ps.steps, stackStep{indent: li.indent, kind: "index", index: idx})
		} else {
			ps.steps[len(ps.steps)-1].index = idx
		}

		// Inline mapping inside list item.
		if li.key != "" {
			// treat as key at indent+2 (YAML convention)
			ps.popToIndent(li.indent + 1)
			ps.steps = append(ps.steps, stackStep{indent: li.indent + 2, kind: "key", key: li.key})
		}
		return
	}

	if li.key != "" {
		if li.isMapStart {
			ps.steps = append(ps.steps, stackStep{indent: li.indent, kind: "key", key: li.key})
			return
		}
		// scalar key: don't push leaf yet (we will compute path with leaf)
		// but we do need to track nesting for subsequent lines; for scalars we don't push.
		return
	}
}

func (ps *pathStack) popToIndent(indent int) {
	// Pop any steps at same or deeper indent.
	for len(ps.steps) > 0 {
		top := ps.steps[len(ps.steps)-1]
		if indent > top.indent {
			break
		}
		ps.steps = ps.steps[:len(ps.steps)-1]
	}
	// Drop list index tracking for deeper indents
	for k := range ps.listIndexByIndent {
		if k >= indent {
			delete(ps.listIndexByIndent, k)
		}
	}
}

func (ps *pathStack) currentPathWithLeaf(li lineInfo) string {
	var b strings.Builder
	b.WriteString("$")

	for _, st := range ps.steps {
		switch st.kind {
		case "key":
			b.WriteString(".")
			b.WriteString(st.key)
		case "index":
			b.WriteString("[")
			b.WriteString(strconv.Itoa(st.index))
			b.WriteString("]")
		}
	}
	if li.key != "" {
		b.WriteString(".")
		b.WriteString(li.key)
	}
	return b.String()
}
