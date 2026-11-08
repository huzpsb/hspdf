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

// TestLexerJunkRunsAreIterative verifies that runs of single byte junk
// delimiters and repeated dictionaries do not recurse per byte: the
// pre-fix implementation consumed them through self recursion and crashed
// with a fatal stack overflow on multi megabyte junk runs.
func TestLexerJunkRunsAreIterative(t *testing.T) {
	for _, tc := range []struct {
		name string
		bb   []byte
	}{
		{"closingBraces", []byte(strings.Repeat("}", 4_000_000))},
		{"closingParens", []byte(strings.Repeat(")", 4_000_000))},
		{"junkGreaterThan", []byte(strings.Repeat(">", 4_000_000))},
		{"procBraces", []byte(strings.Repeat("{", 4_000_000))},
		{"dicts", []byte(strings.Repeat("<<>>", 1_000_000))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustFinish(t, 30*time.Second, tc.name, func() {
				lx := newLexer(tc.bb)
				for {
					tok, err := lx.next()
					if err != nil {
						t.Errorf("lexer error: %v", err)
						return
					}
					if tok.kind == tokEOF {
						return
					}
				}
			})
		})
	}
}

// TestLexerJunkRunBetweenTokens verifies tokens around junk runs still parse.
func TestLexerJunkRunBetweenTokens(t *testing.T) {
	lx := newLexer([]byte("1 }}}} 2 re f >>>>>>>>>> Q"))
	var ops []string
	for {
		tok, err := lx.next()
		if err != nil {
			t.Fatal(err)
		}
		if tok.kind == tokEOF {
			break
		}
		if tok.kind == tokOp {
			ops = append(ops, tok.op)
		}
	}
	if len(ops) != 3 || ops[0] != "re" || ops[1] != "f" || ops[2] != "Q" {
		t.Fatalf("operators = %v, want [re f Q]", ops)
	}
}

// TestLexerRepeatedBINotRecursive verifies a junk run of BI operators does
// not recurse once per operator (pre-fix: fatal stack overflow via the
// next()/inlineImage() cycle).
func TestLexerRepeatedBINotRecursive(t *testing.T) {
	bb := []byte(strings.Repeat("BI ", 2_000_000))
	mustFinish(t, 30*time.Second, "repeated BI", func() {
		lx := newLexer(bb)
		for {
			tok, err := lx.next()
			if err != nil {
				// Expected: unterminated inline image.
				return
			}
			if tok.kind == tokEOF {
				return
			}
		}
	})
}
