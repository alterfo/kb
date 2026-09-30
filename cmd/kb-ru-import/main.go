package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/alterfo/kb/internal/bench/ruimport"
)

func main() {
	src := flag.String("src", "", "markdown source root")
	out := flag.String("out", "", "corpus output root")
	sourceType := flag.String("source-type", "doka", "source type directory name")
	sections := flag.String("sections", "", "comma-separated top-level sections (empty = all)")
	limit := flag.Int("limit", 0, "max documents to import (0 = all)")
	flag.Parse()
	if *src == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "kb-ru-import: -src and -out are required")
		os.Exit(2)
	}
	var secs []string
	if *sections != "" {
		for _, s := range strings.Split(*sections, ",") {
			if s = strings.TrimSpace(s); s != "" {
				secs = append(secs, s)
			}
		}
	}
	res, err := ruimport.Import(ruimport.Options{
		SourceType: *sourceType,
		Root:       *src,
		OutDir:     *out,
		Sections:   secs,
		Limit:      *limit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kb-ru-import: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("kb-ru-import: imported %d documents, skipped %d\n", res.Imported, res.Skipped)
	for _, w := range res.Warnings {
		fmt.Printf("kb-ru-import: warning: %s\n", w)
	}
}
