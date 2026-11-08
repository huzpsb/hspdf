/*
Copyright 2026 huzpsb.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package render

import (
	"strings"
	"testing"
	"time"
)

// psProg builds a type 4 function program from source text.
func psProg(t *testing.T, src string) *pdfFunction {
	t.Helper()
	fn := &pdfFunction{fnType: 4, rng: [][2]float64{{0, 1000}}}
	fn.prog = parsePSNodes(strings.Fields(src))
	if fn.prog == nil {
		t.Fatal("empty program")
	}
	return fn
}

// TestEvalPSProgramSemantics pins the arithmetic and branch semantics of the
// calculator function interpreter (incl. the ifelse branch order: true runs
// the FIRST of the two procs).
func TestEvalPSProgramSemantics(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		in   []float64
		want []float64
	}{
		{"arith", "2 3 add", nil, []float64{5}},
		{"input passthrough", "mul", []float64{6, 7}, []float64{42}},
		{"if true", "1 { 5 } if 7", nil, []float64{7}},
		{"if false", "0 { 5 } if 7", nil, []float64{7}},
		{"ifelse true runs first proc", "1 { 8 } { 9 } ifelse", nil, []float64{8}},
		{"ifelse false runs second proc", "0 { 8 } { 9 } ifelse", nil, []float64{9}},
		{"nested if", "1 { 1 { 42 } if } if", nil, []float64{42}},
		{"nested ifelse in false branch", "0 { 1 } { 1 { 3 } { 4 } ifelse } ifelse", nil, []float64{3}},
		{"dup add", "11 dup add", nil, []float64{22}},
		{"exch sub", "2 10 exch sub", nil, []float64{8}},
		{"conditional on input", "dup 5 gt { 100 } { 200 } ifelse", []float64{3}, []float64{200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := evalPSProgram(psProg(t, tc.src), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != len(tc.want) {
				t.Fatalf("out = %v, want %v", out, tc.want)
			}
			for i := range out {
				if out[i] != tc.want[i] {
					t.Fatalf("out = %v, want %v", out, tc.want)
				}
			}
		})
	}
}

// TestEvalPSProgramUnresolvedIfErrors verifies a missing proc is reported.
func TestEvalPSProgramUnresolvedIfErrors(t *testing.T) {
	if _, err := evalPSProgram(psProg(t, "1 if 2"), nil); err == nil ||
		!strings.Contains(err.Error(), "proc scan failed") {
		t.Fatalf("want proc scan failed error, got %v", err)
	}
}

// TestEvalPSProgramLazyProcResolution verifies an unexecutable if inside a
// proc that never runs is tolerated (resolution happens at execution time).
func TestEvalPSProgramLazyProcResolution(t *testing.T) {
	out, err := evalPSProgram(psProg(t, "0 { 1 if } if 3"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0] != 3 {
		t.Fatalf("out = %v, want [3]", out)
	}
}

// TestEvalPSProgramProcNestingCapped verifies deeply nested { proc } if
// constructs fail with an error instead of unbounded recursion.
func TestEvalPSProgramProcNestingCapped(t *testing.T) {
	var sb strings.Builder
	var open func(depth int)
	open = func(depth int) {
		if depth == 0 {
			sb.WriteString("1 ")
			return
		}
		sb.WriteString("1 { ")
		open(depth - 1)
		sb.WriteString("} if ")
	}
	open(maxPSProcDepth + 10)

	_, err := evalPSProgram(psProg(t, sb.String()), nil)
	if err == nil || !strings.Contains(err.Error(), "nesting too deep") {
		t.Fatalf("want nesting error, got %v", err)
	}
}

// TestEvalPSProgramNestedIfLinear verifies evaluation stays linear: the
// pre-fix implementation rescanned every token once per nesting level and
// needed ~1s for 64k tokens (minutes for a few MB programs).
func TestEvalPSProgramNestedIfLinear(t *testing.T) {
	// Deep as allowed, then wide: many sibling ifs on one level.
	var sb strings.Builder
	sb.WriteString("1 { 1 { 500 ")
	for i := 0; i < 100_000; i++ {
		sb.WriteString("dup 0 gt { 1 add } { 1 add } ifelse ")
	}
	sb.WriteString(" } if } if")

	mustFinish(t, 20*time.Second, "wide nested if program", func() {
		fn := &pdfFunction{fnType: 4, rng: [][2]float64{{0, 1 << 30}}}
		fn.prog = parsePSNodes(strings.Fields(sb.String()))
		if _, err := evalPSProgram(fn, nil); err != nil {
			t.Errorf("eval: %v", err)
		}
	})
}

// TestParsePSNodesToleratesUnmatchedBraces verifies stray and unclosed
// braces do not error: the stray "}" is dropped and the unclosed group's
// tokens stay unexecutable.
func TestParsePSNodesToleratesUnmatchedBraces(t *testing.T) {
	nodes := parsePSNodes([]string{"}", "5", "{", "6"})
	if len(nodes) != 2 || nodes[0].tok != "5" || !nodes[1].isProc {
		t.Fatalf("nodes = %+v, want [5, proc([6])]", nodes)
	}
}
