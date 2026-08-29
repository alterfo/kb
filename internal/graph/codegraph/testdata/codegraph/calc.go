package calc

import (
	"fmt"
	"strings"
)

type Calculator interface {
	Add(a, b int) int
}

type IntCalc struct {
	precision int
}

func (c *IntCalc) Add(a, b int) int {
	return c.round(a + b)
}

func (c *IntCalc) round(n int) int {
	return n
}

func Sum(nums []int) int {
	total := 0
	for _, n := range nums {
		total = Add(total, n)
	}
	return total
}

func Add(a, b int) int {
	return a + b
}

func Report(tag string) string {
	label := strings.TrimSpace(tag)
	return fmt.Sprintf("sum=%s", label)
}
