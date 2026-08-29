package chunk

type Chunk struct {
	Text       string
	Index      int
	TokenCount int
	Metadata   map[string]string
}
