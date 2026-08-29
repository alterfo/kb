package chunk

import "unicode/utf8"

func EstimateTokens(text string) int {
	n := utf8.RuneCountInString(text)
	if n == 0 {
		return 0
	}
	t := n / 4
	if t == 0 {
		t = 1
	}
	return t
}
