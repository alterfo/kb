package actualize

import "github.com/alterfo/kb/internal/connector"

func SeedDocuments() []connector.Document {
	docs := make([]connector.Document, 0, len(SeedDocs()))
	for _, d := range SeedDocs() {
		docs = append(docs, connector.Document{
			ID:     d.ID,
			Source: "seed",
			Title:  d.Title,
			Body:   d.Body,
		})
	}
	return docs
}
