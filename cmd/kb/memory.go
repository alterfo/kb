package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/gguf"
	"github.com/alterfo/kb/internal/plepatch"
)

const memoryTableTensor = "per_layer_token_embd.weight"

type memoryCompileResult struct {
	model       string
	qtype       uint32
	rowDim      uint64
	bytesPerRow uint64
	nRows       uint64
	ple         plepatch.PLEInfo
	plan        *plepatch.Plan
	overlay     *plepatch.Overlay
}

type memoryEntryReport struct {
	Trigger string   `json:"trigger"`
	Op      string   `json:"op"`
	Note    string   `json:"note,omitempty"`
	Tokens  []int64  `json:"tokens"`
	Rows    []uint64 `json:"rows"`
}

type memoryCompileReport struct {
	Model       string              `json:"model"`
	TableTensor string              `json:"table_tensor"`
	QType       uint32              `json:"qtype"`
	RowDim      uint64              `json:"row_dim"`
	BytesPerRow uint64              `json:"bytes_per_row"`
	NRows       uint64              `json:"n_rows"`
	PLE         plepatch.PLEInfo    `json:"ple"`
	Entries     []memoryEntryReport `json:"entries"`
	UniqueRows  int                 `json:"unique_rows"`
	Touched     int                 `json:"touched"`
	Collisions  int                 `json:"collisions"`
	Output      string              `json:"output,omitempty"`
	DryRun      bool                `json:"dry_run,omitempty"`
}

func runMemoryCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "compile" {
		fmt.Fprintln(stderr, memoryUsage())
		return 2
	}

	fset := flag.NewFlagSet("memory compile", flag.ContinueOnError)
	fset.SetOutput(stderr)
	ggufPath := fset.String("gguf", "", "path to the model GGUF file (first shard)")
	knowledgePath := fset.String("knowledge", "", "path to knowledge JSON")
	outPath := fset.String("out", "", "output .plepatch path (default: knowledge path with .plepatch)")
	reportPath := fset.String("report", "", "write compile report JSON to this path")
	dryRun := fset.Bool("dry-run", false, "build the plan without writing .plepatch")
	if err := fset.Parse(args[1:]); err != nil {
		return 2
	}
	if fset.NArg() != 0 {
		fmt.Fprintf(stderr, "memory compile: unexpected arguments: %v\n", fset.Args())
		return 2
	}
	if *ggufPath == "" {
		fmt.Fprintln(stderr, "memory compile: -gguf is required")
		return 2
	}
	if *knowledgePath == "" {
		fmt.Fprintln(stderr, "memory compile: -knowledge is required")
		return 2
	}

	output := *outPath
	if output == "" {
		ext := filepath.Ext(*knowledgePath)
		output = strings.TrimSuffix(*knowledgePath, ext) + ".plepatch"
	}

	result, err := compileMemory(*ggufPath, *knowledgePath)
	if err != nil {
		fmt.Fprintf(stderr, "memory compile: %v\n", err)
		return 1
	}

	printMemoryCompile(stdout, output, *dryRun, result)

	if *dryRun {
		return 0
	}

	if *reportPath != "" {
		report := memoryReport(result, output, false)
		if err := writeMemoryReport(*reportPath, report); err != nil {
			fmt.Fprintf(stderr, "memory compile: write report: %v\n", err)
			return 1
		}
	}

	if err := result.overlay.WriteAtomic(output); err != nil {
		fmt.Fprintf(stderr, "memory compile: write patch: %v\n", err)
		return 1
	}
	return 0
}

func memoryUsage() string {
	return "usage: kb memory compile -gguf <model.gguf> -knowledge <knowledge.json> [-out <model.gguf.plepatch>] [-report <report.json>] [-dry-run]"
}

func compileMemory(ggufPath, knowledgePath string) (*memoryCompileResult, error) {
	knowledgeData, err := os.ReadFile(knowledgePath)
	if err != nil {
		return nil, fmt.Errorf("read knowledge %q: %w", knowledgePath, err)
	}
	knowledge, err := plepatch.ParseKnowledge(knowledgeData)
	if err != nil {
		return nil, err
	}

	shards, err := gguf.DiscoverShards(ggufPath)
	if err != nil {
		return nil, fmt.Errorf("discover shards %q: %w", ggufPath, err)
	}
	loc, err := gguf.LocateTensor(shards, memoryTableTensor)
	if err != nil {
		return nil, err
	}
	if loc.Type != 8 {
		return nil, fmt.Errorf("table tensor %q has quant type %d, want Q8_0 (8)", memoryTableTensor, loc.Type)
	}
	if loc.RowDim == 0 || loc.NRows == 0 || loc.NBytes <= 0 {
		return nil, fmt.Errorf("table tensor %q has invalid shape dims=%v", memoryTableTensor, loc.Dims)
	}
	if loc.NBytes%int64(loc.NRows) != 0 {
		return nil, fmt.Errorf("table tensor %q byte size %d is not divisible by n_rows %d", memoryTableTensor, loc.NBytes, loc.NRows)
	}
	bytesPerRow := uint64(loc.NBytes / int64(loc.NRows))
	rowDim := loc.RowDim
	if rowDim%32 != 0 {
		return nil, fmt.Errorf("table tensor %q row_dim=%d is not divisible by 32", memoryTableTensor, rowDim)
	}
	if bytesPerRow != rowDim/32*34 {
		return nil, fmt.Errorf("table tensor %q bytes_per_row=%d, want %d for Q8_0 row_dim=%d", memoryTableTensor, bytesPerRow, rowDim/32*34, rowDim)
	}
	if rowDim > uint64(maxInt()) {
		return nil, fmt.Errorf("table tensor %q row_dim=%d overflows int", memoryTableTensor, rowDim)
	}

	metaFile, err := gguf.Open(ggufPath)
	if err != nil {
		return nil, fmt.Errorf("open GGUF %q: %w", ggufPath, err)
	}
	constants, err := plepatch.LoadConstants(metaFile)
	if err != nil {
		metaFile.Close()
		return nil, err
	}
	tokenizer, err := plepatch.LoadTokenizer(metaFile)
	if err != nil {
		metaFile.Close()
		return nil, err
	}
	model := metadataString(metaFile, "general.name")
	if model == "" {
		model = strings.TrimSuffix(filepath.Base(ggufPath), filepath.Ext(ggufPath))
	}
	if err := metaFile.Close(); err != nil {
		return nil, fmt.Errorf("close GGUF %q: %w", ggufPath, err)
	}

	if constants.NRows() != loc.NRows {
		return nil, fmt.Errorf("table tensor %q n_rows=%d does not match PLE metadata n_rows=%d", memoryTableTensor, loc.NRows, constants.NRows())
	}

	reader := func(row uint64) ([]float32, error) {
		raw, err := readTableRow(loc.Path, loc.DataOffset, bytesPerRow, loc.NRows, row)
		if err != nil {
			return nil, err
		}
		return plepatch.DecodeRow(raw)
	}

	plan, err := knowledge.BuildPlan(constants, tokenizer, int(rowDim), reader)
	if err != nil {
		return nil, err
	}

	rowIDs := make([]uint64, 0, len(plan.RowOps))
	for row := range plan.RowOps {
		rowIDs = append(rowIDs, row)
	}
	sort.Slice(rowIDs, func(i, j int) bool { return rowIDs[i] < rowIDs[j] })

	rawRows := make([][]byte, 0, len(rowIDs))
	rows := make([]plepatch.RowRecord, 0, len(rowIDs))
	for _, row := range rowIDs {
		raw, err := readTableRow(loc.Path, loc.DataOffset, bytesPerRow, loc.NRows, row)
		if err != nil {
			return nil, err
		}
		rawRows = append(rawRows, raw)
		payload, err := plepatch.EncodeRow(plan.RowOps[row])
		if err != nil {
			return nil, fmt.Errorf("encode row %d: %w", row, err)
		}
		if uint64(len(payload)) != bytesPerRow {
			return nil, fmt.Errorf("encode row %d produced %d bytes, want %d", row, len(payload), bytesPerRow)
		}
		rows = append(rows, plepatch.RowRecord{RowID: row, Payload: payload})
	}

	entries := make([]plepatch.OverlayEntry, 0, len(plan.Entries))
	for _, entry := range plan.Entries {
		entries = append(entries, plepatch.OverlayEntry{
			Trigger: entry.Trigger,
			Op:      entry.Op,
			Note:    entry.Note,
		})
	}

	overlay, err := plepatch.NewOverlay(plepatch.OverlayParams{
		Model:       model,
		TableTensor: memoryTableTensor,
		SHA1Before:  plepatch.SHA1RowBytes(rawRows),
		RowDim:      rowDim,
		QType:       uint32(loc.Type),
		BytesPerRow: bytesPerRow,
		NRows:       loc.NRows,
		PLE: plepatch.PLEInfo{
			NgramSize:     constants.NgramSize,
			HeadsPerNgram: constants.HeadsPerNgram,
			EosTokenID:    constants.EosTokenID,
		},
		Entries: entries,
		Rows:    rows,
	})
	if err != nil {
		return nil, err
	}

	return &memoryCompileResult{
		model:       model,
		qtype:       uint32(loc.Type),
		rowDim:      rowDim,
		bytesPerRow: bytesPerRow,
		nRows:       loc.NRows,
		ple:         overlay.Manifest.PLE,
		plan:        plan,
		overlay:     overlay,
	}, nil
}

func metadataString(f *gguf.File, key string) string {
	v, ok := f.MetadataValue(key)
	if !ok {
		return ""
	}
	s, err := v.AsString()
	if err != nil {
		return ""
	}
	return s
}

func readTableRow(path string, dataOffset int64, bytesPerRow, nRows, row uint64) ([]byte, error) {
	if row >= nRows {
		return nil, fmt.Errorf("row %d out of range [0,%d)", row, nRows)
	}
	if bytesPerRow > uint64(maxInt()) {
		return nil, fmt.Errorf("bytes_per_row %d overflows int", bytesPerRow)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open tensor file %q: %w", path, err)
	}
	defer f.Close()

	raw := make([]byte, int(bytesPerRow))
	off := dataOffset + int64(row)*int64(bytesPerRow)
	n, err := f.ReadAt(raw, off)
	if err != nil {
		return nil, fmt.Errorf("read row %d at offset %d: %w", row, off, err)
	}
	if n != len(raw) {
		return nil, fmt.Errorf("read row %d at offset %d: got %d bytes, want %d", row, off, n, len(raw))
	}
	return raw, nil
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func printMemoryCompile(w io.Writer, output string, dryRun bool, r *memoryCompileResult) {
	fmt.Fprintf(w, "model: %s\n", r.model)
	fmt.Fprintf(w, "table: %s (qtype=%d, row_dim=%d, bytes_per_row=%d, n_rows=%d)\n", memoryTableTensor, r.qtype, r.rowDim, r.bytesPerRow, r.nRows)
	fmt.Fprintf(w, "ple: ngram_size=%d heads_per_ngram=%d eos_token_id=%d\n", r.ple.NgramSize, r.ple.HeadsPerNgram, r.ple.EosTokenID)
	fmt.Fprintf(w, "plan: entries=%d unique_rows=%d touched=%d collisions=%d\n", len(r.plan.Entries), r.plan.UniqueRows(), r.plan.Touched, r.plan.Collisions)
	for i, entry := range r.plan.Entries {
		note := ""
		if entry.Note != "" {
			note = " note=" + entry.Note
		}
		fmt.Fprintf(w, "entry[%d]: trigger=%q op=%s tokens=%d rows=%d%s\n", i, entry.Trigger, entry.Op, len(entry.Tokens), len(entry.Rows), note)
	}
	if dryRun {
		fmt.Fprintln(w, "dry-run: no file written")
	} else {
		fmt.Fprintf(w, "wrote %s\n", output)
	}
}

func memoryReport(r *memoryCompileResult, output string, dryRun bool) memoryCompileReport {
	entries := make([]memoryEntryReport, 0, len(r.plan.Entries))
	for _, entry := range r.plan.Entries {
		entries = append(entries, memoryEntryReport{
			Trigger: entry.Trigger,
			Op:      entry.Op,
			Note:    entry.Note,
			Tokens:  append([]int64(nil), entry.Tokens...),
			Rows:    append([]uint64(nil), entry.Rows...),
		})
	}
	return memoryCompileReport{
		Model:       r.model,
		TableTensor: memoryTableTensor,
		QType:       r.qtype,
		RowDim:      r.rowDim,
		BytesPerRow: r.bytesPerRow,
		NRows:       r.nRows,
		PLE:         r.ple,
		Entries:     entries,
		UniqueRows:  r.plan.UniqueRows(),
		Touched:     r.plan.Touched,
		Collisions:  r.plan.Collisions,
		Output:      output,
		DryRun:      dryRun,
	}
}

func writeMemoryReport(path string, report memoryCompileReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
