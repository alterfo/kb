package plepatch

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
)

type DefaultSpec struct {
	At     string
	Heads  []int
	Orders []int
	Note   string
	Prefix string
}

type EntrySpec struct {
	Trigger  string
	Op       string
	At       string
	Heads    []int
	Orders   []int
	Note     string
	Prefix   string
	Vector   json.RawMessage
	CopyFrom string
	Seed     int64
	SeedSet  bool
}

type Knowledge struct {
	Defaults DefaultSpec
	Entries  []EntrySpec
}

type RowReader func(row uint64) ([]float32, error)

type PlanEntry struct {
	Trigger string
	Op      string
	Note    string
	Tokens  []int64
	Rows    []uint64
	Vector  []float32
}

type Plan struct {
	Entries    []PlanEntry
	RowOps     map[uint64][]float32
	Touched    int
	Collisions int
}

func (p *Plan) UniqueRows() int {
	return len(p.RowOps)
}

type rawDefaults struct {
	At     json.RawMessage `json:"at"`
	Heads  json.RawMessage `json:"heads"`
	Orders json.RawMessage `json:"orders"`
	Note   *string         `json:"note"`
	Prefix *string         `json:"prefix"`
}

type rawEntry struct {
	Trigger  string          `json:"trigger"`
	Op       string          `json:"op"`
	At       json.RawMessage `json:"at"`
	Heads    json.RawMessage `json:"heads"`
	Orders   json.RawMessage `json:"orders"`
	Note     *string         `json:"note"`
	Prefix   *string         `json:"prefix"`
	Vector   json.RawMessage `json:"vector"`
	CopyFrom string          `json:"copy_from"`
	Seed     *int64          `json:"seed"`
}

func ParseKnowledge(data []byte) (*Knowledge, error) {
	var doc struct {
		Defaults *rawDefaults `json:"defaults"`
		Entries  []rawEntry   `json:"entries"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("plepatch: parse knowledge: %w", err)
	}

	knowledge := &Knowledge{Defaults: DefaultSpec{At: "last"}}
	if doc.Defaults != nil {
		def := doc.Defaults
		at, err := parseAt(def.At, "last")
		if err != nil {
			return nil, fmt.Errorf("plepatch: defaults.at: %w", err)
		}
		heads, err := parseHeadList(def.Heads)
		if err != nil {
			return nil, fmt.Errorf("plepatch: defaults.heads: %w", err)
		}
		orders, err := parseOrderList(def.Orders)
		if err != nil {
			return nil, fmt.Errorf("plepatch: defaults.orders: %w", err)
		}
		knowledge.Defaults = DefaultSpec{
			At:     at,
			Heads:  heads,
			Orders: orders,
			Note:   stringPtr(def.Note),
			Prefix: stringPtr(def.Prefix),
		}
	}

	for i := range doc.Entries {
		raw := doc.Entries[i]
		entry := EntrySpec{
			Trigger:  raw.Trigger,
			Op:       raw.Op,
			At:       knowledge.Defaults.At,
			Heads:    knowledge.Defaults.Heads,
			Orders:   knowledge.Defaults.Orders,
			Note:     knowledge.Defaults.Note,
			Prefix:   knowledge.Defaults.Prefix,
			Vector:   raw.Vector,
			CopyFrom: raw.CopyFrom,
		}
		if raw.Seed != nil {
			entry.Seed = *raw.Seed
			entry.SeedSet = true
		}
		if len(raw.At) > 0 {
			at, err := parseAt(raw.At, "")
			if err != nil {
				return nil, fmt.Errorf("plepatch: entry %d at: %w", i, err)
			}
			entry.At = at
		}
		if len(raw.Heads) > 0 {
			heads, err := parseHeadList(raw.Heads)
			if err != nil {
				return nil, fmt.Errorf("plepatch: entry %d heads: %w", i, err)
			}
			entry.Heads = heads
		}
		if len(raw.Orders) > 0 {
			orders, err := parseOrderList(raw.Orders)
			if err != nil {
				return nil, fmt.Errorf("plepatch: entry %d orders: %w", i, err)
			}
			entry.Orders = orders
		}
		if raw.Note != nil {
			entry.Note = *raw.Note
		}
		if raw.Prefix != nil {
			entry.Prefix = *raw.Prefix
		}

		if strings.TrimSpace(entry.Trigger) == "" {
			return nil, fmt.Errorf("plepatch: entry %d trigger is required", i)
		}
		switch entry.Op {
		case "set", "zero", "random", "copy_from":
		default:
			return nil, fmt.Errorf("plepatch: entry %d unknown op %q", i, entry.Op)
		}
		if entry.Op == "set" && len(entry.Vector) == 0 {
			return nil, fmt.Errorf("plepatch: entry %d op set requires vector", i)
		}
		if entry.Op == "copy_from" && strings.TrimSpace(entry.CopyFrom) == "" {
			return nil, fmt.Errorf("plepatch: entry %d op copy_from requires copy_from", i)
		}
		knowledge.Entries = append(knowledge.Entries, entry)
	}

	return knowledge, nil
}

func (k *Knowledge) BuildPlan(c *Constants, tok *Tokenizer, rowDim int, readRow RowReader) (*Plan, error) {
	if c == nil {
		return nil, fmt.Errorf("plepatch: nil constants")
	}
	if tok == nil {
		return nil, fmt.Errorf("plepatch: nil tokenizer")
	}
	if rowDim <= 0 {
		return nil, fmt.Errorf("plepatch: row_dim=%d, want > 0", rowDim)
	}

	plan := &Plan{RowOps: make(map[uint64][]float32)}
	for i := range k.Entries {
		entry := &k.Entries[i]
		text := entry.Prefix + entry.Trigger
		ids := tok.Encode(text)
		tokens := make([]int64, len(ids))
		for j, id := range ids {
			tokens[j] = int64(id)
		}
		if len(tokens) == 0 {
			return nil, fmt.Errorf("plepatch: entry %d tokenized to no tokens", i)
		}

		seqRows := c.RowsForSequence(tokens)
		positions, err := positionsFor(entry.At, len(seqRows))
		if err != nil {
			return nil, fmt.Errorf("plepatch: entry %d at=%q: %w", i, entry.At, err)
		}
		heads, err := selectedHeadIndices(entry.Heads, entry.Orders, c)
		if err != nil {
			return nil, fmt.Errorf("plepatch: entry %d: %w", i, err)
		}

		var targetRows []uint64
		for _, pos := range positions {
			targetRows = append(targetRows, rowsForHeads(seqRows[pos], heads)...)
		}

		var vector []float32
		switch entry.Op {
		case "set":
			vector, err = resolveSetVector(entry.Vector, rowDim, entry.Seed)
		case "zero":
			vector = make([]float32, rowDim)
		case "random":
			vector = seededNormal(rowDim, entry.Seed)
		case "copy_from":
			vector, err = resolveCopyFrom(entry.CopyFrom, rowDim, c, tok, heads, readRow)
		}
		if err != nil {
			return nil, fmt.Errorf("plepatch: entry %d: %w", i, err)
		}

		pe := PlanEntry{
			Trigger: entry.Trigger,
			Op:      entry.Op,
			Note:    entry.Note,
			Tokens:  append([]int64(nil), tokens...),
			Rows:    uniquePreserve(targetRows),
			Vector:  append([]float32(nil), vector...),
		}

		for _, row := range targetRows {
			plan.Touched++
			if _, exists := plan.RowOps[row]; exists {
				plan.Collisions++
			}
			plan.RowOps[row] = vector
		}
		plan.Entries = append(plan.Entries, pe)
	}

	return plan, nil
}

func stringPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func parseAt(raw json.RawMessage, fallback string) (string, error) {
	if len(raw) == 0 {
		return fallback, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", err
		}
		switch s {
		case "last", "all", "first":
			return s, nil
		default:
			return "", fmt.Errorf("invalid position %q, want last, all, first, or integer", s)
		}
	}
	var n int64
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return "", fmt.Errorf("invalid position %s", trimmed)
	}
	return strconv.FormatInt(n, 10), nil
}

func parseHeadList(raw json.RawMessage) ([]int, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, err
		}
		if s != "all" {
			return nil, fmt.Errorf("invalid heads %q, want all or an integer list", s)
		}
		return nil, nil
	}
	var list []int
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return nil, fmt.Errorf("invalid heads: %w", err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("heads list is empty")
	}
	for _, h := range list {
		if h < 0 {
			return nil, fmt.Errorf("negative head %d", h)
		}
	}
	return list, nil
}

func parseOrderList(raw json.RawMessage) ([]int, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, err
		}
		if s != "all" {
			return nil, fmt.Errorf("invalid orders %q, want all or an integer list", s)
		}
		return nil, nil
	}
	var list []int
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return nil, fmt.Errorf("invalid orders: %w", err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("orders list is empty")
	}
	for _, o := range list {
		if o < 0 {
			return nil, fmt.Errorf("negative order %d", o)
		}
	}
	return list, nil
}

func positionsFor(at string, n int) ([]int, error) {
	switch at {
	case "all":
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out, nil
	case "last":
		return []int{n - 1}, nil
	case "first":
		return []int{0}, nil
	default:
		idx, err := strconv.Atoi(at)
		if err != nil || idx < 0 || idx >= n {
			return nil, fmt.Errorf("invalid position %q for %d tokens", at, n)
		}
		return []int{idx}, nil
	}
}

func selectedHeadIndices(heads, orders []int, c *Constants) ([]int, error) {
	var fromHeads []bool
	if heads != nil {
		fromHeads = make([]bool, c.NHeads())
		for _, h := range heads {
			if h < 0 || h >= c.NHeads() {
				return nil, fmt.Errorf("head %d out of range [0,%d)", h, c.NHeads())
			}
			fromHeads[h] = true
		}
	}

	var fromOrders []bool
	if orders != nil {
		fromOrders = make([]bool, c.NHeads())
		for _, o := range orders {
			if o < 2 || o > c.NgramSize {
				return nil, fmt.Errorf("order %d out of range [2,%d]", o, c.NgramSize)
			}
			base := (o - 2) * c.HeadsPerNgram
			for g := 0; g < c.HeadsPerNgram; g++ {
				fromOrders[base+g] = true
			}
		}
	}

	var selected []int
	for h := 0; h < c.NHeads(); h++ {
		if (fromHeads == nil || fromHeads[h]) && (fromOrders == nil || fromOrders[h]) {
			selected = append(selected, h)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("heads/orders filters select no heads")
	}
	return selected, nil
}

func rowsForHeads(rows []uint64, heads []int) []uint64 {
	out := make([]uint64, len(heads))
	for i, h := range heads {
		out[i] = rows[h]
	}
	return out
}

func uniquePreserve(rows []uint64) []uint64 {
	seen := make(map[uint64]bool, len(rows))
	out := make([]uint64, 0, len(rows))
	for _, row := range rows {
		if seen[row] {
			continue
		}
		seen[row] = true
		out = append(out, row)
	}
	return out
}

func resolveSetVector(raw json.RawMessage, rowDim int, seed int64) ([]float32, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("op set requires vector")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, err
		}
		switch s {
		case "zero":
			return make([]float32, rowDim), nil
		case "random":
			return seededNormal(rowDim, seed), nil
		default:
			return readVectorFile(s, rowDim)
		}
	}
	var vec []float32
	if err := json.Unmarshal(trimmed, &vec); err != nil {
		return nil, fmt.Errorf("vector must be a float32 array, path, random, or zero: %w", err)
	}
	if len(vec) != rowDim {
		return nil, fmt.Errorf("inline vector has %d elements, want %d", len(vec), rowDim)
	}
	return vec, nil
}

func readVectorFile(path string, rowDim int) ([]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vector %q: %w", path, err)
	}
	if strings.HasSuffix(path, ".json") {
		var vec []float32
		if err := json.Unmarshal(data, &vec); err != nil {
			return nil, fmt.Errorf("vector json %q: %w", path, err)
		}
		if len(vec) != rowDim {
			return nil, fmt.Errorf("vector json %q has %d elements, want %d", path, len(vec), rowDim)
		}
		return vec, nil
	}
	if len(data) != rowDim*4 {
		return nil, fmt.Errorf("vector file %q is %d bytes, want %d (raw float32 x %d)", path, len(data), rowDim*4, rowDim)
	}
	vec := make([]float32, rowDim)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4 : i*4+4]))
	}
	return vec, nil
}

func seededNormal(rowDim int, seed int64) []float32 {
	rng := rand.New(rand.NewPCG(uint64(seed), 0))
	vec := make([]float32, rowDim)
	for i := range vec {
		vec[i] = float32(rng.NormFloat64() * 0.02)
	}
	return vec
}

func resolveCopyFrom(source string, rowDim int, c *Constants, tok *Tokenizer, heads []int, readRow RowReader) ([]float32, error) {
	if readRow == nil {
		return nil, fmt.Errorf("copy_from requires a table row reader")
	}
	ids := tok.Encode(source)
	tokens := make([]int64, len(ids))
	for i, id := range ids {
		tokens[i] = int64(id)
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("copy_from tokenized to no tokens")
	}
	seqRows := c.RowsForSequence(tokens)
	sourceRows := rowsForHeads(seqRows[len(seqRows)-1], heads)

	total := make([]float32, rowDim)
	for _, row := range sourceRows {
		vec, err := readRow(row)
		if err != nil {
			return nil, fmt.Errorf("read row %d: %w", row, err)
		}
		if len(vec) != rowDim {
			return nil, fmt.Errorf("row %d has %d elements, want %d", row, len(vec), rowDim)
		}
		for j := range total {
			total[j] += vec[j]
		}
	}
	for j := range total {
		total[j] /= float32(len(sourceRows))
	}
	return total, nil
}
