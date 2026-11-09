/*
Copyright 2026 The pdfcpu Authors.

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

package pdfcpu

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/huzpsb/hspdf/pkg/filter"
	"github.com/huzpsb/hspdf/pkg/pdfcpu/model"
	"github.com/huzpsb/hspdf/pkg/pdfcpu/types"
)

// TestPDFFilterPipelineRejectsOversizedArrays verifies the read-side cap on
// filter arrays: pipelines longer than types.MaxFilterPipelineLen must not
// be built.
func TestPDFFilterPipelineRejectsOversizedArrays(t *testing.T) {
	ctx, err := model.NewContext(bytes.NewReader([]byte("%PDF-1.7\n")), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}

	arr := types.Array{}
	for i := 0; i <= types.MaxFilterPipelineLen; i++ {
		arr = append(arr, types.Name(filter.Flate))
	}
	d := types.Dict{"Filter": arr}

	_, err = pdfFilterPipeline(context.Background(), ctx, d)
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("want pipeline length error, got %v", err)
	}

	// A maximum length array is accepted on length grounds.
	d = types.Dict{"Filter": arr[:types.MaxFilterPipelineLen]}
	if _, err := pdfFilterPipeline(context.Background(), ctx, d); err != nil {
		t.Fatalf("pipeline of %d filters must be accepted, got %v", types.MaxFilterPipelineLen, err)
	}
}
