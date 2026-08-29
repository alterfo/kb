package shapes

import "example.com/nonexistent"

type Square struct {
	size int
}

func (s *Square) Area() int {
	return s.size * s.size
}

func Make(size int) *Square {
	return NewSquare(size)
}

func NewSquare(size int) *Square {
	return &Square{size: size}
}
