package run

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type StatDelta struct {
	AvgRecall float64 `json:"avg_recall"`
	Abstain   int     `json:"abstain"`
	Cited     int     `json:"cited"`
}

type CompareResult struct {
	TotalDelta        int                  `json:"total_delta"`
	AbstainTotalDelta int                  `json:"abstain_total_delta"`
	Types             map[string]StatDelta `json:"types"`
	Languages         map[string]StatDelta `json:"languages"`
}

func Compare(baseline, candidate *Report) CompareResult {
	if baseline == nil {
		baseline = &Report{}
	}
	if candidate == nil {
		candidate = &Report{}
	}

	res := CompareResult{
		TotalDelta:        candidate.Total - baseline.Total,
		AbstainTotalDelta: candidate.AbstainTotal - baseline.AbstainTotal,
		Types:             map[string]StatDelta{},
		Languages:         map[string]StatDelta{},
	}

	for k := range baseline.Types {
		res.Types[k] = statDelta(baseline.Types[k], candidate.Types[k])
	}
	for k := range candidate.Types {
		if _, ok := res.Types[k]; ok {
			continue
		}
		res.Types[k] = statDelta(nil, candidate.Types[k])
	}

	for k := range baseline.Languages {
		res.Languages[k] = statDelta(baseline.Languages[k], candidate.Languages[k])
	}
	for k := range candidate.Languages {
		if _, ok := res.Languages[k]; ok {
			continue
		}
		res.Languages[k] = statDelta(nil, candidate.Languages[k])
	}

	return res
}

func statDelta(baseline, candidate *TypeStat) StatDelta {
	var b, c TypeStat
	if baseline != nil {
		b = *baseline
	}
	if candidate != nil {
		c = *candidate
	}
	return StatDelta{
		AvgRecall: c.AvgRecall - b.AvgRecall,
		Abstain:   c.Abstain - b.Abstain,
		Cited:     c.Cited - b.Cited,
	}
}

func (d StatDelta) String() string {
	return fmt.Sprintf("recall=%+.3f abstain=%+d cited=%+d", d.AvgRecall, d.Abstain, d.Cited)
}

func (c CompareResult) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "total=%+d abstain=%+d\n", c.TotalDelta, c.AbstainTotalDelta)

	b.WriteString("languages:\n")
	for _, k := range sortedKeys(c.Languages) {
		fmt.Fprintf(&b, "  %s\t%s\n", k, c.Languages[k].String())
	}

	b.WriteString("types:\n")
	for _, k := range sortedKeys(c.Types) {
		fmt.Fprintf(&b, "  %s\t%s\n", k, c.Types[k].String())
	}

	return strings.TrimRight(b.String(), "\n")
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func LoadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bench: read report: %w", err)
	}
	var rep Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("bench: parse report: %w", err)
	}
	return &rep, nil
}

func SaveCompareResult(path string, res *CompareResult) error {
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("bench: encode compare result: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("bench: write compare result: %w", err)
	}
	return nil
}
